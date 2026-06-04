---
title: Add teardown with Finalize
permalink: /guides/finalizers
createTime: 2026/06/03 12:00:00
---

Your operator creates something outside the cluster: a DNS record, an external lease, a row in some registry. Owned Kubernetes objects clean themselves up through owner references and garbage collection, but anything external won't, and if the CR is deleted before you release it, it leaks. The Kubernetes answer is a finalizer: a string on the object that blocks deletion until you've done your teardown and removed it. Wiring that by hand means adding the finalizer on normal reconciles, detecting the deletion timestamp, running teardown, and removing the finalizer, all guarded so you don't loop or double-release. `prose` models the whole thing as one group.

## `Finalize` is a deletion-mode group

`Finalize(name, fn)` declares a group of steps that run only when the object is being deleted. The framework handles the finalizer mechanics around it:

```go
prose.For[*v1alpha1.Wormhole](mgr).
    Step("configmap", upsertConfigMap).
    Step("deployment", upsertDeployment).
    Finalize("collapse", func(g *prose.Group[*v1alpha1.Wormhole]) {
        g.Step("drain-traffic", drainTraffic)
        g.Step("release-coordinates", releaseCoordinates)
        // the framework removes the finalizer once this group succeeds
    }).
    Complete()
```

On a normal reconcile, the `configmap` and `deployment` steps run and the `collapse` group is skipped. The moment the object's `DeletionTimestamp` is set, that inverts: the normal steps are skipped and the `collapse` group runs instead. Deletion is a distinct reconcile *mode*, not a callback bolted onto the side of the happy path, and it has its own vocabulary so it reads as the separate thing it is.

The steps inside a `Finalize` group are ordinary steps. They get the same typed `rctx`, the same `Set`, the same outcomes and `humane` errors, and they show up in the wide event and the trace exactly like any other step. The only difference is when they run.

## How the framework manages the finalizer

Two pieces of mechanics happen around your `Finalize` group, and both are the kind of thing you'd otherwise hand-write and occasionally get wrong:

**On a normal reconcile, the framework adds the finalizer.** As soon as a pipeline has a `Finalize` group, every normal reconcile ensures the finalizer string is present on the object before running the body. That's what guarantees the object can't be hard-deleted out from under you; Kubernetes won't actually remove an object while a finalizer remains.

**Once the `Finalize` group succeeds, the framework removes it.** When the object is being deleted, the `collapse` group runs, and if every step in it returns successfully, the framework strips the finalizer. Removing the last finalizer is what lets Kubernetes complete the deletion. If a step in the group errors or requeues, the finalizer stays put and the object stays around, so the next reconcile retries the teardown. You can't accidentally drop the finalizer while teardown is unfinished, because removal is gated on the group succeeding.

::: tip This is why teardown has to be idempotent
A `Finalize` step may run several times before it finally succeeds: a transient error requeues, the next reconcile runs `collapse` again. Write `releaseCoordinates` so that releasing a coordinate that's already gone is a no-op, not an error. The same idempotency you already need everywhere in a reconciler applies here.
:::

## Where the finalizer name comes from

By default the framework derives the finalizer name from the type, so you don't have to invent and remember a string. When you want control over it, `WithFinalizer` overrides the derived name:

```go
prose.For[*v1alpha1.Wormhole](mgr).
    WithFinalizer("wormhole.example.com/collapse").
    Step("deployment", upsertDeployment).
    Finalize("collapse", func(g *prose.Group[*v1alpha1.Wormhole]) {
        g.Step("release-coordinates", releaseCoordinates)
    }).
    Complete()
```

Reach for `WithFinalizer` when the name matters to something outside your code: another controller that inspects finalizers, an existing convention in your project, or an object that already carries a specific finalizer you're adopting. Picking your own string also means you can change the teardown implementation later without the derived name shifting under you.

## Contrast: the DIY approach

You don't have to use `Finalize`. The escape hatch is open, and a hand-rolled finalizer is a few lines inside an ordinary step:

```go
g.Step("teardown", func(rctx *prose.Context[*v1alpha1.Wormhole]) (prose.Outcome, error) {
    obj := rctx.Object()
    const name = "wormhole.example.com/collapse"

    if obj.GetDeletionTimestamp().IsZero() {
        // not being deleted: make sure the finalizer is present, then carry on
        if !controllerutil.ContainsFinalizer(obj, name) {
            controllerutil.AddFinalizer(obj, name)
            if err := rctx.Client().Update(rctx.Context(), obj); err != nil {
                return prose.Requeue, humane.Wrap(err, "add finalizer")
            }
        }
        return prose.Continue, nil
    }

    // being deleted: do the teardown, then drop the finalizer
    if err := releaseExternal(rctx); err != nil {
        return prose.Requeue, humane.Wrap(err, "release external resource")
    }
    controllerutil.RemoveFinalizer(obj, name)
    if err := rctx.Client().Update(rctx.Context(), obj); err != nil {
        return prose.Requeue, humane.Wrap(err, "remove finalizer")
    }
    return prose.Done, nil
})
```

That works, and `prose` doesn't stop you. Compare it to the `Finalize` version, though: you're now hand-managing the deletion-timestamp branch, the add path, the remove path, and the `Update` calls, and the linear "what this reconcile does" story has a deletion sub-controller wedged into the middle of it. The whole point of `Finalize` is to keep the normal body and the teardown legible as two separate modes rather than one branchy step. Use the DIY form when you genuinely need behavior `Finalize` doesn't model; otherwise the declarative group is the cleaner read.

## Verify it

Apply a CR, confirm the finalizer landed, delete it, and watch the deletion-mode reconcile run `collapse`:

::: terminal

```text
# the framework added the finalizer on the first normal reconcile
$ kubectl get wormhole sample -o jsonpath='{.metadata.finalizers}'
["wormhole.example.com/collapse"]

# delete it: the object sticks around in Terminating while collapse runs
$ kubectl delete wormhole sample &
$ kubectl -n wormhole-system logs deploy/controller-manager | grep 'name=sample' | tail -1
controller=wormhole name=sample result=success collapse.drain-traffic.outcome=continue collapse.release-coordinates.outcome=continue

# once the group succeeded, the finalizer dropped and the object is gone
$ kubectl get wormhole sample
Error from server (NotFound): wormholes.example.com "sample" not found
```

:::

If a teardown step keeps erroring, the object stays in `Terminating` and the wide event shows the failing step's `collapse.<step>.error`, which is exactly where you'd look to find out why a delete is hanging.

## Where to go next

[`DeferErrorCleanup` vs `DeferCleanup`](/guides/cleanup) covers compensation *within* a single reconcile, which is a different problem from teardown on delete. [When the DSL isn't enough](/guides/escape-hatch) goes deeper on the raw-client access the DIY example above uses.
