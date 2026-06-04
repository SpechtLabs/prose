---
title: The Mental Model
permalink: /understanding/mental-model
createTime: 2026/06/03 12:00:00
---

`prose` has exactly one idea, and every part of the API is a consequence of it:

::: tip The whole design in one line
A reconcile is a single observable transaction.
:::

If you hold that sentence in your head, you can predict most of how `prose` behaves without reading the reference. A transaction has a beginning, a body, and an end. The beginning is fetching the object. The body is an ordered sequence of steps. The end is emitting exactly one record that describes everything that happened. Observability isn't scattered through the body; it's the boundary of the transaction itself.

This page is the vocabulary, and how the four words compose. If you want the reasoning behind each choice, [Design Principles](/understanding/design-principles) walks the six principles one at a time, and [Observability as a Boundary](/understanding/observability) goes deep on the telemetry side. Here we're just building the model.

## What falls out of the sentence

A controller-runtime reconciler is mechanically one function: it gets an object, does some work, and returns `(ctrl.Result, error)`. The function is yours to write, and so is `SetupWithManager`, and so is every `Get`, every requeue, every log line. `prose` looks at that function and makes a claim about its shape. Because a reconcile is a transaction, it has a frame the framework can own (fetch at the start, emit at the end) and a body you fill in. Because it's *observable*, the frame is where telemetry lives, which means your body never has to.

So the API gives you four things to write in the body, and nothing else you strictly need: **steps**, **outcomes**, **groups**, and **gates**. Everything else is the framework holding the boundary.

## Steps

A step is one named, observable unit of reconcile work. It takes a typed reconcile context and returns an outcome and an error.

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

Look at what's *not* in that function. There's no `Get` and no `IgnoreNotFound`; `rctx.Object()` hands you the object already fetched, typed, and ready. There's no logging call, no span annotation, no metric increment. The step does two observable things: it calls `rctx.Set` to contribute a field, and it returns an outcome. That's the whole contract.

Everything telemetry-shaped about the step (its span, its duration, its result, its error) is recorded by the framework *around* the step, not inside it. The step describes what happened; it doesn't decide how that becomes a log line or a span attribute.

This is the inversion the whole library turns on, so it's worth stating flatly: business logic describes *what happened* by setting fields and returning outcomes, and the framework decides *how that becomes telemetry*. A step that logs is a step doing the framework's job, and `prose` removes the temptation by never giving the step a logger to misuse.

You register a step by name, either at the top level or inside a group:

```go
Step("status", syncStatus)
```

The name isn't decoration. It becomes the span name, the metric label, and the dotted key prefix for any field the step sets. Name your steps the way you'd want to read them in a trace at 3am, because that's exactly where the name shows up.

## Outcomes

A step returns a `prose.Outcome` alongside its error. The outcome is how the step tells the runner what should happen next, and it keeps requeue semantics first-class instead of smuggling them into an error type.

| Outcome | Meaning |
| ----------------------- | ----------------------------------------------------- |
| `prose.Continue` | success; proceed to the next step |
| `prose.Requeue` | come back immediately, paired with the controller's backoff |
| `prose.RequeueAfter(d)` | come back after duration `d`; this is a result, not an error |
| `prose.Done` | the reconcile is complete; stop the pipeline successfully |

The signature is `(prose.Outcome, error)`, and the two return values answer two genuinely different questions. The outcome answers "what should the runner do next?" The error answers "did something go wrong, and what was it?" A step can return `prose.RequeueAfter(30*time.Second)` with a `nil` error, and that's a perfectly healthy reconcile: nothing failed, the object simply isn't done converging and wants another pass in thirty seconds.

That separation is the point. "No error, come back in 30s" is a normal, expected reconcile result, and it's never represented as an error. In raw controller-runtime you either return a `Result{RequeueAfter: ...}` with a `nil` error (fine, but easy to forget) or you reach for a sentinel error type that carries the duration (common, and it poisons your error handling forever). `prose` makes the healthy path the obvious one: the outcome carries the requeue, the error carries the failure, and they don't get confused.

## Groups

A group is a step that contains steps. `Describe`, `Context`, and `When` all construct groups.

```go
Describe("dependencies", func(g *prose.Group[*v1alpha1.Foo]) {
    g.Step("configmap", upsertConfigMap)
    g.Step("deployment", upsertDeployment)
})
```

