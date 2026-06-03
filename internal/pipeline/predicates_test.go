package pipeline

import (
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

var _ = ginkgo.DescribeTable("IgnoreStatusOnlyUpdates keeps real changes and drops status-only churn",
	func(mutate func(oldObj, newObj *corev1.ConfigMap), wantReconcile bool) {
		oldObj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "x", Generation: 1}}
		newObj := oldObj.DeepCopy()
		mutate(oldObj, newObj)

		got := IgnoreStatusOnlyUpdates().Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj})
		Expect(got).To(Equal(wantReconcile))
	},
	ginkgo.Entry("status-only update (nothing we track changed) is dropped",
		func(oldObj, newObj *corev1.ConfigMap) {}, false),
	ginkgo.Entry("a generation (spec) change reconciles",
		func(oldObj, newObj *corev1.ConfigMap) { newObj.Generation = 2 }, true),
	ginkgo.Entry("setting the deletion timestamp reconciles (Finalize safety)",
		func(oldObj, newObj *corev1.ConfigMap) { t := metav1.Now(); newObj.DeletionTimestamp = &t }, true),
	ginkgo.Entry("a finalizer change reconciles",
		func(oldObj, newObj *corev1.ConfigMap) { newObj.Finalizers = []string{"x/finalizer"} }, true),
	ginkgo.Entry("a label change reconciles",
		func(oldObj, newObj *corev1.ConfigMap) { newObj.Labels = map[string]string{"a": "b"} }, true),
	ginkgo.Entry("an annotation change reconciles",
		func(oldObj, newObj *corev1.ConfigMap) { newObj.Annotations = map[string]string{"a": "b"} }, true),
)

var _ = ginkgo.Describe("IgnoreStatusOnlyUpdates non-update events", func() {
	p := IgnoreStatusOnlyUpdates()

	ginkgo.It("reconciles on create", func() {
		Expect(p.Create(event.CreateEvent{Object: &corev1.ConfigMap{}})).To(BeTrue())
	})
	ginkgo.It("reconciles on delete", func() {
		Expect(p.Delete(event.DeleteEvent{Object: &corev1.ConfigMap{}})).To(BeTrue())
	})
	ginkgo.It("reconciles on generic", func() {
		Expect(p.Generic(event.GenericEvent{Object: &corev1.ConfigMap{}})).To(BeTrue())
	})
})
