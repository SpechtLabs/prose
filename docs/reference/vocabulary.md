---
title: The Vocabulary
permalink: /reference/vocabulary
createTime: 2026/06/03 12:00:00
---

The whole DSL is a small set of words. This page lists them in one place so you can scan for the one you want; for exact signatures and option types, follow through to the [godoc](https://pkg.go.dev/github.com/spechtlabs/prose/pkg/prose).

## Entry point

| Call | What it does |
| --- | --- |
| `prose.For[T](mgr)` | Begins a pipeline for objects of type `T` (a `client.Object` pointer like `*v1alpha1.Foo`), managed by `mgr`. Returns a `*Builder[T]`. |

## Builder chain

Everything below is a method on `*Builder[T]`. They return the builder (or a `*Gate[T]` that embeds it), so the chain reads top to bottom as what the controller does.

| Method | What it does |
| --- | --- |
| `.Owns(obj, ...opts)` | Re-reconcile the owner when an owned child of this type changes. Owner references are stamped by `rctx.Apply`. |
| `.Watches(obj, handler, ...opts)` | The full controller-runtime watch surface, including `EnqueueRequestsFromMapFunc` and `builder.WithPredicates`. |
| `.WithPredicates(...preds)` | Event filters on the primary (`For`) watch. See [`IgnoreStatusOnlyUpdates`](#predicates). |
| `.WithObservability(...opts)` | Wire tracing, wide events, and Kubernetes events. Configured once, here. |
| `.WithFinalizer(name)` | Override the finalizer name (otherwise derived from the object's GVK). |
| `.Step(name, fn)` | Add a named step to the top-level pipeline. |
| `.Describe(name, fn)` | Add a named group; its steps become child spans. |
| `.Context(name, fn)` | Exact alias of `Describe`, so a group can read as natural language. |
| `.When(label, pred, ...fn)` | Add a predicate-gated group. With a closure, fills the group inline; without one, call `.Skip()`. Returns a `*Gate[T]`. |
| `.Finalize(name, fn)` | Declare the deletion-mode pipeline. Runs only when the object is being deleted; the finalizer is removed once it succeeds. |
| `.Complete()` | Build and register the reconciler with the manager. Returns the underlying `*builder.TypedBuilder` (or an error) for the escape hatch. |

`*Gate[T]` (returned by `When`) embeds the builder, so the chain continues either way; it adds one method:

| Method | What it does |
| --- | --- |
| `.Skip()` | Turn the gate into a short-circuit: when the predicate holds, the whole reconcile stops successfully. The sugar for pause / finalizing / deletion checks. |

## Group façade

Inside a `Describe`/`Context`/`When` closure you get a `*prose.Group[T]` with the same small vocabulary:

| Method | What it does |
| --- | --- |
| `g.Step(name, fn)` | Add a named step to the group. |
| `g.Describe(name, fn)` | Nest a group. |
| `g.Context(name, fn)` | Alias of `Describe`. |
| `g.When(label, pred, ...fn)` | Nest a predicate-gated group; `.Skip()` available on the returned gate. |

## The step context

A step is a `func(rctx *prose.Context[T]) (prose.Outcome, error)`. The context is everything a step touches:

| Method | What it does |
| --- | --- |
| `rctx.Object()` | The typed object, already fetched; no `Get`, no cast. |
| `rctx.Set(key, value)` | Contribute a field. Lands in the wide event and on the current span, under a dotted key mirroring the group path. |
| `rctx.Apply(obj)` | Server-side apply, stamping the controller owner reference from the reconciled object. |
| `rctx.Event(type, reason, fmt, args...)` | Record a Kubernetes event against the object. Opt-in per step; no-ops without a `Recorder`. |
| `rctx.Client()` | The raw controller-runtime client, for work the DSL doesn't model. |
| `rctx.Context()` | The reconcile `context.Context`. |
| `rctx.DeferCleanup(fn)` | Register a cleanup that always runs, LIFO. Use with caution; see [Clean Up Safely](/guides/cleanup). |
| `rctx.DeferErrorCleanup(fn)` | Register compensation that runs LIFO only when a later step errors. The recommended primitive. |

## Outcomes

A step returns one of these alongside its error. See [Outcomes](/reference/outcomes) for the full semantics and the `ctrl.Result` mapping.

| Value | Meaning |
| --- | --- |
| `prose.Continue` | Success; proceed to the next step. |
| `prose.Requeue` | Come back immediately, paired with backoff. |
| `prose.RequeueAfter(d)` | Come back after duration `d`. Not an error. |
| `prose.Done` | Reconcile complete; stop the pipeline successfully. |

## Observability options

Passed to `.WithObservability(...)`:

| Option | What it configures |
| --- | --- |
| `prose.Otel(tracer)` | Per-step and per-group spans to an OpenTelemetry tracer. |
| `prose.WideEvents(logger)` | One canonical structured log line per reconcile, to a `logr.Logger`. |
| `prose.Recorder(rec)` | Kubernetes events via `rctx.Event`, to a `record.EventRecorder`. |

## Gates and predicates {#predicates}

| Helper | What it does |
| --- | --- |
| `prose.Match[T](matcher)` | Adapt a non-panicking Gomega matcher into a gate predicate `func(T) bool`. |
| `prose.IgnoreStatusOnlyUpdates()` | A deletion-safe predicate for `.WithPredicates`: skip updates that only changed status, while still observing spec changes, creation, and deletion. |

::: tip
A predicate is just `func(T) bool`. `prose.Match` is one way to build one; a plain Go function is the other. Inside step bodies you write plain Go, never matchers; that's where branching and requeue live.
:::
