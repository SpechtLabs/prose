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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cachev1alpa1 "example.com/operator-sdk/memcached-operator/api/v1alpa1"
)

var _ = Describe("Memcached Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName = "test-resource"
			namespace    = "default"
			image        = "memcached:1.4.36"
			replicas     = int32(3)
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: namespace,
		}

		reconciler := &MemcachedReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Memcached")
			memcached := &cachev1alpa1.Memcached{}
			err := k8sClient.Get(ctx, typeNamespacedName, memcached)
			if err != nil && errors.IsNotFound(err) {
				resource := &cachev1alpa1.Memcached{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace,
					},
					Spec: cachev1alpa1.MemcachedSpec{
						Size:  replicas,
						Image: image,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("cleaning up the Memcached resource and its owned objects")
			resource := &cachev1alpa1.Memcached{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			deleteIfExists(ctx, &appsv1.Deployment{}, typeNamespacedName)
			deleteIfExists(ctx, &corev1.Service{}, typeNamespacedName)

			pods := &corev1.PodList{}
			Expect(k8sClient.List(ctx, pods,
				client.InNamespace(namespace),
				client.MatchingLabels(labelsForMemcached(resourceName)),
			)).To(Succeed())
			for i := range pods.Items {
				Expect(k8sClient.Delete(ctx, &pods.Items[i])).To(Succeed())
			}
		})

		It("should create the Deployment, Service, and reflect pods into status", func() {
			By("reconciling once to create the Deployment (and requeue)")
			res, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Requeue).To(BeTrue(), "first reconcile should requeue after creating the Deployment")

			By("checking the Deployment was created with the desired size")
			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, deployment)).To(Succeed())
			Expect(*deployment.Spec.Replicas).To(Equal(replicas))

			By("reconciling again to create the Service")
			res, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Requeue).To(BeFalse(), "second reconcile should not requeue")

			By("checking the Service was created")
			service := &corev1.Service{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, service)).To(Succeed())

			By("creating the pods that the Deployment would have produced")
			podNames := []string{resourceName + "-pod-0", resourceName + "-pod-1", resourceName + "-pod-2"}
			for _, name := range podNames {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
						Labels:    labelsForMemcached(resourceName),
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "memcached",
							Image: image,
						}},
					},
				}
				Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			}

			By("reconciling so the Memcached status is updated with the pod names")
			res, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{}), "final reconcile should return an empty Result")

			By("verifying the status reflects every backing pod")
			memcached := &cachev1alpa1.Memcached{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, memcached)).To(Succeed())
			Expect(memcached.Status.Nodes).To(ConsistOf(podNames))
		})
	})
})

// deleteIfExists deletes obj identified by key, tolerating a missing object so
// cleanup is idempotent across specs.
func deleteIfExists(ctx context.Context, obj client.Object, key types.NamespacedName) {
	if err := k8sClient.Get(ctx, key, obj); err == nil {
		Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
	}
}
