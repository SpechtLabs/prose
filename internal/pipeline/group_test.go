package pipeline

import (
	"context"
	"errors"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/spechtlabs/prose/internal/observability"
)

// testContext builds a Context with a no-op sink and a Pod object, for exercising
// the executor without a manager or client.
func testContext() *Context[*corev1.Pod] {
	pod := &corev1.Pod{Name: "widget", Namespace: "team-a"}
	return newContext[*corev1.Pod](context.Background(), nil, nil, observability.NewSink(), "pod", "prose", pod)
}

func recStep(log *[]string, name string, out Outcome, err error) *node[*corev1.Pod] {
	return &node[*corev1.Pod]{
		name: name,
		fn: func(rctx *Context[*corev1.Pod]) (Outcome, error) {
			*log = append(*log, name)
			return out, err
		},
	}
}

func grp(name string, pred Predicate[*corev1.Pod], children ...*node[*corev1.Pod]) *node[*corev1.Pod] {
	return &node[*corev1.Pod]{name: name, isGroup: true, pred: pred, children: children}
}

func fieldVal(rctx *Context[*corev1.Pod], key string) any {
	v, _ := rctx.fields.Value(key)
	return v
}

var _ = ginkgo.Describe("the executor", func() {
	var (
		rctx *Context[*corev1.Pod]
		log  []string
	)

	ginkgo.BeforeEach(func() {
		rctx = testContext()
		log = nil
	})

	ginkgo.It("runs steps depth-first in declaration order and records reserved fields", func() {
		nodes := []*node[*corev1.Pod]{
			grp("deps", nil,
				recStep(&log, "a", Continue, nil),
				recStep(&log, "b", Continue, nil),
			),
			recStep(&log, "c", Continue, nil),
		}

		out, err := rctx.run(nodes)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(Continue))
		Expect(log).To(Equal([]string{"a", "b", "c"}))
		Expect(fieldVal(rctx, "deps.a.outcome")).To(Equal("continue"))
		Expect(rctx.fields.Has("deps.b.duration")).To(BeTrue())
		Expect(fieldVal(rctx, "c.outcome")).To(Equal("continue"), "top-level step flattens without a group prefix")
	})

	ginkgo.It("measures step durations with the sink's clock", func() {
		// Every reading moves the clock on by a second, so each step, which
		// reads it once before and once after, lasts exactly one second.
		clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		tick := func() time.Time {
			clock = clock.Add(time.Second)
			return clock
		}
		pod := &corev1.Pod{Name: "widget", Namespace: "team-a"}
		rctx = newContext[*corev1.Pod](context.Background(), nil, nil,
			observability.NewSink(observability.Clock(tick)), "pod", "prose", pod)

		_, err := rctx.run([]*node[*corev1.Pod]{
			grp("deps", nil, recStep(&log, "a", Continue, nil)),
			recStep(&log, "b", Continue, nil),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(fieldVal(rctx, "deps.a.duration")).To(Equal("1s"))
		Expect(fieldVal(rctx, "b.duration")).To(Equal("1s"))
	})

	ginkgo.It("skips the body of a When group whose predicate does not hold", func() {
		never := func(*corev1.Pod) bool { return false }
		nodes := []*node[*corev1.Pod]{
			grp("gated", never, recStep(&log, "inner", Continue, nil)),
			recStep(&log, "after", Continue, nil),
		}

		out, err := rctx.run(nodes)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(Continue))
		Expect(log).To(Equal([]string{"after"}))
	})

	ginkgo.It("stops the whole reconcile when a skip gate's predicate holds", func() {
		always := func(*corev1.Pod) bool { return true }
		nodes := []*node[*corev1.Pod]{
			{name: "paused", isGroup: true, pred: always, skip: true},
			recStep(&log, "after", Continue, nil),
		}

		out, err := rctx.run(nodes)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(Done))
		Expect(log).To(BeEmpty())
	})

	ginkgo.It("continues past an inactive skip gate", func() {
		never := func(*corev1.Pod) bool { return false }
		nodes := []*node[*corev1.Pod]{
			{name: "paused", isGroup: true, pred: never, skip: true},
			recStep(&log, "after", Continue, nil),
		}

		out, err := rctx.run(nodes)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(Continue))
		Expect(log).To(Equal([]string{"after"}))
	})

	ginkgo.It("stops the pipeline on a step error and folds the error into the fields", func() {
		boom := errors.New("io timeout")
		nodes := []*node[*corev1.Pod]{
			recStep(&log, "first", Continue, nil),
			recStep(&log, "second", Requeue, boom),
			recStep(&log, "third", Continue, nil),
		}

		out, err := rctx.run(nodes)

		Expect(out).To(Equal(Requeue))
		Expect(err).To(MatchError(boom))
		Expect(log).To(Equal([]string{"first", "second"}))
		Expect(fieldVal(rctx, "second.error")).To(Equal("io timeout"))
	})

	ginkgo.It("aborts (not errors) when a step fails with the reconcile context canceled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // simulate the manager shutting down mid-reconcile
		rctx.ctx = ctx

		nodes := []*node[*corev1.Pod]{
			recStep(&log, "open-tunnel", Requeue, errors.New("apply tunnel manifest")),
			recStep(&log, "after", Continue, nil),
		}

		out, err := rctx.run(nodes)

		Expect(err).NotTo(HaveOccurred(), "a canceled context is not a business failure")
		Expect(out.label()).To(Equal("aborted"))
		Expect(log).To(Equal([]string{"open-tunnel"}), "the pipeline stops at the aborted step")
		Expect(fieldVal(rctx, "open-tunnel.outcome")).To(Equal("aborted"))
		Expect(rctx.fields.Has("open-tunnel.error")).To(BeFalse(), "no error field on an aborted step")
	})
})
