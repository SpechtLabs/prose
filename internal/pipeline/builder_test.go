package pipeline

import (
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

func okStep(rctx *Context[*corev1.Pod]) (Outcome, error) { return Continue, nil }

func newTestBuilder() *Builder[*corev1.Pod] {
	return &Builder[*corev1.Pod]{root: &node[*corev1.Pod]{isGroup: true}}
}

var _ = ginkgo.Describe("Builder", func() {
	ginkgo.It("builds a tree with a skip gate, a group, and a step", func() {
		b := newTestBuilder()
		paused := func(*corev1.Pod) bool { return true }

		b.When("paused", paused).Skip().
			Describe("deps", func(g *Group[*corev1.Pod]) {
				g.Step("a", okStep)
			}).
			Step("status", okStep)

		ch := b.root.children
		Expect(ch).To(HaveLen(3))
		Expect(ch[0].name).To(Equal("paused"))
		Expect(ch[0].isGroup).To(BeTrue())
		Expect(ch[0].skip).To(BeTrue())
		Expect(ch[1].name).To(Equal("deps"))
		Expect(ch[1].children).To(HaveLen(1))
		Expect(ch[1].children[0].name).To(Equal("a"))
		Expect(ch[2].name).To(Equal("status"))
		Expect(ch[2].isGroup).To(BeFalse())
	})

	ginkgo.It("continues the chain after a When with a closure", func() {
		b := newTestBuilder()
		always := func(*corev1.Pod) bool { return true }

		b.When("scaled", always, func(g *Group[*corev1.Pod]) {
			g.Step("inner", okStep)
		}).Step("after", okStep)

		ch := b.root.children
		Expect(ch).To(HaveLen(2))
		Expect(ch[0].name).To(Equal("scaled"))
		Expect(ch[0].pred).NotTo(BeNil())
		Expect(ch[0].children).To(HaveLen(1))
		Expect(ch[1].name).To(Equal("after"))
	})

	ginkgo.It("treats Context as an exact alias of Describe", func() {
		b := newTestBuilder()
		b.Context("now that both ends exist", func(g *Group[*corev1.Pod]) {
			g.Step("link", okStep)
		})
		ch := b.root.children
		Expect(ch).To(HaveLen(1))
		Expect(ch[0].isGroup).To(BeTrue())
		Expect(ch[0].name).To(Equal("now that both ends exist"))
	})

	ginkgo.It("keeps the Finalize group out of the normal pipeline", func() {
		b := newTestBuilder()
		b.Finalize("collapse", func(g *Group[*corev1.Pod]) {
			g.Step("drain", okStep)
			g.Step("release", okStep)
		})
		Expect(b.finalize).NotTo(BeNil())
		Expect(b.finalize.name).To(Equal("collapse"))
		Expect(b.finalize.children).To(HaveLen(2))
		Expect(b.root.children).To(BeEmpty())
	})
})
