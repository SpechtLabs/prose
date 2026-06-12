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

package controller

import (
	"context"
	"reflect"
	"time"

	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	universev1alpha1 "example.com/operator-sdk/wormhole-operator/api/v1alpha1"
)

const (
	// stabilityStep is how much stability an anchor gains per stabilityInterval.
	stabilityStep = 10
	// stabilityInterval paces the stability climb in wall-clock time.
	stabilityInterval = 10 * time.Second
)

// AnchorReconciler reconciles an Anchor object.
type AnchorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Tracer   trace.Tracer
}

// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=anchors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=anchors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=anchors/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile creates the field-generator Deployment and tuning ConfigMap for an
// Anchor, then advances the Anchor stability status over time.
func (r *AnchorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	log := logf.FromContext(ctx)

	anchor := &universev1alpha1.Anchor{}
	if err = r.Get(ctx, req.NamespacedName, anchor); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Anchor resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get Anchor")
		return ctrl.Result{}, err
	}

	ctx, obs := beginReconcile(ctx, log, r.Tracer, "anchor", req, anchor.Generation)
	outcome := ""
	defer func() {
		obs.Finish(outcome, result, err)
	}()

	_, err = obs.Step(ctx, "configmap", func(ctx context.Context) (string, error) {
		cm := r.configMapForAnchor(anchor)
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
			cm.Data = map[string]string{"end": anchor.Spec.End, "coordinates": anchor.Spec.Coordinates}
			return controllerutil.SetControllerReference(anchor, cm, r.Scheme)
		})
		if err != nil {
			return "requeue", err
		}
		obs.Set("configmap.name", cm.Name)
		return "continue", nil
	})
	if err != nil {
		log.Error(err, "Failed to reconcile anchor tuning ConfigMap")
		return ctrl.Result{}, err
	}

	_, err = obs.Step(ctx, "deployment", func(ctx context.Context) (string, error) {
		deployment := r.deploymentForAnchor(anchor)
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
			replicas := int32(1)
			labels := labelsForAnchor(anchor.Name)
			deployment.Spec.Replicas = &replicas
			deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
			deployment.Spec.Template.ObjectMeta.Labels = labels
			deployment.Spec.Template.Spec.Containers = []corev1.Container{{
				Name:    "field-generator",
				Image:   "registry.k8s.io/pause:3.9",
				Command: []string{"/pause"},
			}}
			return controllerutil.SetControllerReference(anchor, deployment, r.Scheme)
		})
		if err != nil {
			return "requeue", err
		}
		obs.Set("deployment.name", deployment.Name)
		return "continue", nil
	})
	if err != nil {
		log.Error(err, "Failed to reconcile anchor field-generator Deployment")
		return ctrl.Result{}, err
	}

	originalStatus := anchor.Status
	_, err = obs.Step(ctx, "charge-stability", func(ctx context.Context) (string, error) {
		target := int32(time.Since(anchor.CreationTimestamp.Time)/stabilityInterval) * stabilityStep
		if target > anchor.Spec.TargetStability {
			target = anchor.Spec.TargetStability
		}
		if target > anchor.Status.Stability {
			anchor.Status.Stability = target
		}
		if anchor.Status.Stability >= anchor.Spec.TargetStability {
			anchor.Status.Phase = "Stable"
		} else {
			anchor.Status.Phase = "Forming"
		}
		obs.Set("stability", anchor.Status.Stability)
		obs.Set("target_stability", anchor.Spec.TargetStability)
		if anchor.Status.Stability < anchor.Spec.TargetStability {
			return "requeue_after", nil
		}
		return "continue", nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	_, err = obs.Step(ctx, "status", func(ctx context.Context) (string, error) {
		obs.Set("status.phase", anchor.Status.Phase)
		if !reflect.DeepEqual(originalStatus, anchor.Status) {
			if err := r.Status().Update(ctx, anchor); err != nil {
				return "requeue", err
			}
			if originalStatus.Phase != "Stable" && anchor.Status.Phase == "Stable" && r.Recorder != nil {
				r.Recorder.Eventf(anchor, corev1.EventTypeNormal, "Stable",
					"anchor reached %d%% stability", anchor.Status.Stability)
			}
		}
		return "continue", nil
	})
	if err != nil {
		log.Error(err, "Failed to update Anchor status")
		return ctrl.Result{}, err
	}

	if anchor.Status.Stability < anchor.Spec.TargetStability {
		result = ctrl.Result{RequeueAfter: stabilityInterval}
		outcome = "requeue_after"
		return ctrl.Result{RequeueAfter: stabilityInterval}, nil
	}
	outcome = "done"
	return ctrl.Result{}, nil
}

func (r *AnchorReconciler) configMapForAnchor(a *universev1alpha1.Anchor) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.Name + "-tuning",
			Namespace: a.Namespace,
		},
	}
}

// deploymentForAnchor returns the field-generator Deployment for an Anchor.
func (r *AnchorReconciler) deploymentForAnchor(a *universev1alpha1.Anchor) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.Name,
			Namespace: a.Namespace,
		},
	}
}

func labelsForAnchor(name string) map[string]string {
	return map[string]string{"app": "anchor", "anchor": name}
}

// SetupWithManager sets up the controller with the Manager.
func (r *AnchorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&universev1alpha1.Anchor{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&appsv1.Deployment{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.ConfigMap{}).
		Named("anchor").
		Complete(r)
}
