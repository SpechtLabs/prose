/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package controller implements the Memcached controller as a prose pipeline:
// a linear, observable sequence of named steps. There is no Reconcile method to
// write and no MemcachedReconciler struct to carry a client around. The builder
// chain in SetupWithManager is the whole controller — what it watches, the order
// of work, and where its telemetry goes — and the step functions below hold only
// business logic; not one of them logs, traces, or counts.
package controller

import (
	"reflect"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spechtlabs/prose"
	"go.opentelemetry.io/otel"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cachev1alpa1 "example.com/prose/memcached-operator/api/v1alpa1"
)

// +kubebuilder:rbac:groups=cache.example.com,resources=memcacheds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cache.example.com,resources=memcacheds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cache.example.com,resources=memcacheds/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// SetupWithManager builds the Memcached reconcile pipeline and registers it with
// the manager. Observability is configured once, here, and never touched again
// inside a step: every step becomes a child span, every group a parent span, and
// the whole reconcile collapses into exactly one wide log event. The pipeline
// reads top to bottom as what it does — converge the dependencies, then sync
// status — because that is exactly the order it runs.
func SetupWithManager(mgr ctrl.Manager) error {
	_, err := prose.For[*cachev1alpa1.Memcached](mgr).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		WithObservability(
			prose.Otel(otel.Tracer("memcached")),
			prose.WideEvents(mgr.GetLogger().WithName("memcached")), // one canonical line per reconcile
			prose.Recorder(mgr.GetEventRecorderFor("memcached")),    // kubectl-visible events
		).
		Describe("dependencies", func(g *prose.Group[*cachev1alpa1.Memcached]) {
			g.Step("deployment", upsertDeployment)
			g.Step("service", upsertService)
		}).
		Step("status", syncStatus).
		Complete()

	return err
}

// upsertDeployment converges the backing Deployment to the desired state.
//
// A single server-side Apply replaces the original three-part dance — "create if
// missing, then requeue", "fix the replica count", "fix the image" — with one
// declarative call. The desired object already encodes size and image, so there
// is nothing left to diff by hand and no create-then-requeue round trip. The
// controller owner reference is stamped by Apply from the Owns wiring above.
func upsertDeployment(rctx *prose.Context[*cachev1alpa1.Memcached]) (prose.Outcome, error) {
	m := rctx.Object() // already fetched, typed, no Get, no cast

	rctx.Set("deployment.replicas", m.Spec.Size)
	rctx.Set("deployment.image", m.Spec.Image)

	if err := rctx.Apply(deploymentForMemcached(m)); err != nil {
		return prose.Requeue, humane.Wrap(err, "apply memcached deployment",
			"verify the controller's ServiceAccount can create and update Deployments in this namespace")
	}

	return prose.Continue, nil
}

// upsertService converges the Service that fronts the Deployment. The Service is
// not required for memcached to work; it exists to show a second owned dependency
// reconciling under the same group span as the Deployment.
func upsertService(rctx *prose.Context[*cachev1alpa1.Memcached]) (prose.Outcome, error) {
	if err := rctx.Apply(serviceForMemcached(rctx.Object())); err != nil {
		return prose.Requeue, humane.Wrap(err, "apply memcached service",
			"verify the controller's ServiceAccount can create and update Services in this namespace")
	}

	return prose.Continue, nil
}

// syncStatus reflects the observed pods back into Memcached.Status.Nodes. Listing
// by label selector is outside what Apply models, so it reaches for the raw client
// through rctx — exactly the documented escape hatch, used inline without leaving
// the pipeline. The Kubernetes event fires only when the node set actually changes,
// because that is the one transition a human watching `kubectl describe` cares about.
func syncStatus(rctx *prose.Context[*cachev1alpa1.Memcached]) (prose.Outcome, error) {
	m := rctx.Object()

	pods := &corev1.PodList{}
	err := rctx.Client().List(rctx.Context(), pods,
		client.InNamespace(m.Namespace),
		client.MatchingLabels(labelsForMemcached(m.Name)),
	)
	if err != nil {
		return prose.Requeue, humane.Wrap(err, "list memcached pods",
			"verify the controller has RBAC to list Pods in this namespace")
	}

	names := getPodNames(pods.Items)
	rctx.Set("status.nodes", len(names))

	if reflect.DeepEqual(names, m.Status.Nodes) {
		return prose.Continue, nil
	}

	m.Status.Nodes = names
	if err := rctx.Client().Status().Update(rctx.Context(), m); err != nil {
		return prose.Requeue, humane.Wrap(err, "update memcached status",
			"verify the Memcached CRD has its status subresource enabled")
	}

	rctx.Event(corev1.EventTypeNormal, "NodesUpdated", "now tracking %d memcached pod(s)", len(names))
	return prose.Continue, nil
}

// deploymentForMemcached builds the desired Deployment for m. TypeMeta is set
// because server-side Apply is keyed by GVK; owner references are left to Apply.
func deploymentForMemcached(m *cachev1alpa1.Memcached) *appsv1.Deployment {
	ls := labelsForMemcached(m.Name)
	replicas := m.Spec.Size

	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: ls},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: ls},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:   m.Spec.Image,
						Name:    m.Name,
						Command: []string{"memcached", "-m=64", "-o", "modern", "-v"},
						Ports: []corev1.ContainerPort{{
							ContainerPort: 11211,
							Name:          m.Name,
						}},
					}},
				},
			},
		},
	}
}

// serviceForMemcached builds the desired Service for m.
func serviceForMemcached(m *cachev1alpa1.Memcached) *corev1.Service {
	ls := labelsForMemcached(m.Name)

	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: ls,
			Ports: []corev1.ServicePort{{
				Port: 11211,
				Name: m.Name,
			}},
		},
	}
}

// labelsForMemcached returns the labels for selecting the resources
// belonging to the given memcached CR name.
func labelsForMemcached(name string) map[string]string {
	return map[string]string{"app": "memcached", "memcached_cr": name}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}
