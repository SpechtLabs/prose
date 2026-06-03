package pipeline

import (
	"context"
	"errors"
	"slices"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/spechtlabs/prose/internal/observability"
)

// Context is the typed reconcile context handed to every step. It already holds
// the fetched object (no Get, no cast), the raw client and context for the escape
// hatch, and the field accumulator that becomes the wide event. It also carries
// the per-reconcile executor state (current group path and span), which the runner
// mutates as it descends the tree.
type Context[T client.Object] struct {
	ctx        context.Context
	client     client.Client
	scheme     *runtime.Scheme
	sink       *observability.Sink
	controller string
	fieldOwner string
	obj        T

	fields    *observability.Fields
	groupPath []string
	span      trace.Span

	// curStepPath is the dotted path of the step currently executing, captured by
	// DeferCleanup/DeferErrorCleanup so a cleanup's error folds under its step.
	curStepPath string

	cleanups      []deferredCleanup
	errorCleanups []deferredCleanup
}

type deferredCleanup struct {
	path string
	fn   func() error
}

func newContext[T client.Object](ctx context.Context, c client.Client, scheme *runtime.Scheme, sink *observability.Sink, controller, fieldOwner string, obj T) *Context[T] {
	return &Context[T]{
		ctx:        ctx,
		client:     c,
		scheme:     scheme,
		sink:       sink,
		controller: controller,
		fieldOwner: fieldOwner,
		obj:        obj,
		fields:     observability.NewFields(),
	}
}

// Object returns the typed object being reconciled, already fetched — no Get, no
// cast.
func (rctx *Context[T]) Object() T { return rctx.obj }

// Client exposes the raw controller-runtime client for work the DSL does not
// model (a label-selector List, a status subresource update). It is the
// documented escape hatch, usable inline without leaving the pipeline.
func (rctx *Context[T]) Client() client.Client { return rctx.client }

// Context returns the reconcile context, for passing to client calls.
func (rctx *Context[T]) Context() context.Context { return rctx.ctx }

// Set contributes a field to the reconcile. The same call lands in two places:
// the wide log event (keyed by the current group path plus this key) and the
// current OpenTelemetry span (as an attribute under the same dotted key). You
// write the field once; it shows up in both logs and traces.
func (rctx *Context[T]) Set(key string, value any) {
	dottedKey := observability.Dotted(append(slices.Clone(rctx.groupPath), key)...)
	rctx.fields.Set(dottedKey, value)
	if rctx.span != nil {
		rctx.span.SetAttributes(observability.ToAttr(dottedKey, value))
	}
}

// Apply converges an object via server-side apply, stamping the controller owner
// reference from the reconciled object first (so the result is garbage-collected
// with its owner and re-triggers this reconcile when it changes). ForceOwnership
// makes prose own the fields it sets across updates.
func (rctx *Context[T]) Apply(obj client.Object) error {
	if err := controllerutil.SetControllerReference(rctx.obj, obj, rctx.scheme); err != nil {
		return err
	}
	return rctx.client.Patch(rctx.ctx, obj, client.Apply,
		client.FieldOwner(rctx.fieldOwner), client.ForceOwnership)
}

// Event records a Kubernetes event against the reconciled object, for the human
// running kubectl describe. It is opt-in per step and no-ops without a Recorder.
func (rctx *Context[T]) Event(eventtype, reason, msgFmt string, args ...any) {
	rctx.sink.Event(rctx.obj, eventtype, reason, msgFmt, args...)
}

// DeferCleanup registers a cleanup that always runs at the end of the reconcile,
// LIFO, success or failure. Use it only for resources whose lifetime is exactly
// this reconcile and whose release has no cluster-observable effect.
func (rctx *Context[T]) DeferCleanup(fn func() error) {
	rctx.cleanups = append(rctx.cleanups, deferredCleanup{path: rctx.curStepPath, fn: fn})
}

// DeferErrorCleanup registers compensation that runs only on the unwind path, LIFO,
// when a subsequent step errors. This is the recommended primitive: it undoes work
// a later step then failed to complete.
func (rctx *Context[T]) DeferErrorCleanup(fn func() error) {
	rctx.errorCleanups = append(rctx.errorCleanups, deferredCleanup{path: rctx.curStepPath, fn: fn})
}

// runCleanups runs the always-cleanup stack LIFO, folding any error into the wide
// event under the step that registered it. A cleanup error can never change the
// returned outcome or error — it is additive context only.
func (rctx *Context[T]) runCleanups() {
	runCleanupStack(rctx.fields, rctx.cleanups)
}

