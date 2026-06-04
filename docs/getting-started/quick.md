---
title: Your First Reconciler
permalink: /getting-started/quick
createTime: 2026/06/03 12:00:00
---

This is a five-minute tutorial. You'll take an empty `SetupWithManager` and turn it into a running `prose` pipeline that converges a Deployment, fronts it with a Service, and syncs the observed pods back into status. The model is the [memcached-operator sample](https://github.com/spechtlabs/prose/tree/main/samples/prose/memcached-operator); if you'd rather read the finished file, it's there.

Assume you've scaffolded a `Memcached` CRD with `Spec.Size` (replica count), `Spec.Image`, and `Status.Nodes` (a `[]string` of pod names). The CRD has its status subresource enabled. Now you'll write the controller.

::: note Before you start
Have [the prerequisites](/getting-started/prerequisites) in place: Go 1.25+, `go get github.com/spechtlabs/prose`, and a working manager.
:::

## 1. Replace the reconciler with a pipeline

Open your controller package. Delete the `Reconcile` method and the reconciler struct if the scaffold generated them; `prose` doesn't need either. Replace the body of `SetupWithManager` with a builder chain:

```go
package controller

import (
    humane "github.com/sierrasoftworks/humane-errors-go"
    "github.com/spechtlabs/prose/pkg/prose"
    "go.opentelemetry.io/otel"

    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
    ctrl "sigs.k8s.io/controller-runtime"

    cachev1alpha1 "example.com/memcached-operator/api/v1alpha1"
)

func SetupWithManager(mgr ctrl.Manager) error {
    _, err := prose.For[*cachev1alpha1.Memcached](mgr).
        Owns(&appsv1.Deployment{}).
        Owns(&corev1.Service{}).
        WithObservability(
            prose.Otel(otel.Tracer("memcached")),
            prose.WideEvents(mgr.GetLogger().WithName("memcached")),
            prose.Recorder(mgr.GetEventRecorderFor("memcached")),
        ).
        Describe("dependencies", func(g *prose.Group[*cachev1alpha1.Memcached]) {
            g.Step("deployment", upsertDeployment)
            g.Step("service", upsertService)
        }).
        Step("status", syncStatus).
        Complete()

    return err
}
```

`prose.For[*cachev1alpha1.Memcached](mgr)` opens the pipeline. The type parameter is a *pointer* to your CRD type; that's the one thing the compiler will bite you on if you get it wrong. `Owns` registers the objects this controller manages, exactly as you'd write in plain controller-runtime, and `WithObservability` wires the three sinks once. `Complete()` returns the underlying `*builder.Builder` and an error; here we keep only the error.

Read the chain top to bottom and it tells you what the controller does: converge the dependencies, then sync status. That's the order it runs.

## 2. Write the deployment step

A step takes a typed `*prose.Context` and returns an outcome plus an error. The object is already fetched and typed; there's no `Get`, no cast, no `IgnoreNotFound`.

```go
func upsertDeployment(rctx *prose.Context[*cachev1alpha1.Memcached]) (prose.Outcome, error) {
    m := rctx.Object()

    rctx.Set("deployment.replicas", m.Spec.Size)
    rctx.Set("deployment.image", m.Spec.Image)

    if err := rctx.Apply(deploymentForMemcached(m)); err != nil {
        return prose.Requeue, humane.Wrap(err, "apply memcached deployment",
            "verify the controller's ServiceAccount can create and update Deployments in this namespace")
    }

    return prose.Continue, nil
}
```

A single server-side `Apply` replaces the old create-if-missing-then-requeue dance. The desired Deployment already encodes the size and the image, so there's nothing left to diff by hand, and the owner reference gets stamped automatically from the `Owns` wiring above.

Notice what the step *doesn't* do. It doesn't log. The two `rctx.Set` calls aren't logging; they contribute fields to the reconcile's wide event and to the span. You write `deployment.image` once and it shows up in both. On failure the step returns `prose.Requeue` with a humane error: the message says what failed, the advice string says how to fix it, and both land in the event.

## 3. Write the service step

The Service exists to show a second owned dependency reconciling under the same group span as the Deployment:

```go
func upsertService(rctx *prose.Context[*cachev1alpha1.Memcached]) (prose.Outcome, error) {
    if err := rctx.Apply(serviceForMemcached(rctx.Object())); err != nil {
        return prose.Requeue, humane.Wrap(err, "apply memcached service",
            "verify the controller's ServiceAccount can create and update Services in this namespace")
    }

    return prose.Continue, nil
}
```

Both steps live inside `Describe("dependencies", ...)`, so in a trace they're sibling child spans under one `dependencies` parent, and in the wide event their fields are flattened under `dependencies.deployment.*` and `dependencies.service.*`.

## 4. Sync status, and emit an event on a real transition

The last step lists the backing pods and reflects them into status. Listing by label selector is outside what `Apply` models, so it reaches for the raw client through `rctx`. That's the escape hatch, used inline without leaving the pipeline:

```go
func syncStatus(rctx *prose.Context[*cachev1alpha1.Memcached]) (prose.Outcome, error) {
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

This step adds two imports the setup block didn't need yet: `reflect` for the `DeepEqual` check, and `sigs.k8s.io/controller-runtime/pkg/client` for `client.InNamespace` and `client.MatchingLabels`. Let your editor add them, or run `goimports`.

The `Status().Update` only runs when the node set actually changed, and the Kubernetes `Event` fires on that same transition. That's the rule for events: emit on a meaningful state change a human watching `kubectl describe` cares about, not on every pass. Most steps emit none.

The helper functions (`deploymentForMemcached`, `serviceForMemcached`, `labelsForMemcached`, `getPodNames`) are plain builders; the [sample file](https://github.com/spechtlabs/prose/blob/main/samples/prose/memcached-operator/internal/controller/memcached_controller.go) has them in full. One detail matters: every object you hand to `Apply` must set its `TypeMeta`, because server-side Apply is keyed by GVK.

## 5. Run it and read the wide event

Wire `SetupWithManager` into your `main.go` as you normally would, then run the manager against a cluster. Create a `Memcached`, and watch one reconcile collapse into a single structured line:

::: terminal manager log

```text
controller=memcached namespace=default name=sample generation=1 result=continue duration=214ms
  dependencies.deployment.duration=120ms dependencies.deployment.outcome=continue
    dependencies.deployment.replicas=3 dependencies.deployment.image=memcached:1.6
  dependencies.service.duration=31ms  dependencies.service.outcome=continue
  status.duration=58ms status.outcome=continue status.nodes=3
```

:::

One reconcile, one queryable row. The dotted keys mirror your group nesting: `dependencies.deployment.image` is the field you set in step 2, sitting right where you'd expect it. If a step had asked for a requeue, you'd see `result=requeue` at the head of the line and the offending step's `outcome=requeue`, with no extra grepping.

You wrote three step functions and one builder chain. There's no `Reconcile` method, no logger threaded through anything, and no tracing or metrics call in your business logic, yet every step has a span, a duration, and a per-step metric.

## Next

You've got the shape of a `prose` pipeline. To see the rest of the vocabulary in one reconcile, the skip gate, nested groups, gated `When` blocks, both cleanup primitives, and the deletion mode, read [The Wormhole Walkthrough](/getting-started/comprehensive). If something didn't behave, [Troubleshooting](/getting-started/troubleshooting) covers the usual first stumbles.
