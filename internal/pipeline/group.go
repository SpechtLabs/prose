package pipeline

import "sigs.k8s.io/controller-runtime/pkg/client"

// StepFunc is one named, observable unit of reconcile work. It receives the typed
// reconcile context and returns an outcome and an error. It never logs, traces, or
// counts: it contributes fields via rctx.Set and returns outcomes, and the
// framework turns that into telemetry around it.
type StepFunc[T client.Object] func(rctx *Context[T]) (Outcome, error)

// Predicate is a pure boolean question over the reconciled object. It has no side
// effects and never requeues, which is exactly why a matcher algebra is a clean
// fit here and nowhere else: a gate either holds or it does not.
type Predicate[T client.Object] func(obj T) bool

// node is one entry in the pipeline tree: either a step (leaf, fn set) or a group
// (isGroup, children set). Groups may carry a predicate (When) and a skip flag
// (When(...).Skip()). The tree is built lexically at SetupWithManager time and
// executed depth-first, in declaration order, every reconcile.
type node[T client.Object] struct {
	name string

	// step
	fn StepFunc[T]

	// group
	isGroup  bool
	pred     Predicate[T] // nil for Describe/Context; set for When
	skip     bool         // When(...).Skip(): stop the whole reconcile when pred holds
	children []*node[T]
}

// Group is the façade handed to a Describe/Context/When closure. It is a thin
// wrapper over the group node being filled, exposing the small step-and-group
// vocabulary while keeping the tree type unexported.
type Group[T client.Object] struct {
	n *node[T]
}

// Step adds a named step to the group.
func (g *Group[T]) Step(name string, fn StepFunc[T]) *Group[T] {
	g.n.children = append(g.n.children, &node[T]{name: name, fn: fn})
	return g
}

// Describe adds a nested group. A group is a step that contains steps: it
// structures spans (a parent span over child spans) and scopes gating.
func (g *Group[T]) Describe(name string, build func(*Group[T])) *Group[T] {
	child := &node[T]{name: name, isGroup: true}
	build(&Group[T]{n: child})
	g.n.children = append(g.n.children, child)
	return g
}

// Context is an exact alias of Describe; it exists only so a group can read as
// natural language ("now that both ends exist").
func (g *Group[T]) Context(name string, build func(*Group[T])) *Group[T] {
	return g.Describe(name, build)
}

// When adds a group gated by a predicate. With a build closure the gated group is
// filled inline; without one, call .Skip() on the returned gate to make the
// predicate stop the whole reconcile (the pause/finalizing/deletion gate).
func (g *Group[T]) When(label string, pred Predicate[T], build ...func(*Group[T])) *groupGate[T] {
	if len(build) > 1 {
		panic("prose: When accepts at most one group closure")
	}
	child := &node[T]{name: label, isGroup: true, pred: pred}
	for _, fn := range build {
		fn(&Group[T]{n: child})
	}
	g.n.children = append(g.n.children, child)
	return &groupGate[T]{Group: g, n: child}
}

// groupGate is returned by Group.When. It embeds the parent Group so the chain
// continues, and offers Skip() for the gate-and-stop case.
type groupGate[T client.Object] struct {
	*Group[T]
	n *node[T]
}

// Skip turns the gate into a short-circuit: when the predicate holds, the whole
// reconcile stops successfully instead of running anything further.
func (gg *groupGate[T]) Skip() *Group[T] {
	gg.n.skip = true
	return gg.Group
}
