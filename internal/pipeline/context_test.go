package pipeline

import (
	"context"
	"errors"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	"github.com/spechtlabs/prose/internal/observability"
)

var _ = ginkgo.Describe("Context", func() {
	ginkgo.Describe("Set", func() {
		ginkgo.It("writes one field to both the wide event and the current span", func() {
			rctx := testContext()
			span := &recSpan{}
			rctx.span = span
			rctx.groupPath = []string{"dependencies"}

			rctx.Set("deployment.image", "ghcr.io/x:v2")

			Expect(fieldVal(rctx, "dependencies.deployment.image")).To(Equal("ghcr.io/x:v2"))
			v, ok := span.attr("dependencies.deployment.image")
			Expect(ok).To(BeTrue(), "span attribute with the dotted key must be set")
			Expect(v.AsString()).To(Equal("ghcr.io/x:v2"))
		})

		ginkgo.It("records the field even with no span", func() {
			rctx := testContext()
			rctx.Set("k", "v")
			Expect(fieldVal(rctx, "k")).To(Equal("v"))
		})
	})

	ginkgo.Describe("accessors", func() {
		ginkgo.It("expose the object, context, and client", func() {
			ctx := context.Background()
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}
			rctx := newContext[*corev1.Pod](ctx, nil, nil, observability.NewSink(), "pod", "prose", pod)

			Expect(rctx.Object()).To(Equal(pod))
			Expect(rctx.Context()).To(Equal(ctx))
			Expect(rctx.Client()).To(BeNil())
		})
	})

	ginkgo.Describe("Event", func() {
		ginkgo.It("dispatches a formatted event when a recorder is configured", func() {
			rec := record.NewFakeRecorder(10)
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}
			rctx := newContext[*corev1.Pod](context.Background(), nil, nil, observability.NewSink(observability.Recorder(rec)), "pod", "prose", pod)

			rctx.Event(corev1.EventTypeNormal, "NodesUpdated", "tracking %d pods", 4)

			var got string
			Expect(rec.Events).To(Receive(&got))
			Expect(got).To(ContainSubstring("NodesUpdated"))
			Expect(got).To(ContainSubstring("tracking 4 pods"))
		})
	})

	ginkgo.Describe("cleanup stacks", func() {
		ginkgo.It("runs always-cleanups LIFO", func() {
			rctx := testContext()
			var order []string
			rctx.curStepPath = "s1"
			rctx.DeferCleanup(func() error { order = append(order, "c1"); return nil })
			rctx.curStepPath = "s2"
			rctx.DeferCleanup(func() error { order = append(order, "c2"); return nil })

			rctx.runCleanups()

			Expect(order).To(Equal([]string{"c2", "c1"}))
		})

		ginkgo.It("runs error-cleanups LIFO", func() {
			rctx := testContext()
			var order []string
			rctx.curStepPath = "s1"
			rctx.DeferErrorCleanup(func() error { order = append(order, "e1"); return nil })
			rctx.curStepPath = "s2"
			rctx.DeferErrorCleanup(func() error { order = append(order, "e2"); return nil })

			rctx.runErrorCleanups()

			Expect(order).To(Equal([]string{"e2", "e1"}))
		})

		ginkgo.It("folds a cleanup error into the wide event under its step", func() {
			rctx := testContext()
			rctx.curStepPath = "anchors.lease"
			rctx.DeferCleanup(func() error { return errors.New("release failed") })

			rctx.runCleanups()

			Expect(fieldVal(rctx, "anchors.lease.cleanup.error")).To(Equal("release failed"))
		})
	})
})
