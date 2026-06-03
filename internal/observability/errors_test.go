package observability

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	humane "github.com/sierrasoftworks/humane-errors-go"
)

var _ = Describe("FoldError", func() {
	It("splits a humane error into message, cause, and advice", func() {
		f := NewFields()
		err := humane.Wrap(errors.New("io timeout"), "apply deployment", "check RBAC")

		FoldError(f, "dependencies.deployment", err)

		msg, _ := f.Value("dependencies.deployment.error")
		cause, _ := f.Value("dependencies.deployment.cause")
		advice, _ := f.Value("dependencies.deployment.advice")
		Expect(msg).To(Equal("apply deployment"))
		Expect(cause).To(Equal("io timeout"))
		Expect(advice).To(Equal([]string{"check RBAC"}))
	})

	It("records only the message for a plain error", func() {
		f := NewFields()
		FoldError(f, "status", errors.New("boom"))

		msg, _ := f.Value("status.error")
		Expect(msg).To(Equal("boom"))
		Expect(f.Has("status.cause")).To(BeFalse())
		Expect(f.Has("status.advice")).To(BeFalse())
	})
})

var _ = Describe("FrameError", func() {
	It("frames a humane error with the step name while preserving the original", func() {
		orig := humane.Wrap(errors.New("io timeout"), "apply deployment", "check RBAC")

		framed := FrameError("deployment", orig)

		Expect(framed).To(MatchError(orig))
		var h humane.Error
		Expect(errors.As(framed, &h)).To(BeTrue())
		Expect(h.Error()).To(Equal("deployment"))
		Expect(h.Advice()).To(Equal([]string{"check RBAC"}))
	})

	It("frames a plain error and preserves it for errors.Is", func() {
		orig := errors.New("boom")
		framed := FrameError("status", orig)

		Expect(framed).To(MatchError(orig))
		Expect(framed.Error()).To(Equal("status"))
	})
})
