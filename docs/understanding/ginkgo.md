---
title: Borrowing from Ginkgo
permalink: /understanding/ginkgo
createTime: 2026/06/03 12:00:00
---

If you've written Go tests with [Ginkgo](https://github.com/onsi/ginkgo), the first `prose` pipeline you read will feel familiar in a way that's deliberate and, in one specific respect, a little misleading. `prose` borrows Ginkgo's vocabulary on purpose. It rejects Ginkgo's engine just as deliberately. Knowing which half is which is the fastest way to build an accurate mental model, so this page is written for the reader who already knows what `Describe` and `Context` do in a spec file.

## What `prose` takes

`Describe`, `Context`, `When`. Those three words are the best thing Ginkgo did for readability, and `prose` keeps them, including the way they let a structure read as English:

```go
Describe("anchors", func(g *prose.Group[*v1alpha1.Wormhole]) {
    g.Step("reserve-coordinates", reserveCoordinates)
    g.Step("entry-anchor", upsertEntryAnchor)
    g.Step("exit-anchor", upsertExitAnchor)
}).
Context("now that both ends exist", func(g *prose.Group[*v1alpha1.Wormhole]) {
    g.Step("subspace-link", openSubspaceLink)
    g.When("charged past the ignition threshold",
        prose.Match(gomega.HaveField("Status.Charge", gomega.BeNumerically(">=", 88))),
        func(g *prose.Group[*v1alpha1.Wormhole]) {
            g.Step("open-tunnel", openTunnel)
        })
})
```

Read that top to bottom and it's a sentence: describe the anchors, then in the context where both ends exist, open the subspace link, and when it's charged past the ignition threshold, open the tunnel. `Describe` and `Context` are identical in `prose` exactly as they're meant to be in Ginkgo; `Context` is an alias that exists so a group can read as a clause instead of a noun. `When` adds the predicate. The matcher in the gate, `gomega.HaveField(...)` wrapped in `prose.Match`, is Gomega doing the one job it's perfect for here: a pure boolean question over the object, with no side effects to hide.

The vocabulary is good because it makes nesting legible, and legible nesting is exactly what a reconciler needs when you're reading it under pressure. So `prose` took it.

## What `prose` throws out

The familiar surface hides a sharp difference underneath, and this is the part that matters. Ginkgo's `Describe`/`Context`/`When` closures don't run your test when they execute. They run a *registration* pass: each closure registers nodes into a spec tree, Ginkgo then collects that whole tree, applies ordering and randomization and parallelization, and *later* walks it to actually run anything. The closure you wrote and the code that eventually runs are separated by an engine you don't see.

That engine is the right design for a test framework and the wrong design for a reconciler. `prose` rejects three things about it:

**No opaque ordering.** Ginkgo can randomize spec order, reorder by container, and parallelize across processes, all of which are features when you're hunting for order-dependent test flakes. A reconciler must be deterministic and idempotent, and you'll be reading it while something's broken. Groups in `prose` execute depth-first, in declaration order, every single time. The reading order is the execution order, full stop.

**No closure-registration indirection.** In `prose` there's no separate "collect the tree, then run it" phase. The `Group` closure runs immediately and registers its children in the order you wrote them; what you read is the structure that runs. There's no hidden tree-building pass between your code and its behavior.

**No panic-based control flow.** This is the big one. Ginkgo's assertions abort a spec by panicking and recovering up the stack, which is how a failed `Expect` in a deeply nested helper can cleanly stop the node. It's elegant for tests and disastrous for a reconciler, where a panic unwinding through your convergence logic is a bug, not a control-flow primitive. `prose` steps return `(prose.Outcome, error)`. Control flow is a value you return, visible at the call site, never a panic thrown from three frames down. A gate that fails is `false`, not a thrown assertion; `prose.Match` even wraps Gomega with a non-panicking handler precisely so a malformed matcher fails closed instead of crashing the reconcile.

The summary fits in one line: `prose` keeps the prose and drops the magic.

## What `prose` deliberately doesn't reproduce

Ginkgo has a rich setup-and-teardown surface, `BeforeEach`, `AfterEach`, `JustBeforeEach`, `DeferCleanup`, and `prose` deliberately doesn't reproduce it. This isn't an oversight or a roadmap gap. Those concepts mostly collapse into things the model already has, and adding them would create places for reconcile logic to hide *outside* the linear step pipeline, which erodes the one property the whole library is built to protect: one reconcile, one readable sequence, one wide event.

