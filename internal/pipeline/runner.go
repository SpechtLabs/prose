package pipeline

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/spechtlabs/prose/internal/observability"
)

// runner is the reconcile.Reconciler prose registers with controller-runtime. It
// owns the framework-owned prologue (Get + IgnoreNotFound), the unwind ordering,
// the finalizer add/remove, and the single, structurally-unmissable wide-event
// emit. Its fields are read-only after Complete builds it, so concurrent
// reconciles (MaxConcurrentReconciles > 1) share nothing mutable.
type runner[T client.Object] struct {
	client     client.Client
	scheme     *runtime.Scheme
	sink       *observability.Sink
	newObject  func() T
	root       *node[T]
	finalize   *node[T]
	controller string
	finalizer  string
	fieldOwner string
	baseLogger logr.Logger

	// conflicts tracks consecutive optimistic-concurrency conflicts per object so a
	// transient conflict requeues quietly and only a persistent streak logs loudly.
	conflicts *conflictTracker
	// maxConflicts is the streak length tolerated quietly before going loud, set by
	// Builder.WithConflictTolerance (default maxQuietConflicts).
	maxConflicts int
}

// Reconcile fetches the object, runs the pipeline as one observable transaction,
// and emits exactly one wide event. The emit is deferred so no early return,
// requeue, or error path can skip it.
func (r *runner[T]) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	obj := r.newObject()
	if err := r.client.Get(ctx, req.NamespacedName, obj); err != nil {
		// The fetch is the first client call of every reconcile, so on shutdown it
		// is the most likely to be canceled. Swallow that quietly: there is no
		// transaction to record and nothing worth an error log.
		if isCancellation(ctx, err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger := r.baseLogger.WithValues(
		"namespace", req.Namespace,
		"name", req.Name,
		"generation", obj.GetGeneration(),
	)

	rctx := newContext[T](ctx, r.client, r.scheme, r.sink, r.controller, r.fieldOwner, obj)

	// Open one root span for the whole reconcile and seed the context with it, so
	// every group and step nests beneath it: one reconcile, one trace, mirroring the
	// single wide event. Without this, each top-level group or step would start its
	// own parentless span and a single reconcile would shatter into several traces.
	//
	// SpanKindServer marks the reconcile as the controller's entry point — handling
	// a request taken off the workqueue. That is what the span-metrics generator
	// records into per-service RED (rate/errors/duration), so reconciles show up in
	// Application Observability's service view, not only in raw trace search. An
	// INTERNAL root span is excluded from those metrics and stays invisible there.
	spanCtx, rootSpan := r.sink.Tracer().Start(ctx, "reconcile",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("controller", r.controller),
			attribute.String("namespace", req.Namespace),
			attribute.String("name", req.Name),
		),
	)
	rctx.ctx = spanCtx
	rctx.span = rootSpan

	start := time.Now()
	outcome := Continue
	var rerr error

	defer func() {
		rootSpan.SetAttributes(attribute.String("prose.outcome", outcome.label()))
		if rerr != nil {
			rootSpan.RecordError(rerr)
			rootSpan.SetStatus(codes.Error, rerr.Error())
		}
		rootSpan.End()
		result, _ := translate(outcome, rerr)
		r.emit(logger, rctx, outcome, rerr, result, time.Since(start))
	}()

	outcome, rerr = r.execute(rctx, obj)
	outcome, rerr = r.resolveConflict(rctx, req.NamespacedName.String(), outcome, rerr)
	return translate(outcome, rerr)
}

// execute runs the pipeline in the README's exact unwind order. The original step
// error is the root cause returned to controller-runtime; cleanup failures are
// additive context in the wide event only.
func (r *runner[T]) execute(rctx *Context[T], obj T) (Outcome, error) {
	deleting := !obj.GetDeletionTimestamp().IsZero()

	// Finalizer add on the normal path, only when there is teardown to do.
	if r.finalize != nil && !deleting {
		if controllerutil.AddFinalizer(obj, r.finalizer) {
			if err := r.client.Update(rctx.ctx, obj); err != nil {
				if isCancellation(rctx.ctx, err) {
					return aborted, nil
				}
				return Requeue, observability.FrameError("finalizer", humane.Wrap(err, "add finalizer",
					"verify the controller has RBAC to update this resource"))
			}
		}
	}

	var tree []*node[T]
	switch {
	case deleting && r.finalize != nil:
		tree = []*node[T]{r.finalize} // deletion mode: run only the Finalize group
	case deleting:
		return Done, nil // being deleted, nothing to finalize
	default:
		tree = r.root.children
	}

	outcome, stepErr := rctx.run(tree)

	// Unwind: error cleanups first (only on error), then always-cleanups, both LIFO.
	if stepErr != nil {
		rctx.runErrorCleanups()
	}
	rctx.runCleanups()

	// Finalizer removal once the Finalize group has succeeded.
	if deleting && r.finalize != nil && stepErr == nil && terminalSuccess(outcome) {
		if controllerutil.RemoveFinalizer(obj, r.finalizer) {
			if err := r.client.Update(rctx.ctx, obj); err != nil {
				if isCancellation(rctx.ctx, err) {
					return aborted, nil
				}
				return Requeue, observability.FrameError("finalizer", humane.Wrap(err, "remove finalizer",
					"verify the controller has RBAC to update this resource"))
			}
		}
	}

	return outcome, stepErr
}

// emit writes the single wide event: one structured log line per reconcile with
// the result, requeue backoff, total duration, and every accumulated field
// flattened into dotted keys. controller/namespace/name/generation ride on the
// derived logger, so they appear once.
func (r *runner[T]) emit(logger logr.Logger, rctx *Context[T], outcome Outcome, err error, result ctrl.Result, dur time.Duration) {
	resultLabel := outcome.label()
	if err != nil {
		resultLabel = "error"
	}
	kv := []any{
		"result", resultLabel,
		"requeue_after", result.RequeueAfter.String(),
		"duration", dur.String(),
	}
	kv = append(kv, rctx.fields.Flatten()...)
	logger.Info("reconcile", kv...)
}
