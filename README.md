# prose

Kubernetes operators that read like prose.

`prose` is a thin DSL over [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) for building Kubernetes operators as a linear, observable sequence of steps. You describe _what_ a reconcile does as a series of named actions; `prose` handles the boilerplate around them (the `Get`, the requeue plumbing, the setup wiring) and gives you OpenTelemetry tracing, structured wide-event logging, Prometheus metrics, and Kubernetes events for free, without a single observability call in your business logic.

```go
prose.For[*v1alpha1.Foo](mgr).
    Owns(&appsv1.Deployment{}).
    Owns(&corev1.ConfigMap{}).
    When("paused", isPaused).Skip().
    Describe("dependencies", func(g *prose.Group[*v1alpha1.Foo]) {
        g.Step("configmap", upsertConfigMap)
        g.Step("deployment", upsertDeployment)
    }).
    Step("status", syncStatus).
    Complete()
```

There is no `Reconcile` method to write and no `SetupWithManager` to wire up. There is one sentence per thing the operator does.

## Why this exists

A controller-runtime reconciler is, mechanically, a single function that must be deterministic, idempotent, return `(ctrl.Result, error)`, and stay readable when you are paged at 3am. In practice it accretes three kinds of noise that bury that intent:

1. **Boilerplate.** Every reconciler opens with the same `Get` + `IgnoreNotFound` prologue and the same pause/finalizer/deletion gating. Every operator wires the same `Owns`/`Watches`/predicates in `SetupWithManager`. None of this is the interesting part, but all of it is in the way.
2. **Scattered observability.** Operators that take observability seriously end up with logging calls, span annotations, metric increments, and event emissions interleaved through the business logic. The signal-to-noise ratio of the reconcile body drops, and the observability is _still_ usually incomplete: controller-runtime gives you reconcile-level metrics but nothing _within_ a reconcile, so a slow or flapping step is invisible.
3. **Implicit control flow.** Requeue-after smuggled into error types, early returns that skip half the logic, partial-failure handling spread across the function. The linear story of "what this reconcile does" gets hard to follow.

`prose` attacks all three by making one claim load-bearing:

> **A reconcile is a single observable transaction.**

Once you commit to that sentence, the rest of the design falls out of it. The transaction has a beginning (fetch the object), a body (an ordered sequence of steps), and an end (emit exactly one record describing everything that happened). Observability is not something you sprinkle into the body; it is the boundary of the transaction itself.

## Design principles