Walk through where each one goes.

"Before each" has two honest homes in `prose`. If it's framework concern, it's the prologue: `prose` owns the `Get` and the `IgnoreNotFound` at the start of every reconcile, so the universal "before" is already handled and you never write it. If it's your concern, it's a step: `Step("setup", ...)`, explicit and sequenced and observable like everything else. There's no privileged hook that does what a step does with less visibility, because a privileged hook is a place for logic to live off the page.

"After each" is the emit boundary, and that one is framework-owned on purpose. The wide event fires in a `defer` so it can't be skipped (see [Observability as a Boundary](/understanding/observability) for why that matters). If `prose` exposed an `AfterEach`, it would be a user-controlled slot competing with the framework's emit, a place to forget the record or to sneak in mutation that the wide event then can't see. The after-reconcile slot is reserved precisely so it stays unmissable.

The hazard is general: every hook is a second pipeline. The moment reconcile logic can live in a `BeforeEach` *and* in a step, "read the steps top to bottom" stops being the whole story, and you're back to grepping for the logic that didn't fit the linear shape. `prose` keeps the shape by keeping the steps the only place work happens.

## The inverted cleanup semantics

Ginkgo's `DeferCleanup` is reproduced in name only, and the name carries a warning, because the semantics are inverted. A test always tears down: you provision a fixture, you run the assertion, you tear the fixture back down, and `DeferCleanup` *always* runs because that's what the end of a test is. A reconciler is the opposite. It converges. On a successful reconcile, the resources you acquired are usually the desired state and must **not** be torn down; tearing them down would undo the convergence you just did.

So `prose` splits cleanup into two primitives whose names carry the warning, and both are inverted from Ginkgo's always-run model:

`DeferErrorCleanup` is the one you usually want. It's compensation for work a later step then fails to complete: claim an external slot, and if a subsequent step errors, hand the slot back so the next reconcile doesn't leak it. It runs LIFO, only on the unwind path, only when something later errored. On a clean reconcile it doesn't run at all, which is exactly right, because on a clean reconcile the slot is the desired state.

`DeferCleanup` is the one to use with caution, and it's the closest thing to Ginkgo's version: it always runs, success or failure, LIFO. The hazard is specific and worse than "side effects." An always-run cleanup with any cluster-observable effect couples teardown to reconcile *frequency* instead of desired *state*, and reconciles fire constantly: resync periods, watch events, your own status writes. An always-run cleanup that deletes a resource or decrements a counter is firing on a cadence you don't control, which is the exact bug class reconcilers are supposed to be immune to.

::: warning Use `DeferCleanup` only for reconcile-scoped resources
Reach for `DeferCleanup` only when the resource's lifetime is exactly one reconcile invocation and its release has no observable effect on cluster state or external systems: an in-memory buffer, a non-pooled connection, a client you opened for this pass. If the cleanup mutates anything another reconcile could observe, it belongs in a step gated on desired state, not in a deferred callback.
:::

The type system backs the caution. A `DeferCleanup` function's error can only ever land in the wide event as `cleanup.<name>.error`; it can never convert a successful reconcile into a requeue or alter the returned error. The original step error always wins, and an always-run cleanup failure on the happy path never triggers a requeue of already-converged logic.

For deletion, `prose` doesn't use cleanup hooks at all. Deletion is a *mode*, not a deferred callback: when `DeletionTimestamp` is set, `Finalize` steps run, and the framework removes the finalizer once they succeed. That's a different shape from "tear down at the end," and it gets its own vocabulary because it's a genuinely different thing.

## The one-sentence version

Ginkgo's vocabulary makes nested structure read like English, and that's worth keeping. Ginkgo's engine, the collection pass, the reordering, the panic-based aborts, the always-run cleanup, is built for tests, and a reconciler needs the opposite of every one of those. `prose` keeps the words and writes its own engine underneath.

If this resonates, [The Mental Model](/understanding/mental-model) shows the vocabulary in full, and [Design Principles](/understanding/design-principles) (principle three especially) is the short form of the argument on this page.
