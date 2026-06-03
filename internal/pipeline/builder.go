package pipeline

import (
	"fmt"
	"reflect"
	"strings"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/spechtlabs/prose/internal/observability"
)

// Builder is the entry point to a prose pipeline. It captures the manager, accrues
// the controller-runtime wiring (Owns/Watches), the observability sink, and the
// ordered tree of steps and groups, and registers a reconciler with the manager on
// Complete.
type Builder[T client.Object] struct {
	mgr  manager.Manager
	crb  *builder.TypedBuilder[reconcile.Request]
	sink *observability.Sink

	root     *node[T] // the normal pipeline, a synthetic root group
	finalize *node[T] // the Finalize group, nil if none

	finalizer string              // overridable; derived from the object GVK at Complete
	forOpts   []builder.ForOption // predicates/options for the primary (For) watch

	conflictTolerance int // consecutive conflicts absorbed as quiet requeues before going loud
}

// For begins a prose pipeline for objects of type T managed by mgr.
func For[T client.Object](mgr manager.Manager) *Builder[T] {
	return &Builder[T]{
		mgr:               mgr,
		crb:               builder.TypedControllerManagedBy[reconcile.Request](mgr),
		root:              &node[T]{isGroup: true},
		conflictTolerance: maxQuietConflicts,
	}
}

// Owns declares an owned child object type: the controller re-reconciles the owner
// when a child it owns changes. (Owner references themselves are stamped by
// rctx.Apply.)
func (b *Builder[T]) Owns(object client.Object, opts ...builder.OwnsOption) *Builder[T] {
	b.crb = b.crb.Owns(object, opts...)
	return b
}

// Watches exposes controller-runtime's full Watches surface, including
// handler.EnqueueRequestsFromMapFunc and builder.WithPredicates, so non-obvious
// triggering is in reach without leaving the DSL.
func (b *Builder[T]) Watches(object client.Object, eventHandler handler.EventHandler, opts ...builder.WatchesOption) *Builder[T] {
	b.crb = b.crb.Watches(object, eventHandler, opts...)
	return b
}

// WithPredicates sets event filters on the primary (For) watch. The headline use
// is cutting reconcile churn from a controller reacting to its own status writes:
// pass IgnoreStatusOnlyUpdates(). Note that a bare predicate.GenerationChangedPredicate
// is unsafe here — it also drops the metadata-only update that sets the deletion
// timestamp, so a controller with a Finalize stage would never observe deletion and
// its finalizer would wedge the object. Predicates for owned and watched types are
// passed through the Owns/Watches options instead.
func (b *Builder[T]) WithPredicates(predicates ...predicate.Predicate) *Builder[T] {
	b.forOpts = append(b.forOpts, builder.WithPredicates(predicates...))
	return b
}

// WithConflictTolerance sets how many consecutive optimistic-concurrency conflicts
// on a single object prose absorbs as quiet requeues before letting the error
// propagate loudly. The default is 3. Pass 0 to make every conflict loud
// immediately (no quieting); a higher value rides out noisier write contention.
// The streak resets on any non-conflict outcome, and the count is always recorded
// in the wide event as conflict.count regardless of this threshold.
func (b *Builder[T]) WithConflictTolerance(n int) *Builder[T] {
	b.conflictTolerance = n
	return b
}

// WithObservability configures the one place telemetry is wired: tracing, wide
// events, and Kubernetes events. Configured once, never touched inside a step.
func (b *Builder[T]) WithObservability(opts ...observability.Option) *Builder[T] {
	b.sink = observability.NewSink(opts...)
	return b
}

// WithFinalizer overrides the finalizer name (otherwise derived from the GVK).
func (b *Builder[T]) WithFinalizer(name string) *Builder[T] {
	b.finalizer = name
	return b
}

// Step adds a named step to the top-level pipeline.
func (b *Builder[T]) Step(name string, fn StepFunc[T]) *Builder[T] {
	b.root.children = append(b.root.children, &node[T]{name: name, fn: fn})
	return b
}

// Describe adds a named group to the top-level pipeline.
func (b *Builder[T]) Describe(name string, build func(*Group[T])) *Builder[T] {
	child := &node[T]{name: name, isGroup: true}
	build(&Group[T]{n: child})
	b.root.children = append(b.root.children, child)
	return b
}

