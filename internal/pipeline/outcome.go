package pipeline

import (
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

// outcomeKind enumerates the kinds of Outcome. The zero value is kindContinue,
// so a zero Outcome reads as a successful "proceed".
type outcomeKind uint8

const (
	kindContinue outcomeKind = iota
	kindRequeue
	kindRequeueAfter
	kindDone
	kindAborted
)

// Outcome is the result a step returns alongside its error. It keeps requeue
// semantics first-class rather than smuggled into errors: "come back in 30s" is
// a normal reconcile result, never an error.
//
// Outcome is a comparable value type. Continue, Requeue, and Done are
// package-level values; RequeueAfter is a constructor carrying a duration.
type Outcome struct {
	kind  outcomeKind
	after time.Duration
}

var (
	// Continue means the step succeeded; proceed to the next step.
	Continue = Outcome{kind: kindContinue}
	// Requeue means come back immediately, paired with the controller's backoff.
	Requeue = Outcome{kind: kindRequeue}
	// Done means the reconcile is complete; stop the pipeline successfully.
	Done = Outcome{kind: kindDone}

	// aborted is an internal outcome the runner synthesizes when a step fails only
	// because the reconcile context was canceled (typically manager shutdown).
	// Steps never return it. It stops the pipeline without surfacing an error, so a
	// shutdown reads as an aborted reconcile rather than a failure.
	aborted = Outcome{kind: kindAborted}
)

// RequeueAfter means come back after duration d. It is a result, not an error.
func RequeueAfter(d time.Duration) Outcome {
	return Outcome{kind: kindRequeueAfter, after: d}
}

// label returns the canonical, closed-set string for an outcome, used for the
// wide event's <step>.outcome field and the metric's outcome label. It is never
// derived from user input, keeping the metric's cardinality bounded.
func (o Outcome) label() string {
	switch o.kind {
	case kindRequeue:
		return "requeue"
	case kindRequeueAfter:
		return "requeue_after"
	case kindDone:
		return "done"
	case kindAborted:
		return "aborted"
	default:
		return "continue"
	}
}

// terminalSuccess reports whether an outcome represents a successful end state
// (as opposed to a request to be requeued). Used by the runner to decide whether
// a deletion-mode Finalize group succeeded before removing the finalizer.
func terminalSuccess(o Outcome) bool {
	return o.kind == kindContinue || o.kind == kindDone
}

// translate maps a step Outcome and error into the (ctrl.Result, error) pair
// controller-runtime expects. A returned error always propagates as the root
// cause; requeue semantics ride alongside it.
//
// This is the single place that depends on controller-runtime's requeue
// representation, so adapting to API changes (e.g. the deprecated Result.Requeue
// bool) is a one-function change.
func translate(o Outcome, err error) (ctrl.Result, error) {
	if err != nil {
		if o.kind == kindRequeueAfter {
			return ctrl.Result{RequeueAfter: o.after}, err
		}
		return ctrl.Result{}, err
	}

	switch o.kind {
	case kindRequeue:
		return ctrl.Result{Requeue: true}, nil
	case kindRequeueAfter:
		return ctrl.Result{RequeueAfter: o.after}, nil
	default:
		return ctrl.Result{}, nil
	}
}
