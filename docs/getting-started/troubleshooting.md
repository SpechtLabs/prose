---
title: Troubleshooting and Next Steps
permalink: /getting-started/troubleshooting
createTime: 2026/06/03 12:00:00
---

A handful of stumbles catch almost everyone the first time. They're not subtle once you know them; the symptoms just don't always point at the cause. When in doubt, the wide event is where the answer is, so this page ends by pointing you there.

## The type parameter has to be a pointer

`prose.For[T]` takes a `client.Object`, and `client.Object` is satisfied by the *pointer* to your generated type, not the value. Write `prose.For[*v1alpha1.Foo](mgr)`, never `prose.For[v1alpha1.Foo](mgr)`. The same goes everywhere the type parameter reappears: `*prose.Context[*v1alpha1.Foo]`, `*prose.Group[*v1alpha1.Foo]`, `prose.Match[*v1alpha1.Foo](...)`.

The compiler catches it, but the error (`*v1alpha1.Foo does not implement client.Object` or a constraint mismatch) reads like the type is wrong rather than the missing star. Add the `*`.

## Server-side Apply needs TypeMeta set

`rctx.Apply` uses server-side Apply, which is keyed by GroupVersionKind. An object with an empty `TypeMeta` has no GVK, and the apply fails or behaves strangely. Set it on every object you build:

```go
&appsv1.Deployment{
    TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
    // ...
}
```

Owner references are the opposite case: leave them off. `Apply` stamps the controller owner reference for you from the `Owns` wiring, so you don't build it by hand.

## The status subresource must be enabled

Any step that calls `rctx.Client().Status().Update(...)` fails if the CRD doesn't declare its status subresource. The humane errors in the samples say this in their advice string for a reason. Mark the type and regenerate:

```go
// +kubebuilder:subresource:status
```

If you're seeing status writes rejected with something about the subresource not existing, this is almost always it.

## "My step error shows as result=error, but on shutdown it's result=aborted"

These mean different things, and the difference is where the failure came from. A step that returns an error produces `result=error` in the wide event: your business logic failed, the humane message and cause are folded in under `step.<name>.error` and `step.<name>.cause`, and the error propagates to controller-runtime, which requeues with backoff.

`result=aborted` is the reconcile being torn down out from under you, typically because the manager's context was cancelled on shutdown or lease loss. That's not your step failing; it's the run not getting to finish. If you see `aborted` during a rolling restart or a leader-election handover, it's expected and not a bug in your reconciler. If you see `error`, read the cause chain.

## Look at the wide event first

Most "why did my reconcile do that" questions answer themselves from the one structured line `prose` emits per reconcile. It carries the `result` at the head, the per-step `outcome` and `duration`, every field you set, and the error message and cause when a step failed:

::: terminal wide event

```text
controller=foo namespace=team-a name=widget generation=7 result=error duration=88ms
  dependencies.deployment.duration=80ms dependencies.deployment.outcome=error
    dependencies.deployment.error="apply deployment" dependencies.deployment.cause="deployments.apps is forbidden: ..."
```

:::

The `outcome` tells you which step stopped the pipeline, the `error` is the human-readable message, and the `cause` is the unwrapped chain (here, an RBAC denial). Emission runs in a `defer` inside the runner, so no early return, requeue, or panic-free error path can skip it; the line is always there. Before reaching for a debugger, read the row.

## Next steps

You've written a pipeline and you know where to look when one misbehaves. From here:

- The [How-to Guides](/guides/observability) cover wiring OpenTelemetry, the wide-event logger, metrics, and Kubernetes events for a real deployment, plus the watch and predicate patterns the Wormhole sample only sketched.
- [Understanding](/understanding/mental-model) explains *why* the model is shaped this way: the transaction boundary, why requeue is a result and not an error, and why metrics get their own narrow door.
- The [Reference](/reference/api) and [godoc](https://pkg.go.dev/github.com/spechtlabs/prose/pkg/prose) carry the exact signatures for every symbol.
