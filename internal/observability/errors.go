package observability

import (
	"errors"

	humane "github.com/sierrasoftworks/humane-errors-go"
)

// FoldError writes the structured form of a step error into the wide event under
// the step's path: <path>.error (human-readable message), <path>.cause (the
// immediate cause), and <path>.advice (the recovery advice). It uses humane's
// separated message/cause/advice rather than a flattened err.Error(), so the
// emitted record keeps the advice and the cause queryable on their own.
//
// FoldError records the original error, not the step-framed one, so .error
// carries the meaningful message ("apply deployment") rather than the bare step
// frame — the step name is already the field's key prefix.
func FoldError(f *Fields, basePath string, err error) {
	if err == nil {
		return
	}

	var h humane.Error
	if errors.As(err, &h) {
		f.Set(basePath+".error", h.Error())
		if c := h.Cause(); c != nil {
			f.Set(basePath+".cause", c.Error())
		}
		if a := h.Advice(); len(a) > 0 {
			f.Set(basePath+".advice", a)
		}
		return
	}

	f.Set(basePath+".error", err.Error())
	if u := errors.Unwrap(err); u != nil {
		f.Set(basePath+".cause", u.Error())
	}
}

// FrameError frames a step error with the step name as its contextual message, so
// the error propagated to controller-runtime reads as the step that produced it
// rather than a bare cause. The original error becomes the cause (preserving
// errors.Is), and any humane advice is lifted onto the frame so it survives at the
// controller-runtime boundary.
func FrameError(stepName string, err error) error {
	var h humane.Error
	if errors.As(err, &h) {
		return humane.Wrap(err, stepName, h.Advice()...)
	}
	return humane.Wrap(err, stepName)
}
