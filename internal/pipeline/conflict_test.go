package pipeline

import (
	"context"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	humane "github.com/sierrasoftworks/humane-errors-go"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spechtlabs/prose/internal/observability"
)

func newConflictTestRunner() (*runner[*corev1.ConfigMap], *Context[*corev1.ConfigMap]) {
	r := &runner[*corev1.ConfigMap]{conflicts: newConflictTracker(), maxConflicts: maxQuietConflicts}
	rctx := newContext[*corev1.ConfigMap](
		context.Background(), nil, nil, observability.NewSink(), "test", "prose-test", &corev1.ConfigMap{},
	)
	return r, rctx
}

func conflictErr() error {
	raw := apierrors.NewConflict(schema.GroupResource{Group: "g", Resource: "configmaps"}, "x", nil)
	// Wrap it the way a step and the runStep frame would, to prove IsConflict still
	// sees through the humane chain.
	return humane.Wrap(humane.Wrap(raw, "persist status", "enable the status subresource"), "ignite")
}

var _ = ginkgo.Describe("resolveConflict", func() {
	ginkgo.It("requeues quietly and counts while the streak is within tolerance", func() {
		r, rctx := newConflictTestRunner()

		for i := 1; i <= maxQuietConflicts; i++ {
			outcome, err := r.resolveConflict(rctx, "milkyway/voyager", Requeue, conflictErr())
			Expect(err).To(BeNil())
			Expect(outcome).To(Equal(Requeue))
			n, ok := rctx.fields.Value("conflict.count")
			Expect(ok).To(BeTrue())
			Expect(n).To(Equal(i))
		}
		_, exhausted := rctx.fields.Value("conflict.exhausted")
		Expect(exhausted).To(BeFalse())
	})

	ginkgo.It("propagates loudly once the streak exceeds tolerance", func() {
		r, rctx := newConflictTestRunner()

		var lastErr error
		for i := 1; i <= maxQuietConflicts+1; i++ {
			_, lastErr = r.resolveConflict(rctx, "milkyway/voyager", Requeue, conflictErr())
		}
		Expect(lastErr).NotTo(BeNil())
		Expect(apierrors.IsConflict(lastErr)).To(BeTrue())
		n, _ := rctx.fields.Value("conflict.count")
		Expect(n).To(Equal(maxQuietConflicts + 1))
		exhausted, ok := rctx.fields.Value("conflict.exhausted")
		Expect(ok).To(BeTrue())
		Expect(exhausted).To(Equal(true))
	})

	ginkgo.It("resets the streak after a non-conflict outcome", func() {
		r, rctx := newConflictTestRunner()

		// Three conflicts, then a clean reconcile, then conflicts again: the count
		// must restart from 1, not carry over.
		for i := 0; i < maxQuietConflicts; i++ {
			r.resolveConflict(rctx, "milkyway/voyager", Requeue, conflictErr())
		}
		r.resolveConflict(rctx, "milkyway/voyager", Continue, nil)

		outcome, err := r.resolveConflict(rctx, "milkyway/voyager", Requeue, conflictErr())
		Expect(err).To(BeNil())
		Expect(outcome).To(Equal(Requeue))
		n, _ := rctx.fields.Value("conflict.count")
		Expect(n).To(Equal(1))
	})

	ginkgo.It("does not swallow a NotFound (it may be a referenced object, not the reconciled one)", func() {
		r, rctx := newConflictTestRunner()

		// A NotFound from a Get on some other resource inside a step (here a missing
		// referenced relay) must propagate as the step returned it, not be mistaken
		// for the reconciled object vanishing and quietly marked done.
		gone := humane.Wrap(apierrors.NewNotFound(schema.GroupResource{Resource: "subspacerelays"}, "gateway-prime"), "get relay")
		outcome, err := r.resolveConflict(rctx, "milkyway/voyager", Requeue, gone)
		Expect(err).To(Equal(gone))
		Expect(outcome).To(Equal(Requeue))
		_, ok := rctx.fields.Value("conflict.gone")
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("passes a non-conflict error through untouched", func() {
		r, rctx := newConflictTestRunner()

		boom := humane.New("boom")
		outcome, err := r.resolveConflict(rctx, "milkyway/voyager", Requeue, boom)
		Expect(err).To(Equal(boom))
		Expect(outcome).To(Equal(Requeue))
		_, ok := rctx.fields.Value("conflict.count")
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("honors a custom conflict tolerance", func() {
		_, rctx := newConflictTestRunner()
		r := &runner[*corev1.ConfigMap]{conflicts: newConflictTracker(), maxConflicts: 1}

		out1, err1 := r.resolveConflict(rctx, "milkyway/voyager", Requeue, conflictErr())
		Expect(err1).To(BeNil())
		Expect(out1).To(Equal(Requeue))

		out2, err2 := r.resolveConflict(rctx, "milkyway/voyager", Requeue, conflictErr())
		Expect(err2).NotTo(BeNil())
		Expect(apierrors.IsConflict(err2)).To(BeTrue())
		Expect(out2).To(Equal(Requeue))
	})

	ginkgo.It("treats tolerance 0 as loud on the first conflict", func() {
		_, rctx := newConflictTestRunner()
		r := &runner[*corev1.ConfigMap]{conflicts: newConflictTracker(), maxConflicts: 0}

		_, err := r.resolveConflict(rctx, "milkyway/voyager", Requeue, conflictErr())
		Expect(err).NotTo(BeNil())
		Expect(apierrors.IsConflict(err)).To(BeTrue())
	})
})

var _ = ginkgo.Describe("WithConflictTolerance", func() {
	ginkgo.It("overrides the builder's default tolerance", func() {
		b := &Builder[*corev1.ConfigMap]{conflictTolerance: maxQuietConflicts}
		Expect(b.WithConflictTolerance(7)).To(BeIdenticalTo(b))
		Expect(b.conflictTolerance).To(Equal(7))
	})
})
