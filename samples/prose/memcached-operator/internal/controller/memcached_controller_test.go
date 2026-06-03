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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cachev1alpa1 "example.com/prose/memcached-operator/api/v1alpa1"
)

// Unlike a plain controller-runtime reconciler, a prose pipeline has no exported
// Reconcile method to call against a fake client. The pipeline is registered on a
// real manager in suite_test.go, so these specs drive it the way it runs in
// production: mutate the cluster and assert that the controller converges.
var _ = Describe("Memcached Controller", func() {
	Context("When reconciling a Memcached resource", func() {
		const (
			resourceName = "test-resource"
			namespace    = "default"
			image        = "memcached:1.4.36"
			replicas     = int32(3)

			timeout  = 10 * time.Second
			interval = 250 * time.Millisecond
		)

		ctx := context.Background()

		key := types.NamespacedName{Name: resourceName, Namespace: namespace}
		podNames := []string{resourceName + "-pod-0", resourceName + "-pod-1", resourceName + "-pod-2"}

		BeforeEach(func() {
			By("creating the pods the Deployment would produce")
			for _, name := range podNames {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
						Labels:    labelsForMemcached(resourceName),
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "memcached", Image: image}},
					},
				}
				Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			}

			By("creating the Memcached custom resource")
			Expect(k8sClient.Create(ctx, &cachev1alpa1.Memcached{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec: cachev1alpa1.MemcachedSpec{
					Size:  replicas,
					Image: image,
				},
			})).To(Succeed())
		})

		AfterEach(func() {
			By("deleting the Memcached resource so the controller stops reconciling it")
			memcached := &cachev1alpa1.Memcached{}
			if err := k8sClient.Get(ctx, key, memcached); err == nil {
				Expect(k8sClient.Delete(ctx, memcached)).To(Succeed())
			}
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, key, &cachev1alpa1.Memcached{}))
			}, timeout, interval).Should(BeTrue())

			By("cleaning up the owned objects and pods")
			deleteIfExists(ctx, &appsv1.Deployment{}, key)
			deleteIfExists(ctx, &corev1.Service{}, key)
			pods := &corev1.PodList{}
			Expect(k8sClient.List(ctx, pods,
				client.InNamespace(namespace),
				client.MatchingLabels(labelsForMemcached(resourceName)),
			)).To(Succeed())
			for i := range pods.Items {
				Expect(k8sClient.Delete(ctx, &pods.Items[i])).To(Succeed())
			}
		})

		It("creates the Deployment with the desired size", func() {
			Eventually(func(g Gomega) {
				deployment := &appsv1.Deployment{}
				g.Expect(k8sClient.Get(ctx, key, deployment)).To(Succeed())
				g.Expect(deployment.Spec.Replicas).NotTo(BeNil())
				g.Expect(*deployment.Spec.Replicas).To(Equal(replicas))
			}, timeout, interval).Should(Succeed())
		})

		It("creates the fronting Service", func() {
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key, &corev1.Service{})).To(Succeed())
			}, timeout, interval).Should(Succeed())
		})

		It("reflects the backing pods into status.Nodes", func() {
			Eventually(func(g Gomega) {
				memcached := &cachev1alpa1.Memcached{}
				g.Expect(k8sClient.Get(ctx, key, memcached)).To(Succeed())
				g.Expect(memcached.Status.Nodes).To(ConsistOf(podNames))
			}, timeout, interval).Should(Succeed())
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