`Describe` and `Context` are identical; `Context` is an alias that exists only so a group can read as natural language. You write `Describe("dependencies", ...)` when "dependencies" names a phase, and `Context("now that both ends exist", ...)` when the label reads better as a clause. They compile to the same thing. `When` is the third constructor, and it adds a predicate that gates the group; we'll get to gates next.

Groups earn their place for two concrete reasons, and neither is aesthetic.

First, they structure spans. A group is a parent span, and its steps are child spans. The nesting depth in your code equals the nesting depth in your trace, so a `Describe` inside a `Describe` is a span inside a span. Grouping is how you make a trace readable instead of a flat list of forty sibling spans.

Second, they scope gating. A `When` group runs only if its predicate holds, so you can gate a whole cluster of steps on one condition instead of repeating the check at the top of every step in the cluster. Write the condition once, wrap the steps it guards, done.

The execution model is the part that matters most, and it's deliberately boring. Grouping is **lexical and explicit**: there is no hidden tree-building phase, no reordering, no spec-collection pass. Groups execute depth-first, in declaration order, every single time. What runs, and when, is exactly what you read top to bottom. If you've used Ginkgo, the vocabulary will feel immediately familiar and the execution will feel suspiciously straightforward; that's intentional, and [Borrowing from Ginkgo](/understanding/ginkgo) explains exactly which half `prose` kept and which half it threw out.

## Gates and predicates

`When` takes a contextual label and a predicate over the object, and it gates the group on that predicate.

```go
When("scaled up",
    prose.Match(gomega.HaveField("Spec.Replicas", gomega.BeNumerically(">", 0))),
    func(g *prose.Group[*v1alpha1.Foo]) {
        g.Step("status", syncStatus)
    })
```

A gate is a pure boolean question: does this condition hold, yes or no? It has no side effects, it never sets a field, and it never requests a requeue. That purity is what makes a gate the one place in `prose` where a matcher algebra fits cleanly. Everywhere else, matchers would hide control flow; here, there's no control flow to hide, just a predicate that's either true or false.

`prose.Match` adapts a [Gomega](https://github.com/onsi/gomega) matcher into a gate predicate using a non-panicking handler, so a failed or malformed match is simply `false` rather than a crashed reconcile. You get the readable `HaveField` / `And` / `Or` / `WithTransform` vocabulary, confined to the one spot where "boolean with no control flow" is the correct semantics. You can equally pass a plain `func(obj T) bool`; the matcher adapter is sugar, not a requirement.

Inside step bodies you write plain Go, never matchers, because that's where branching, requeue, and error handling live, and those have to stay legible to whoever's reading the function under pressure. The gate is declarative because a gate is genuinely a question; the step body is imperative because real work is genuinely a sequence of decisions.

One gate is so common it gets its own sugar:

```go
When("paused", isPaused).Skip()
```

`Skip()` is the pause, finalizing, and deletion-timestamp check that every operator repeats. Rather than make you wrap your entire pipeline in a `When(...)` group, the gate short-circuits the whole reconcile when its predicate holds.

## How the four compose

Read a `prose` pipeline and you're reading the transaction in order. Steps are the units of work. Outcomes are how each unit reports what should happen next. Groups nest the units so the trace mirrors the code and a condition can guard a whole phase. Gates ask the yes/no questions that decide whether a phase runs at all.

```go
prose.For[*v1alpha1.Foo](mgr).
    When("paused", isPaused).Skip().
    Describe("dependencies", func(g *prose.Group[*v1alpha1.Foo]) {
        g.Step("configmap", upsertConfigMap)
        g.Step("deployment", upsertDeployment)
    }).
    Step("status", syncStatus).
    Complete()
```

Skip while paused; otherwise converge the dependencies (a config map, then a deployment, as a named group so the trace shows them under one parent); then sync the status. One reconcile, one ordered story, and when it runs it produces one wide event with a row per step. The body you wrote contains zero observability calls, because observability isn't in the body. It's the boundary.

That's the model. From here, [Design Principles](/understanding/design-principles) gives you the *why* behind each choice, [Observability as a Boundary](/understanding/observability) shows what the boundary actually emits, and the [API reference](/reference/api) has the signatures. If you'd rather build than read, [Your First Reconciler](/getting-started/quick) gets a pipeline running in a few minutes.