1. **A reconcile is one observable transaction.** Every other decision is downstream of this sentence.
2. **Steps describe what happened; the framework decides how it becomes telemetry.** Business logic sets fields and returns outcomes. It never logs, traces, or counts.
3. **Linear and explicit beats clever and implicit.** Borrow [Ginkgo](https://github.com/onsi/ginkgo)'s vocabulary, reject its engine. What runs, and when, is what you read.
4. **Requeue is a result, not an error.** Backoff stays a first-class signal.
5. **Observability is a boundary, not a sprinkle.** One wide event, fed once, emitted unmissably.
6. **Every hard case has a clean door out.** The escape hatch is first-class.

> [!NOTE]
> `prose` is early and the API surface is still moving. Concepts in this document are stable; signatures may change before a tagged release.

## The mental model

### Steps

A **step** is one named, observable unit of reconcile work. It receives a typed reconcile context and returns an outcome and an error.

```go
func upsertDeployment(rctx *prose.Context[*v1alpha1.Foo]) (prose.Outcome, error) {
    foo := rctx.Object() // already fetched, typed, no Get, no cast

    desired := buildDeployment(foo)
    rctx.Set("deployment.image", desired.Spec.Template.Spec.Containers[0].Image)

    if err := rctx.Apply(desired); err != nil {
        return prose.Requeue, humane.Wrap(err, "apply deployment",
            "check that the controller has RBAC to create Deployments in this namespace")
    }
    return prose.Continue, nil
}
```

Steps do not log, annotate spans, or increment metrics. They _contribute fields_ (`rctx.Set`) and _return outcomes_. Everything observable about a step (its span, its duration, its result, its error) is recorded by the framework around the step, not inside it.

This is the core inversion: business logic describes _what happened_ by setting fields and returning outcomes, and the framework decides _how that becomes telemetry_.

### Outcomes

A step returns a `prose.Outcome`, which keeps requeue semantics first-class rather than smuggled into errors:

| Outcome                 | Meaning                                               |
| ----------------------- | ----------------------------------------------------- |
| `prose.Continue`        | success, proceed to the next step                     |
| `prose.Requeue`         | come back immediately (paired with backoff)           |
| `prose.RequeueAfter(d)` | come back after duration `d`, not an error            |
| `prose.Done`            | reconcile is complete, stop the pipeline successfully |

A step returning an `error` is distinct from a step asking for a requeue. "No error, come back in 30s" is a normal, expected reconcile result and is never represented as an error.

### Groups

A **group** is a step that contains steps. `Describe`, `Context`, and `When` all construct groups. `Describe` and `Context` are identical — `Context` is an alias, there only so a group can read as natural language — while `When` adds a predicate that gates the group.

```go
Describe("dependencies", func(g *prose.Group[*v1alpha1.Foo]) {
    g.Step("configmap", upsertConfigMap)
    g.Step("deployment", upsertDeployment)
})
```

Groups exist for two reasons, and both are concrete:

- **They structure spans.** A group is a parent span; its steps are child spans. Nesting depth in your code equals span nesting depth in your traces. Grouping is how you make a trace readable.
- **They scope gating.** A `When` group runs only if its predicate holds, so you can gate a whole cluster of steps on one condition instead of repeating the check.

Grouping is **lexical and explicit**. There is no hidden tree-building phase and no reordering. Groups execute depth-first, in declaration order, every time. This deliberately borrows Ginkgo's _vocabulary_ (`Describe`/`Context`/`When`) while rejecting its _execution model_: there is no opaque ordering, no closure-registration indirection, and no panic-based control flow. What runs, and when, is exactly what you read top to bottom.

### Gates and predicates

`When` takes a contextual label and a predicate over the object. A gate is a pure boolean question with no side effects and no requeue, which makes it the one place where a matcher algebra is a clean fit. `prose` ships an adapter for [Gomega](https://github.com/onsi/gomega) matchers that uses a non-panicking handler, so a failed match is simply `false`:

```go
When("scaled up",
    prose.Match(gomega.HaveField("Spec.Replicas", gomega.BeNumerically(">", 0))),
    func(g *prose.Group[*v1alpha1.Foo]) {
        g.Step("status", syncStatus)
    })
```

You get the readable `HaveField`/`And`/`Or`/`WithTransform` predicate vocabulary, confined to the one place where "boolean with no control flow" is the correct semantics. Inside step bodies you write plain Go (never matchers) because that is where branching, requeue, and error handling live, and those must stay legible.

`When(label, pred).Skip()` is sugar for the most common gate of all: pause, finalizing, and deletion-timestamp checks that every operator repeats.

## Observability is the transaction boundary

This is the part `prose` exists to get right. A reconcile produces **one** [wide event](https://loggingsucks.com/), one structured record, not one log line per step.

### Wide events (canonical log lines)

Steps contribute fields to an accumulating context via `rctx.Set(key, value)`. When the reconcile returns (success or failure) the framework emits exactly one structured log line containing every field, flattened with dotted keys that mirror your group nesting:

```
controller=foo namespace=team-a name=widget generation=7 result=requeue requeue_after=30s duration=412ms
  dependencies.configmap.duration=8ms  dependencies.configmap.outcome=continue
  dependencies.deployment.duration=190ms dependencies.deployment.outcome=continue dependencies.deployment.image=ghcr.io/...:v2
  status.duration=14ms status.outcome=requeue
```

One reconcile, one queryable row. The alternative, fifteen scattered log lines you have to grep and correlate across step boundaries, is exactly what this model deletes. A reconcile is one logical transaction, so it gets one record.

Emission is structurally unmissable: it runs in a `defer` inside the runner, so no early return, requeue, or error path can skip it.

### One field, three destinations

The same field accumulation that builds the wide log event also annotates the trace. When you call `rctx.Set`, the field lands in:

- the **wide log event** (one `logr` line at the end), and
- the **OpenTelemetry span** as an attribute.

You write the field once; it shows up in both your logs and your traces. OpenTelemetry is not a parallel system you maintain alongside logging; it is fed by the same accumulation. Each step is automatically a child span; each group is a parent span; durations and errors are recorded without a single tracing call in your code.

### Metrics have a separate, narrow door

Logs and spans tolerate high-cardinality fields. Prometheus does not. So metric labels do **not** come from `rctx.Set`; they come from a deliberately bounded path, keyed only by `(controller, step, outcome)`. A per-step histogram lets you see a slow or flapping step, which controller-runtime cannot show you, without ever risking a cardinality explosion from an arbitrary field key. The two doors are kept separate in the type system on purpose.

### Kubernetes events stay per-emit

A Kubernetes `Event` is for the human running `kubectl describe`, not for your observability backend. It cannot be wide-eventified into one mega-event. So event emission stays per-emit and **opt-in per step**, for meaningful state transitions only:

```go
rctx.Event(corev1.EventTypeNormal, "Scaled", "scaled deployment to %d replicas", n)
```

Most steps emit no events. The ones that do are marking transitions a human would want to see.

### The observability sink

All three outputs are configured once, at build time, not per step:

```go
prose.For[*v1alpha1.Foo](mgr).
    WithObservability(
        prose.Otel(tracer),
        prose.WideEvents(logger),                          // one canonical line per reconcile
        prose.Recorder(mgr.GetEventRecorderFor("foo")),    // kubectl-visible events
    ).
    // ... steps ...
    Complete()
```

The logger your steps would otherwise need is _derived_ from the sink, pre-populated with `controller`/`namespace`/`name`, which is how `prose` deletes the "thread the logger through everything" boilerplate.

## Errors

Steps return [humane errors](https://github.com/sierrasoftworks/humane-errors-go) as a first-class citizen. The human-facing message and the underlying cause chain map cleanly onto the wide event:

- `step.<name>.error`: the human-readable message
- `step.<name>.cause`: the unwrapped cause chain

The step name becomes the contextual frame of the error automatically, so a returned error reads `deployment: <cause>` rather than a bare context. `Run` collects errors into the wide event via humane's structured form, not `err.Error()`, so the emitted record preserves the advice and the cause separately.

```go
return prose.Requeue, humane.Wrap(err, "apply deployment",
    "verify the controller's ServiceAccount can create Deployments in this namespace")
```

## Lifecycle and cleanup

`prose` deliberately does **not** reproduce Ginkgo's full `BeforeEach`/`AfterEach`/`DeferCleanup` surface. Most of those concepts collapse into things the model already has, and adding them would create places for reconcile logic to hide _outside_ the linear step pipeline, eroding the "one reconcile, one readable sequence, one wide event" property that is the entire point.

### No before/after hooks

"Before reconcile" is the framework-owned prologue (`Get` + `IgnoreNotFound`) or simply your first step. "After reconcile" is the wide-event emit boundary, which is framework-owned precisely so it cannot be skipped. If you want setup, write `Step("setup", ...)`: explicit, sequenced, and observable like everything else. There is no privileged hook that does what a step does with less visibility.

### Deletion is a mode, not a cleanup

When `DeletionTimestamp` is set, the object is being deleted; that is a distinct reconcile _mode_, not a deferred callback. It has its own vocabulary:

```go
prose.For[*v1alpha1.Foo](mgr).
    Step("configmap", upsertConfigMap).
    Step("deployment", upsertDeployment).
    Finalize("teardown", func(g *prose.Group[*v1alpha1.Foo]) {
        g.Step("external-resource", releaseExternalResource)
        // finalizer removal is handled by the framework once the group succeeds
    }).
    Complete()
```

`Finalize` steps run only on the deletion path; the framework removes the finalizer once they succeed.

### Two cleanup primitives, inverted from Ginkgo

Within a single reconcile, `prose` offers two deferred-cleanup primitives. The naming carries the warning, because their triggers are opposite, and both are the _inverse_ of Ginkgo's `DeferCleanup`, which always runs because a test always tears down. A reconciler converges, so on a successful reconcile the resources you acquired are usually the desired state and must **not** be torn down.

**`DeferErrorCleanup`: recommended. Failure path only.** Compensation for work that a later step then fails to complete. Runs LIFO, only on the unwind path, only when a subsequent step errors.

```go
g.Step("lease", func(rctx *prose.Context[*v1alpha1.Foo]) (prose.Outcome, error) {
    lease, err := acquire(rctx)
    if err != nil {
        return prose.Requeue, humane.Wrap(err, "acquire lease")
    }
    rctx.DeferErrorCleanup(func() error { return release(lease) }) // runs only if a later step fails
    return prose.Continue, nil
})
```

**`DeferCleanup`: use with caution. Always runs, LIFO.** Runs after every reconcile, success or failure. The hazard is specific and stronger than "side effects": an always-run cleanup with any cluster-observable effect couples teardown to reconcile _frequency_ rather than to desired _state_. Reconciles fire constantly (resync periods, watch events, your own status updates), so an always-run cleanup that deletes a resource or decrements a counter is firing on a cadence you do not control, which is the exact bug class reconcilers are supposed to be immune to.

> [!IMPORTANT]
> Use `DeferCleanup` only for releasing resources whose lifetime is exactly one reconcile invocation and whose release has no observable effect on cluster state or external systems: an in-memory buffer, a non-pooled connection, a client you opened for this reconcile. If the cleanup mutates anything another reconcile could observe, it belongs in a step gated on desired state, not in a deferred callback.

The type enforces the caution: a `DeferCleanup` function's error can only ever land in the wide event (`cleanup.<name>.error`); it can never convert a successful reconcile into a requeue or alter the returned error.

### Unwind ordering

The runner guarantees this order, so cleanup outcomes are always captured before emission:

```
run steps depth-first, in declaration order
  on step error:
    stop the pipeline
    run DeferErrorCleanup stack (LIFO)  -> fold cleanup.<name>.* into the event
  always (success or error):
    run DeferCleanup stack (LIFO)       -> fold cleanup.<name>.* into the event
emit the wide event   (deferred, structurally unmissable)
return (ctrl.Result, error) to controller-runtime
```

Error precedence is strict: the original step error is the root cause that propagates to controller-runtime. Cleanup failures are _additive context_ in the wide event; they never replace the original error, and an always-run cleanup failure on the happy path never triggers a requeue of already-converged logic.

## The escape hatch is first-class

A DSL that builds the whole reconciler is delightful for CRUD-shaped operators and a straitjacket the moment you need a non-obvious watch, a custom rate limiter, leader-election-aware setup, or raw client access mid-pipeline. Those are precisely the operators where saving boilerplate matters most, so the escape hatch is designed in, not bolted on.

- `Watches` and predicates are exposed fully, including `handler.EnqueueRequestsFromMapFunc` and `builder.WithPredicates`: the framework does not handle only the easy 80% of triggering and abandon you for the 20% that is the reason the operator is hard.
- `Complete()` returns the underlying `*builder.Builder` (or an error), so you can drop to raw controller-runtime for anything `prose` does not model.
- Inside a step, `rctx` exposes the raw `client.Client`, the `context.Context`, and the typed object, so no step is ever trapped by the DSL.

The design test for `prose` is not "does the happy path read well" (it does). It is "can I express a `MapFunc` watch and a custom predicate without leaving the DSL." If every hard case has a clean door out, the DSL is a strict win.

---

_Part of [SpechtLabs](https://github.com/spechtlabs). Built on [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)._
