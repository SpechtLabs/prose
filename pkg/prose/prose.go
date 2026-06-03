// Package prose is a thin DSL over controller-runtime for building Kubernetes
// operators as a linear, observable sequence of named steps.
//
// A reconcile is modelled as one observable transaction: it has a beginning
// (fetch the object), a body (an ordered tree of steps and groups), and an end
// (emit exactly one wide event describing everything that happened). Steps
// describe what happened by setting fields and returning outcomes; the framework
// decides how that becomes telemetry, recording spans, durations, a single
// structured log line, per-step metrics, and Kubernetes events around the steps
// rather than inside them.
//
// This package is a thin facade. The implementation lives in two internal
// packages — internal/pipeline (the generic DSL and runner) and
// internal/observability (the non-generic telemetry sink) — which this package
// re-exports.
//
//	prose.For[*v1alpha1.Foo](mgr).
//	    Owns(&appsv1.Deployment{}).
//	    WithObservability(prose.Otel(tracer), prose.WideEvents(logger)).
//	    When("paused", isPaused).Skip().
//	    Describe("dependencies", func(g *prose.Group[*v1alpha1.Foo]) {
//	        g.Step("deployment", upsertDeployment)
//	    }).
//	    Step("status", syncStatus).
//	    Complete()
package prose

import (
	"time"

	"github.com/go-logr/logr"
	"github.com/onsi/gomega/types"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/spechtlabs/prose/internal/observability"
	"github.com/spechtlabs/prose/internal/pipeline"
)

// Core DSL types, re-exported from internal/pipeline. These are aliases, so values
// produced here are identical to the underlying implementation types.
type (
	// Builder is the entry point to a prose pipeline; see For.
	Builder[T client.Object] = pipeline.Builder[T]
	// Context is the typed reconcile context handed to every step.
	Context[T client.Object] = pipeline.Context[T]
	// Group is the façade handed to a Describe/Context/When closure.
	Group[T client.Object] = pipeline.Group[T]
	// Gate is returned by Builder.When; call Skip() for the pause/deletion gate.
	Gate[T client.Object] = pipeline.Gate[T]
	// StepFunc is one named, observable unit of reconcile work.
	StepFunc[T client.Object] = pipeline.StepFunc[T]
	// Predicate is a pure boolean question over the reconciled object.
	Predicate[T client.Object] = pipeline.Predicate[T]
	// Outcome is the result a step returns alongside its error.
	Outcome = pipeline.Outcome
	// ObservabilityOption configures the telemetry sink; see Otel/WideEvents/Recorder.
	ObservabilityOption = observability.Option
)

// Outcomes. Continue/Requeue/Done are values; RequeueAfter carries a duration.
var (
	// Continue means the step succeeded; proceed to the next step.
	Continue = pipeline.Continue
	// Requeue means come back immediately, paired with the controller's backoff.
	Requeue = pipeline.Requeue
	// Done means the reconcile is complete; stop the pipeline successfully.
	Done = pipeline.Done
)

// RequeueAfter means come back after duration d. It is a result, not an error.
func RequeueAfter(d time.Duration) Outcome { return pipeline.RequeueAfter(d) }

// For begins a prose pipeline for objects of type T managed by mgr.
func For[T client.Object](mgr manager.Manager) *Builder[T] { return pipeline.For[T](mgr) }

// Otel sends per-step and per-group spans to the given tracer.
func Otel(tracer trace.Tracer) ObservabilityOption { return observability.Otel(tracer) }

// WideEvents emits exactly one canonical structured log line per reconcile.
func WideEvents(logger logr.Logger) ObservabilityOption { return observability.WideEvents(logger) }

// Recorder enables kubectl-visible Kubernetes events via rctx.Event.
func Recorder(rec record.EventRecorder) ObservabilityOption { return observability.Recorder(rec) }

// Match adapts a Gomega matcher into a non-panicking gate Predicate. A Gomega
// matcher's Match method returns (success, error) and never panics on its own —
// panicking is a property of Expect/Ω plus the default fail handler, which this
// never invokes. A matcher that errors (for example HaveField against a missing
// field) is treated as "does not hold" rather than propagated, so a malformed
// gate fails closed instead of crashing a reconcile.
//
//	When("scaled up",
//	    prose.Match[*v1alpha1.Foo](gomega.HaveField("Spec.Replicas", gomega.BeNumerically(">", 0))),
//	    func(g *prose.Group[*v1alpha1.Foo]) { ... })
func Match[T client.Object](matcher types.GomegaMatcher) Predicate[T] {
	return func(obj T) bool {
		success, err := matcher.Match(obj)
		return err == nil && success
	}
}

// IgnoreStatusOnlyUpdates is a deletion-safe predicate for the primary watch,
// passed via Builder.WithPredicates. It skips an update event when only the
// object's status (or resourceVersion) changed — cutting the reconcile churn from
// a controller reacting to its own status writes — while still reconciling on spec
// changes, creation, deletion, and finalizer/label/annotation changes. Unlike a
// bare predicate.GenerationChangedPredicate, it does not drop the update that sets
// the deletion timestamp, so Finalize keeps working.
//
//	prose.For[*v1alpha1.Foo](mgr).
//	    WithPredicates(prose.IgnoreStatusOnlyUpdates()).
//	    Step("status", syncStatus).
//	    Complete()
func IgnoreStatusOnlyUpdates() predicate.Predicate { return pipeline.IgnoreStatusOnlyUpdates() }
