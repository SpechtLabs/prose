---
title: Overview
permalink: /getting-started/overview
createTime: 2026/06/03 12:00:00
---

`prose` is a thin DSL over [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) for writing Kubernetes operators as a linear, observable sequence of named steps. You describe *what* a reconcile does as a series of named actions; the framework handles the boilerplate around them and hands you OpenTelemetry tracing, structured wide-event logging, Prometheus metrics, and Kubernetes events without a single observability call in your business logic.

Here is a whole reconciler:

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

There's no `Reconcile` method to write and no `SetupWithManager` body to wire by hand. There is one sentence per thing the operator does, and the order you read is the order it runs.

## The claim everything hangs on

A controller-runtime reconciler is mechanically one function that has to be deterministic, idempotent, return `(ctrl.Result, error)`, and stay readable when you're paged at 3am. In practice it accretes noise that buries that intent, and `prose` exists to delete three specific kinds of it.

**Boilerplate.** Every reconciler opens with the same `Get` plus `IgnoreNotFound` prologue and the same pause, finalizer, and deletion-timestamp gating. Every operator wires the same `Owns`/`Watches`/predicates in setup. None of it is the interesting part, yet all of it is in the way.

**Scattered observability.** Operators that take telemetry seriously interleave logging calls, span annotations, metric increments, and event emissions through the business logic. The signal-to-noise ratio of the reconcile body drops, and the observability is usually *still* incomplete: controller-runtime gives you reconcile-level metrics but nothing *within* a reconcile, so a slow or flapping step stays invisible.

**Implicit control flow.** Requeue-after smuggled into error types, early returns that skip half the logic, partial-failure handling spread across the function. The linear story of what the reconcile does gets hard to follow.

`prose` attacks all three by making one sentence load-bearing:

::: tip The whole design in one line
A reconcile is a single observable transaction.
:::

Once you commit to that, the rest falls out of it. The transaction has a beginning (fetch the object), a body (an ordered sequence of steps), and an end (emit exactly one record describing everything that happened). Observability isn't something you sprinkle into the body; it's the boundary of the transaction itself.

## What you get for free

Steps don't log, annotate spans, or increment counters. A step *contributes fields* with `rctx.Set(key, value)` and *returns an outcome*. Everything observable about it, the span, the duration, the result, the error, gets recorded by the framework around the step rather than inside it. You write a field once and it lands in three places.

The **wide event** is one structured log line per reconcile, with every field flattened under dotted keys that mirror your group nesting. One reconcile, one queryable row, instead of fifteen scattered lines you have to grep and correlate.

The **trace** comes from the same field accumulation. Each step is automatically a child span; each group is a parent span. Nesting depth in your code equals span nesting depth in your traces, and durations and errors are recorded without a tracing call anywhere in your reconcile.

**Per-step metrics** flow through a deliberately narrow door, keyed only by `(controller, step, outcome)`, so a histogram can show you a slow or flapping step that controller-runtime can't surface, without ever risking a cardinality explosion from an arbitrary field key.

**Kubernetes events** stay per-emit and opt-in, for the meaningful state transitions a human running `kubectl describe` would want to see. Most steps emit none.

You configure all three once, at build time, with `WithObservability`. The logger your steps would otherwise have to thread through everything is derived from that sink, pre-populated with `controller`/`namespace`/`name`.

## What it reads like when it runs

```text
controller=foo namespace=team-a name=widget generation=7 result=requeue requeue_after=30s duration=412ms
  dependencies.configmap.duration=8ms  dependencies.configmap.outcome=continue
  dependencies.deployment.duration=190ms dependencies.deployment.outcome=continue dependencies.deployment.image=ghcr.io/...:v2
  status.duration=14ms status.outcome=requeue
```

Emission runs in a `defer` inside the runner, so no early return, requeue, or error path can skip it. The record is structurally unmissable.

## Who it's for

If you're building CRUD-shaped operators, the kind that converge a few owned objects and sync a status, `prose` deletes most of what you'd otherwise type. If your operator is harder, a `MapFunc` watch, a custom predicate, leader-election-aware setup, raw client access mid-pipeline, the escape hatch is designed in rather than bolted on: `Watches` and predicates are exposed fully, `Complete()` returns the underlying `*builder.Builder`, and inside a step `rctx` gives you the raw `client.Client`, the `context.Context`, and the typed object. No step is ever trapped by the DSL.

You should be comfortable with reconcilers, CRDs, and a controller-runtime project before you start. `prose` is a layer on top of controller-runtime, not a replacement for understanding it.

::: note Early days
`prose` is early and the API surface is still moving. The concepts are stable; signatures may change before a tagged release.
:::

## Where to go next

[Prerequisites](/getting-started/prerequisites) lists what you need installed and scaffolded. [Your First Reconciler](/getting-started/quick) takes you from an empty `SetupWithManager` to a running pipeline in about five minutes. When you want every piece of the vocabulary in one reconcile, [The Wormhole Walkthrough](/getting-started/comprehensive) reads a full operator top to bottom.
