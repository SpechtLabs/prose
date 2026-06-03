package prose

import (
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = ginkgo.Describe("Match", func() {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "widget"}}

	ginkgo.It("is true when the matcher passes", func() {
		Expect(Match[*corev1.Pod](Not(BeNil()))(pod)).To(BeTrue())
	})

	ginkgo.It("is false when the matcher fails", func() {
		Expect(Match[*corev1.Pod](BeNil())(pod)).To(BeFalse())
	})

	ginkgo.It("is false (not a panic) when the matcher errors", func() {
		Expect(Match[*corev1.Pod](BeNumerically(">", 0))(pod)).To(BeFalse())
	})

	ginkgo.It("reads like the README with HaveField", func() {
		Expect(Match[*corev1.Pod](HaveField("ObjectMeta.Name", "widget"))(pod)).To(BeTrue())
		Expect(Match[*corev1.Pod](HaveField("ObjectMeta.Name", "other"))(pod)).To(BeFalse())
	})
})