// runErrorCleanups runs the error-cleanup stack LIFO, folding any error into the
// wide event. Called only when a step errored.
func (rctx *Context[T]) runErrorCleanups() {
	runCleanupStack(rctx.fields, rctx.errorCleanups)
}

func runCleanupStack(f *observability.Fields, stack []deferredCleanup) {
	for i := len(stack) - 1; i >= 0; i-- {
		if err := stack[i].fn(); err != nil {
			f.Set(observability.Dotted(stack[i].path, "cleanup", "error"), err.Error())
		}
	}
}

// run executes a list of sibling nodes depth-first in declaration order, stopping
// at the first node that errors or returns a non-Continue outcome.
func (rctx *Context[T]) run(nodes []*node[T]) (Outcome, error) {
	for _, n := range nodes {
		out, err := rctx.runNode(n)
		if err != nil {
			return out, err
		}
		if out.kind != kindContinue {
			return out, nil // Done/Requeue/RequeueAfter stop the pipeline
		}
	}
	return Continue, nil
}

func (rctx *Context[T]) runNode(n *node[T]) (Outcome, error) {
	if n.isGroup {
		return rctx.runGroup(n)
	}
	return rctx.runStep(n)
}

func (rctx *Context[T]) runGroup(n *node[T]) (Outcome, error) {
	// Gating. A When predicate that does not hold skips the whole subtree.
	if n.pred != nil && !n.pred(rctx.obj) {
		return Continue, nil
	}
	// A fired skip gate (When(...).Skip()) stops the whole reconcile successfully.
	if n.skip {
		return Done, nil
	}

	ctx, span := rctx.sink.Tracer().Start(rctx.ctx, n.name)
	prevCtx, prevSpan, prevPath := rctx.ctx, rctx.span, rctx.groupPath
	rctx.ctx, rctx.span = ctx, span
	rctx.groupPath = append(slices.Clone(prevPath), n.name)

	out, err := rctx.run(n.children)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	rctx.ctx, rctx.span, rctx.groupPath = prevCtx, prevSpan, prevPath
	return out, err
}

func (rctx *Context[T]) runStep(n *node[T]) (Outcome, error) {
	stepPath := observability.Dotted(append(slices.Clone(rctx.groupPath), n.name)...)

	ctx, span := rctx.sink.Tracer().Start(rctx.ctx, n.name)
	prevCtx, prevSpan, prevStep := rctx.ctx, rctx.span, rctx.curStepPath
	rctx.ctx, rctx.span, rctx.curStepPath = ctx, span, stepPath

	start := time.Now()
	out, err := n.fn(rctx)
	dur := time.Since(start)

	rctx.fields.Set(stepPath+".duration", dur.String())

	// A step that failed only because the reconcile context was canceled (the
	// manager is shutting down) is not a business failure. Report it as aborted —
	// no folded error, no error span status — and stop the pipeline without
	// surfacing an error, so a shutdown does not spam ERROR logs and stack traces
	// nor flag the wide event as a failure.
	if err != nil && isCancellation(rctx.ctx, err) {
		rctx.fields.Set(stepPath+".outcome", aborted.label())
		rctx.sink.Observe(rctx.controller, n.name, aborted.label(), dur)
		span.End()
		rctx.ctx, rctx.span, rctx.curStepPath = prevCtx, prevSpan, prevStep
		return aborted, nil
	}

	rctx.fields.Set(stepPath+".outcome", out.label())
	rctx.sink.Observe(rctx.controller, n.name, out.label(), dur)

	var framed error
	if err != nil {
		observability.FoldError(rctx.fields, stepPath, err)
		framed = observability.FrameError(n.name, err)
		span.RecordError(framed)
		span.SetStatus(codes.Error, framed.Error())
	}

	span.End()
	rctx.ctx, rctx.span, rctx.curStepPath = prevCtx, prevSpan, prevStep
	return out, framed
}

// isCancellation reports whether a step error is a consequence of the reconcile
// context being canceled or expired — typically the manager shutting down — rather
// than a genuine failure. It triggers on the reconcile context being done (which
// catches shutdown and any deadline on the reconcile context) and, defensively, on
// an error that wraps context.Canceled. A step's own sub-context deadline does not
// make the reconcile context done, so a real per-call timeout still surfaces as an
// error.
func isCancellation(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled)
}
