---
title: When the DSL isn't enough
permalink: /guides/escape-hatch
createTime: 2026/06/03 12:00:00
---

A DSL that builds the whole reconciler is a pleasure for CRUD-shaped operators and a straitjacket the first time you need a non-obvious watch, a custom predicate, or raw client access in the middle of a step. Those are exactly the operators where saving boilerplate matters most, so in `prose` the escape hatch is designed in, not bolted on. The design test the framework holds itself to isn't "does the happy path read well." It's "can I express a `MapFunc` watch and a custom predicate without leaving the DSL." If every hard case has a clean door out, the DSL is a strict win rather than a trap you grow out of.

## A `Watches` with a MapFunc

`Owns` covers the common case: watch an owned object, enqueue its owner. When you need to react to an object that *isn't* owned, you map it back to the CRs that care about it yourself, with `handler.EnqueueRequestsFromMapFunc`. `prose` exposes `Watches` fully, so this lives in the builder chain alongside everything else:

```go
prose.For[*cachev1alpa1.Memcached](mgr).
    Owns(&appsv1.Deployment{}).
    Watches(
        &corev1.ConfigMap{},
        handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
            // a shared ConfigMap changed; enqueue every Memcached that references it
            cm := obj.(*corev1.ConfigMap)
            var list cachev1alpa1.MemcachedList
            if err := mgr.GetClient().List(ctx, &list,
                client.InNamespace(cm.Namespace)); err != nil {
                return nil
            }
            var reqs []reconcile.Request
            for _, m := range list.Items {
                if m.Spec.ConfigRef == cm.Name {
                    reqs = append(reqs, reconcile.Request{
                        NamespacedName: client.ObjectKeyFromObject(&m),
                    })
                }
            }
            return reqs
        }),
    ).
    Describe("dependencies", func(g *prose.Group[*cachev1alpa1.Memcached]) {
        g.Step("deployment", upsertDeployment)
    }).
    Step("status", syncStatus).
    Complete()
```

That's the full controller-runtime `Watches` surface, with nothing hidden. The framework doesn't handle only the easy 80% of triggering and abandon you for the 20% that's the actual reason the operator is hard.

## Custom predicates and `IgnoreStatusOnlyUpdates`

`Watches` takes the usual variadic options, so you can attach a `builder.WithPredicates` filter to a watch and cut the events that would otherwise wake your reconciler for nothing. For the primary watch, `prose` ships one predicate worth knowing by name, passed through `WithPredicates`:

```go
prose.For[*cachev1alpa1.Memcached](mgr).
    WithPredicates(prose.IgnoreStatusOnlyUpdates()).
    Describe("dependencies", func(g *prose.Group[*cachev1alpa1.Memcached]) {
        g.Step("deployment", upsertDeployment)
    }).
    Step("status", syncStatus).
    Complete()
```

`IgnoreStatusOnlyUpdates` solves a self-inflicted churn problem: your `syncStatus` step writes to the object's status, that status write is an update event, and the update event wakes your reconciler, which runs `syncStatus` again. `IgnoreStatusOnlyUpdates` skips an update when only the status, or only the `resourceVersion`, changed, so the controller stops reacting to its own status writes, while still reconciling on spec changes, creation, deletion, and finalizer, label, or annotation changes.

::: warning Don't reach for GenerationChangedPredicate here
A bare `predicate.GenerationChangedPredicate` looks like it does the same job, but it drops the update that *sets* the deletion timestamp, because setting `DeletionTimestamp` doesn't bump the generation. That silently breaks `Finalize`: your teardown never runs. `IgnoreStatusOnlyUpdates` is deletion-safe by design and is the predicate you want on a pipeline that has a `Finalize` group.
:::

## `Complete()` returns the builder

When `prose` doesn't model something at all, you drop to raw controller-runtime. `Complete()` returns the underlying `*builder.TypedBuilder` (or an error), so anything the DSL doesn't cover is one method call away:

