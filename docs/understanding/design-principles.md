---
title: Design Principles
permalink: /understanding/design-principles
createTime: 2026/06/03 12:00:00
---

`prose` has six design principles, and they're not a values statement; they're the reasoning that produced specific API shapes. Each one explains why a function looks the way it does, and most of them rule out an alternative that would have been easier to ship and worse to live with. Read them in order, because the first one generates the rest.

If you haven't yet, [The Mental Model](/understanding/mental-model) introduces the vocabulary these principles act on. This page is the *why*.

## 1. A reconcile is one observable transaction

Every other decision is downstream of this sentence.

A controller-runtime reconciler is mechanically one function with a fixed contract: take an object, return `(ctrl.Result, error)`. `prose` adds a claim on top of the mechanics, that the function is a *transaction* with a beginning (fetch the object), a body (an ordered sequence of steps), and an end (emit exactly one record describing everything that happened). Once you accept that framing, the boundary becomes framework-owned and the body becomes yours.

The concrete consequence is the runner. `For[T](mgr)` opens the transaction by owning the `Get` and the `IgnoreNotFound` prologue, and `Complete()` closes it by wiring the emit into a `defer` so no path can skip it. You never write `Reconcile`; you write the body, and the frame around it is the transaction. This is also why there are no before/after hooks: the "before" is the framework's prologue or your first step, and the "after" is the emit boundary, which is framework-owned precisely so it can't be skipped. A hook would be a second place for reconcile logic to live, and the whole point is that there's one.

## 2. Steps describe what happened; the framework decides how it becomes telemetry

This is the inversion the library turns on. Business logic sets fields and returns outcomes. It never logs, traces, or counts.

The reason is signal-to-noise under pressure. Operators that take observability seriously end up interleaving log calls, span annotations, metric increments, and event emissions through the business logic, and two bad things happen at once: the reconcile body gets hard to read, and the observability is *still* usually incomplete because nobody instruments every path consistently. `prose` resolves both by removing the choice. A step contributes fields with `rctx.Set` and returns an outcome, and that's the entire observable contract.

The consequence shows up in what `prose.Context` does and doesn't expose. It gives you `rctx.Set(key, value)` and `rctx.Object()` and raw client access; it does not hand you a logger to sprinkle through the function or a span to annotate by hand. The field you set lands in the wide event and the OTel span automatically, because the framework, not your step, owns the translation from "what happened" to "telemetry." A step physically can't scatter log lines, because there's nothing to scatter them with.

## 3. Linear and explicit beats clever and implicit

Borrow [Ginkgo](https://github.com/onsi/ginkgo)'s vocabulary; reject its engine. What runs, and when, is what you read.

Ginkgo's `Describe` / `Context` / `When` vocabulary is genuinely good at making a structure read like prose, and `prose` keeps it. What `prose` refuses is Ginkgo's execution model: the spec-tree collection pass, the closure-registration indirection, the panic-based control flow that lets an assertion abort a node from deep in a call stack. That machinery is the right call for a test framework and the wrong call for a reconciler, where you'll be reading the code while something's on fire and you need the execution order to be the reading order.

So groups in `prose` execute depth-first, in declaration order, every time, with no hidden tree-building and no reordering. The `Group` closure runs immediately and registers its children in the order you wrote them; there's no separate "run" phase that reshuffles anything. [Borrowing from Ginkgo](/understanding/ginkgo) is the full version of this argument, including which Ginkgo features `prose` deliberately doesn't reproduce and why.

## 4. Requeue is a result, not an error

Backoff stays a first-class signal.

In raw controller-runtime, "come back in 30 seconds" lives in `ctrl.Result`, but the path of least resistance, especially once a codebase grows, is to invent an error type that carries the requeue duration and let it ride the error return. The moment you do that, every error-handling decision downstream has to ask "is this a real failure or just a requeue wearing an error costume?" and the answer is buried in a type assertion. The signal gets corrupted.

`prose` keeps the two separate in the return signature itself: a step returns `(prose.Outcome, error)`. The outcome carries requeue intent (`Requeue`, `RequeueAfter(d)`, `Done`, `Continue`); the error carries failure. You can return `prose.RequeueAfter(d)` with a `nil` error and mean exactly "nothing went wrong, come back later," and the runner records the outcome as `requeue` in the wide event without ever treating it as a failure. Backoff stays a clean, first-class signal because it never has to pretend to be an error to travel.

## 5. Observability is a boundary, not a sprinkle

One wide event, fed once, emitted unmissably.

A reconcile is one logical transaction, so it should produce one record, not fifteen scattered log lines you have to grep and correlate across step boundaries. Steps feed an accumulating context through `rctx.Set`, and when the reconcile returns, the framework emits exactly one structured wide event containing every field, flattened with dotted keys that mirror your group nesting. The emit runs in a `defer` inside the runner, so no early return, requeue, or error path can skip it. The record is structurally unmissable, not unmissable-if-you-remember.

The consequence runs deeper than "log once," and it's about which fields go where. One `rctx.Set` lands in both the wide log event and the OTel span, so you write a field once and it shows up in your logs and your traces. Metrics, though, come through a *separate* door keyed only by `(controller, step, outcome)`, never from `rctx.Set`, because logs and spans tolerate high-cardinality fields and Prometheus does not. The two doors are kept apart in the type system on purpose, so an arbitrary field key can never blow up your metric cardinality. [Observability as a Boundary](/understanding/observability) is the full treatment, including why Kubernetes events can't be folded into the wide event.

## 6. Every hard case has a clean door out

The escape hatch is first-class.

A DSL that builds the whole reconciler is delightful for CRUD-shaped operators and a straitjacket the moment you need a non-obvious watch, a custom rate limiter, leader-election-aware setup, or raw client access mid-pipeline. Those are exactly the operators where saving boilerplate matters most, so an escape hatch that's bolted on as an afterthought fails precisely when you need it. `prose` designs the door in.

Three doors, specifically. `Watches` and predicates are exposed fully, including `handler.EnqueueRequestsFromMapFunc` and `builder.WithPredicates`, so the framework doesn't model the easy 80% of triggering and abandon you for the 20% that's the reason the operator is hard. `Complete()` returns the underlying `*builder.Builder` (or an error), so you can drop to raw controller-runtime for anything `prose` doesn't model. And inside a step, `rctx` exposes the raw `client.Client`, the `context.Context`, and the typed object, so no step is ever trapped by the DSL.

The design test for `prose` was never "does the happy path read well," because it does and that's the easy part. The test was "can I express a `MapFunc` watch and a custom predicate without leaving the DSL." If every hard case has a clean door out, the DSL is a strict win, and that test is the reason principle six exists.

## How they hang together

Principle one is the axiom. Two and five are the observability half: describe what happened, and let the boundary turn it into telemetry. Three and four are the control-flow half: read what runs, and keep requeue honest. Six is the pressure-relief valve that keeps the other five from becoming a cage. Pull on any of them and you end up back at the first sentence, which is the point.

Next: [Observability as a Boundary](/understanding/observability) for the deep version of principle five, or [Borrowing from Ginkgo](/understanding/ginkgo) for the full argument behind principle three.