// Context is an exact alias of Describe.
func (b *Builder[T]) Context(name string, build func(*Group[T])) *Builder[T] {
	return b.Describe(name, build)
}

// When adds a predicate-gated group at the top level. Without a closure, call
// .Skip() on the returned gate for the pause/finalizing/deletion short-circuit.
func (b *Builder[T]) When(label string, pred Predicate[T], build ...func(*Group[T])) *Gate[T] {
	if len(build) > 1 {
		panic("prose: When accepts at most one group closure")
	}
	child := &node[T]{name: label, isGroup: true, pred: pred}
	for _, fn := range build {
		fn(&Group[T]{n: child})
	}
	b.root.children = append(b.root.children, child)
	return &Gate[T]{Builder: b, n: child}
}

// Finalize declares the deletion-mode pipeline: it runs only when the object is
// being deleted, and the framework removes the finalizer once the group succeeds.
func (b *Builder[T]) Finalize(name string, build func(*Group[T])) *Builder[T] {
	child := &node[T]{name: name, isGroup: true}
	build(&Group[T]{n: child})
	b.finalize = child
	return b
}

// Gate is returned by Builder.When. It embeds the Builder so the chain continues
// in either form, and offers Skip() for the gate-and-stop case.
type Gate[T client.Object] struct {
	*Builder[T]
	n *node[T]
}

// Skip turns the gate into a short-circuit: when the predicate holds, the whole
// reconcile stops successfully. This is the sugar for pause/finalizing/deletion
// checks every operator repeats.
func (g *Gate[T]) Skip() *Builder[T] {
	g.n.skip = true
	return g.Builder
}

// Complete builds the reconciler, registers it with the manager, and returns the
// underlying controller-runtime builder (or an error) so a caller can drop to raw
// controller-runtime for anything prose does not model.
func (b *Builder[T]) Complete() (*builder.TypedBuilder[reconcile.Request], error) {
	if b.sink == nil {
		b.sink = observability.NewSink()
	}

	factory, err := newObjectFactory[T]()
	if err != nil {
		return b.crb, err
	}
	obj := factory()

	gvk, err := apiutil.GVKForObject(obj, b.mgr.GetScheme())
	if err != nil {
		return b.crb, err
	}
	controllerName := strings.ToLower(gvk.Kind)
	finalizer := b.finalizer
	if finalizer == "" {
		finalizer = deriveFinalizer(gvk.Kind, gvk.Group)
	}

	r := &runner[T]{
		client:       b.mgr.GetClient(),
		scheme:       b.mgr.GetScheme(),
		sink:         b.sink,
		newObject:    factory,
		root:         b.root,
		finalize:     b.finalize,
		controller:   controllerName,
		finalizer:    finalizer,
		fieldOwner:   "prose-" + controllerName,
		baseLogger:   b.sink.Logger().WithValues("controller", controllerName),
		conflicts:    newConflictTracker(),
		maxConflicts: b.conflictTolerance,
	}

	b.crb = b.crb.For(obj, b.forOpts...)
	if err := b.crb.Complete(r); err != nil {
		return b.crb, err
	}
	return b.crb, nil
}

// newObjectFactory returns a constructor for fresh instances of T. T is required
// to be a pointer to a struct implementing client.Object (the usual CRD shape).
func newObjectFactory[T client.Object]() (func() T, error) {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil || rt.Kind() != reflect.Pointer {
		return nil, humane.New(
			fmt.Sprintf("prose: type parameter %T must be a pointer to a struct implementing client.Object", zero),
			"instantiate prose.For with a pointer type, for example prose.For[*v1alpha1.Foo]")
	}
	elem := rt.Elem()
	return func() T {
		return reflect.New(elem).Interface().(T)
	}, nil
}

// deriveFinalizer builds a qualified finalizer name from the object's kind and
// API group, for example "memcached.cache.example.com/finalizer".
func deriveFinalizer(kind, group string) string {
	name := strings.ToLower(kind)
	if group != "" {
		name = name + "." + group
	}
	return name + "/finalizer"
}