```go
b, err := prose.For[*cachev1alpa1.Memcached](mgr).
    Describe("dependencies", func(g *prose.Group[*cachev1alpa1.Memcached]) {
        g.Step("deployment", upsertDeployment)
    }).
    Complete()
if err != nil {
    return err
}

// drop to raw controller-runtime for anything prose doesn't model
return b.
    WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
    Complete( /* ... */ )
```

A custom rate limiter, `MaxConcurrentReconciles`, leader-election-aware setup: none of it needs `prose` to grow an option for it, because the builder you'd use without `prose` is right there.

## Raw client access mid-pipeline

The most common escape isn't in setup at all; it's inside a step. `rctx.Apply` covers declarative upserts, but plenty of real work isn't an upsert: listing by label selector, a `Status().Update`, a subresource call. For those, `rctx` hands you the raw `client.Client`, the `context.Context`, and the typed object, so no step is ever trapped by the DSL. The memcached `syncStatus` step does exactly this:

```go
func syncStatus(rctx *prose.Context[*cachev1alpa1.Memcached]) (prose.Outcome, error) {
    m := rctx.Object()

    pods := &corev1.PodList{}
    err := rctx.Client().List(rctx.Context(), pods,
        client.InNamespace(m.Namespace),
        client.MatchingLabels(labelsForMemcached(m.Name)),
    )
    if err != nil {
        return prose.Requeue, humane.Wrap(err, "list memcached pods",
            "verify the controller has RBAC to list Pods in this namespace")
    }

    names := getPodNames(pods.Items)
    rctx.Set("status.nodes", len(names))

    if reflect.DeepEqual(names, m.Status.Nodes) {
        return prose.Continue, nil
    }

    m.Status.Nodes = names
    if err := rctx.Client().Status().Update(rctx.Context(), m); err != nil {
        return prose.Requeue, humane.Wrap(err, "update memcached status",
            "verify the Memcached CRD has its status subresource enabled")
    }

    rctx.Event(corev1.EventTypeNormal, "NodesUpdated", "now tracking %d memcached pod(s)", len(names))
    return prose.Continue, nil
}
```

Listing pods by label is outside what `Apply` models, so the step reaches for `rctx.Client()` and `rctx.Context()` inline. It's still a normal step: it sets `status.nodes`, returns an outcome, wraps its errors with `humane`, and shows up in the wide event and the trace like any other. Dropping to the raw client didn't cost you the observability or pull you out of the pipeline; it's the documented escape hatch, used in place without leaving the DSL.

::: tip The hatch is a feature of every step, not a special mode
`rctx.Client()`, `rctx.Context()`, and `rctx.Object()` are always available. A step that does one declarative `Apply` and a step that runs three raw client calls are the same kind of thing from the framework's point of view; both get the same span, duration, and field accumulation.
:::

## Verify it

Trigger the watch and confirm the mapped reconcile fired; check that the status-only predicate is cutting the self-induced churn:

::: terminal

```text
# touch the shared ConfigMap and watch the MapFunc enqueue the referencing CRs
$ kubectl patch configmap shared-config --type=merge -p '{"data":{"tuning":"v2"}}'
$ kubectl -n memcached-system logs deploy/controller-manager | grep 'name=sample' | tail -1
controller=memcached name=sample result=success dependencies.deployment.outcome=continue ...

# with IgnoreStatusOnlyUpdates, a status write does NOT re-trigger a reconcile loop:
# you should see one reconcile per spec change, not a burst after each status update
```

:::

## Where to go next

[Add teardown with Finalize](/guides/finalizers) is the high-level alternative to hand-rolling deletion via the raw client. [Test a reconciler](/guides/testing) drives a watch like the one above under envtest. For exact `Watches` and predicate signatures, see [`prose` on pkg.go.dev](https://pkg.go.dev/github.com/spechtlabs/prose/pkg/prose).
