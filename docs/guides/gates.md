---
title: Gate a group on a predicate
permalink: /guides/gates
createTime: 2026/06/03 12:00:00
---

Some work shouldn't run unless a condition holds. The deployment should only roll forward once the config is in place; the traffic step should only fire when there's traffic to route; nothing should run at all while the object is paused. In a hand-written reconciler these become `if` statements scattered through the body, each one a place where a later edit can skip half the logic by accident. `prose` pulls the condition out front with `When`, so the gate reads as one line and applies to a whole group at once.

## A gate is a pure boolean question

`When` takes a contextual label and a predicate over the object, and it constructs a group that runs only if the predicate holds. The predicate is a plain `func(T) bool`:

```go
func isScaledUp(m *cachev1alpa1.Memcached) bool {
    return m.Spec.Size > 0
}

prose.For[*cachev1alpa1.Memcached](mgr).
    When("scaled up", isScaledUp,
        func(g *prose.Group[*cachev1alpa1.Memcached]) {
            g.Step("deployment", upsertDeployment)
            g.Step("service", upsertService)
        }).
    Step("status", syncStatus).
    Complete()
```

Read that straight down: when the object is scaled up, converge the deployment and service; always sync status. The `status` step runs regardless because it's outside the gate. The two steps inside run only when `isScaledUp` returns `true`, and you wrote the check once instead of guarding each step.

A gate has a deliberately narrow contract. It's a pure boolean question with no side effects, no requeue, and no error return. That narrowness is the point: because a gate can't branch, can't fail, and can't ask to come back later, it's the one place in the whole pipeline where a matcher algebra is a clean fit instead of a way to hide control flow.

## The gomega adapter

Writing a named `func(T) bool` for every gate gets tedious when the condition is a simple field check. `prose.Match` adapts a [Gomega](https://github.com/onsi/gomega) matcher into a gate predicate, so you can express the condition inline with the readable matcher vocabulary:

```go
When("scaled up",
    prose.Match[*cachev1alpa1.Memcached](
        gomega.HaveField("Spec.Size", gomega.BeNumerically(">", 0))),
    func(g *prose.Group[*cachev1alpa1.Memcached]) {
        g.Step("deployment", upsertDeployment)
    })
```

The adapter is safe by construction. A Gomega matcher's `Match` method returns `(success, error)` and doesn't panic on its own; panicking is a property of `Expect`/`Ω` plus the default fail handler, which `prose.Match` never invokes. A matcher that errors, say `HaveField` against a field that doesn't exist, is treated as "does not hold" rather than propagated. So a malformed gate fails closed, returning `false`, instead of crashing a reconcile. That's the behavior you want: a gate that can't evaluate is a gate that didn't open.

### The matcher vocabulary

You get the same composable matchers you'd use in a Ginkgo suite, confined to the one place where "boolean with no control flow" is the correct semantics:

- `HaveField("Status.Charge", gomega.BeNumerically(">=", 88))` reaches into a nested field by path and applies a sub-matcher.
- `And(...)` and `Or(...)` compose several matchers into one.
- `WithTransform(fn, matcher)` runs a function over the object first, then matches on the result, which is how you express a condition that isn't a plain field read.

A compound gate reads like the sentence it is:

```go
When("ready to route",
    prose.Match[*v1alpha1.Wormhole](gomega.And(
        gomega.HaveField("Status.Charge", gomega.BeNumerically(">=", 88)),
        gomega.HaveField("Spec.Throughput", gomega.BeNumerically(">", 0)),
    )),
    func(g *prose.Group[*v1alpha1.Wormhole]) {
        g.Step("route-traffic", routeTraffic)
    })
```

::: warning Matchers belong in gates, nowhere else
Inside a step body you write plain Go, never matchers. That's where branching, requeue, and error handling live, and those have to stay legible as ordinary control flow. The matcher vocabulary earns its place in a gate precisely because a gate has no control flow to obscure. Don't reach for `gomega` inside a `Step`.
:::

## `Skip()` for the gates every operator repeats

Three checks show up in nearly every reconciler: is the object paused, is it being finalized, has its deletion timestamp been set. `When(label, pred).Skip()` is sugar for exactly that family of gate, where a `true` predicate means "stop here, don't run the pipeline":

```go
func isPaused(m *cachev1alpa1.Memcached) bool {
    return m.Spec.Paused
}

prose.For[*cachev1alpa1.Memcached](mgr).
    When("paused", isPaused).Skip().
    Describe("dependencies", func(g *prose.Group[*cachev1alpa1.Memcached]) {
        g.Step("deployment", upsertDeployment)
    }).
    Step("status", syncStatus).
    Complete()
```

When `isPaused` returns `true`, the reconcile stops cleanly before any step runs, and it still emits its wide event recording that it skipped. This is the inverse polarity of a normal `When` group: a `When(...).Skip()` predicate that holds means "don't proceed," whereas a `When(label, pred, fn)` predicate that holds means "run this group." The `Skip()` sugar exists because the pause and deletion gates are the most common thing you'd otherwise hand-write at the top of every reconciler.

## Nesting gates inside groups

A `Group` has its own `When`, so you can gate work on a condition that only makes sense once an earlier step has run. The Wormhole reconciler nests two gates this way:

```go
Context("now that both ends exist", func(g *prose.Group[*v1alpha1.Wormhole]) {
    g.Step("subspace-link", openSubspaceLink)

    g.When("charged past the ignition threshold",
        prose.Match[*v1alpha1.Wormhole](
            gomega.HaveField("Status.Charge", gomega.BeNumerically(">=", 88))),
        func(g *prose.Group[*v1alpha1.Wormhole]) {
            g.Step("open-tunnel", openTunnel)

            g.When("downstream traffic is requested",
                prose.Match[*v1alpha1.Wormhole](
                    gomega.HaveField("Spec.Throughput", gomega.BeNumerically(">", 0))),
                func(g *prose.Group[*v1alpha1.Wormhole]) {
                    g.Step("route-traffic", routeTraffic)
                })
        })
})
```

Each gate scopes the steps under it, and the nesting in your code is the nesting in your trace. The inner `route-traffic` step runs only when both gates are open: the tunnel is charged, and there's throughput requested. Because gates are lexical and execute in declaration order every time, you read the conditions top to bottom and know exactly what runs when. There's no hidden tree-building and no reordering.

## Verify it

Set a field on a CR that flips a gate, apply it, and watch the wide event. A closed gate leaves its steps out of the record entirely, so the absence is the signal:

::: terminal

```text
# scale to zero so the "scaled up" gate is closed
$ kubectl patch memcached sample --type=merge -p '{"spec":{"size":0}}'
$ kubectl -n memcached-system logs deploy/controller-manager | grep 'name=sample' | tail -1
controller=memcached name=sample result=success duration=9ms status.duration=8ms status.outcome=continue
# no deployment.* fields: the gate was closed, so those steps never ran

# scale back up and the gated steps reappear
$ kubectl patch memcached sample --type=merge -p '{"spec":{"size":3}}'
$ kubectl -n memcached-system logs deploy/controller-manager | grep 'name=sample' | tail -1
controller=memcached name=sample result=success duration=41ms scaled-up.deployment.duration=...
```

:::

## Where to go next

[Add teardown with Finalize](/guides/finalizers) covers the deletion-mode counterpart to a `Skip()` gate. [Wire up observability](/guides/observability) explains the wide event you're reading above. For exact predicate signatures, see [`prose` on pkg.go.dev](https://pkg.go.dev/github.com/spechtlabs/prose/pkg/prose).
