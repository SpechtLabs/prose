---
title: Wire up observability
permalink: /guides/observability
createTime: 2026/06/03 12:00:00
---

You have a pipeline that reconciles. Now you want to see what it does in production: traces when a step is slow, a queryable log line per reconcile, a metric that alerts when a step starts flapping, and a `kubectl describe` event when something a human cares about changes. In a hand-written reconciler that's four kinds of instrumentation interleaved through your business logic. In `prose` you configure the sink once and write none of it inside a step.

## Configure the sink once

`WithObservability` takes a set of options, each of which lights up one output. You pass it on the builder, before the steps, and never touch it again:

```go
prose.For[*cachev1alpa1.Memcached](mgr).
    Owns(&appsv1.Deployment{}).
    Owns(&corev1.Service{}).
    WithObservability(
        prose.Otel(otel.Tracer("memcached")),
        prose.WideEvents(mgr.GetLogger().WithName("memcached")), // one canonical line per reconcile
        prose.Recorder(mgr.GetEventRecorderFor("memcached")),    // kubectl-visible events
    ).
    Describe("dependencies", func(g *prose.Group[*cachev1alpa1.Memcached]) {
        g.Step("deployment", upsertDeployment)
        g.Step("service", upsertService)
    }).
    Step("status", syncStatus).
    Complete()
```

Three options, three outputs. `Otel` wires per-step and per-group spans to a tracer. `WideEvents` emits exactly one structured log line per reconcile. `Recorder` enables `rctx.Event`, the Kubernetes events that show up under `kubectl describe`. Leave one out and that output is simply off; the others keep working, and your steps don't change a line either way.

There's a payoff you get whether or not you wanted it: the logger you'd otherwise thread through every function is derived from the sink, pre-populated with `controller`, `namespace`, and `name`. That's the "pass the logger down through everything" boilerplate, gone.

For exact signatures, see [`prose` on pkg.go.dev](https://pkg.go.dev/github.com/spechtlabs/prose/pkg/prose). The narrative below is the part godoc can't give you.

## Reading the wide event

A reconcile produces one [wide event](https://loggingsucks.com/): one structured record, not one log line per step. The framework accumulates fields as the pipeline runs and emits the whole thing once, at the end. Here's what it looks like:

```text
controller=foo namespace=team-a name=widget generation=7 result=requeue requeue_after=30s duration=412ms
  dependencies.configmap.duration=8ms  dependencies.configmap.outcome=continue
  dependencies.deployment.duration=190ms dependencies.deployment.outcome=continue dependencies.deployment.image=ghcr.io/...:v2
  status.duration=14ms status.outcome=requeue
```

Read it top to bottom and you have the entire reconcile. The header carries the object identity and the overall result. Each step contributes its duration and outcome under a dotted key, and the keys mirror your group nesting exactly: `dependencies.deployment.duration` is the `deployment` step inside the `dependencies` group, because that's where it lives in your code. When `upsertDeployment` calls `rctx.Set("deployment.image", ...)`, that value rides along as `dependencies.deployment.image`.

One reconcile, one queryable row. Querying "show me every reconcile of `widget` where the deployment step took longer than 100ms" is a filter on a single record instead of a grep across fifteen lines you then have to correlate by timestamp and request ID.

::: tip Emission is unmissable by construction
The emit runs in a `defer` inside the runner, so no early return, requeue, or error path can skip it. Every reconcile that starts produces exactly one record, including the ones that blow up in the middle.
:::

## One field, three destinations

The single mechanic that makes this work is `rctx.Set`. You call it inside a step to record something worth knowing:

```go
func upsertDeployment(rctx *prose.Context[*cachev1alpa1.Memcached]) (prose.Outcome, error) {
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

That field lands in two places at once: the wide log event at the end of the reconcile, and the current OpenTelemetry span as an attribute. You write `deployment.image` once and it shows up in both your logs and your traces, under the same dotted key. OpenTelemetry isn't a parallel system you maintain alongside logging; it's fed by the same accumulation. Each step is automatically a child span and each group a parent span, so when you open the trace for a slow reconcile, the span tree matches your group structure and `deployment.image` is sitting right on the span where the work happened.

Errors fold into the same record. When a step returns a `humane.Wrap`, the message lands as `<step>.error`, the unwrapped cause as `<step>.cause`, and any advice as `<step>.advice`. The step name becomes the contextual frame automatically, so the error reads `deployment: <cause>` rather than a bare string you have to trace back to a line number.

## Metrics go through a different door

Logs and spans are happy to carry high-cardinality fields; a `deployment.image` of `ghcr.io/...:v2` is fine in a log line and fine on a span. Prometheus is not. A label whose value is an image tag, an object name, or a generation number is a cardinality bomb that will eventually take your metrics backend down.

So metric labels do **not** come from `rctx.Set`. They come from a deliberately bounded path keyed only by `(controller, step, outcome)`. The framework records a histogram, `prose_step_duration_seconds`, per step, and those three labels are the only ones it ever attaches:

```text
prose_step_duration_seconds_bucket{controller="memcached",step="dependencies.deployment",outcome="continue",le="0.1"}
```

That's enough to alert on a step getting slow or to spot one flapping between `continue` and `requeue`, which is precisely what controller-runtime can't show you: it gives you reconcile-level metrics and nothing within a reconcile. You get per-step visibility without ever risking that an arbitrary field key explodes your label cardinality, because the two doors are kept separate in the type system. There is no path from `Set` to a metric label, by design.

::: info Why this is a hard wall, not a guideline
If `Set` fed metrics, the first time someone set a field keyed by object name, your Prometheus would start tracking one time series per object forever. Keeping the door narrow means you can `Set` anything you want, as freely as you'd log it, and never think about cardinality.
:::

## When to emit a Kubernetes event

A Kubernetes `Event` is for the human running `kubectl describe`, not for your observability backend. It can't be folded into the one wide event, and you wouldn't want it to be: events are how an operator tells a person "I just did a thing you'd want to know about." So they stay per-emit and opt-in per step, through `rctx.Event`:

```go
func syncStatus(rctx *prose.Context[*cachev1alpa1.Memcached]) (prose.Outcome, error) {
    // ... list pods, compute names ...
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

Notice where the event fires: after the status actually changed, not on every pass. The `Set("status.nodes", ...)` happens every reconcile because the count is always worth recording in the wide event, but the event fires only when the node set genuinely transitioned. That's the rule. Set a field for anything you'd want to query or trace, which is almost everything; emit an event only for a state transition a human would want to see in `kubectl describe`, which is almost nothing.

Most of your steps will `Set` fields and emit no events at all. The handful that do are marking the moments someone debugging the cluster will be glad to find.

## Verify it

Point your operator at a cluster, change a CR, and check each output:

::: terminal

```text
# the wide event, one line per reconcile
$ kubectl -n memcached-system logs deploy/controller-manager | grep 'controller=memcached'

# the Kubernetes event, only on a real transition
$ kubectl describe memcached sample
  ...
  Normal  NodesUpdated  12s  memcached  now tracking 3 memcached pod(s)

# the metric, three labels and nothing else
$ kubectl -n memcached-system port-forward deploy/controller-manager 8080:8080
$ curl -s localhost:8080/metrics | grep prose_step_duration_seconds
```

:::

If you wired `Otel` to a real tracer, the span tree for that reconcile shows up in your tracing backend with `dependencies` as a parent span over `deployment` and `service`, and your `Set` fields hanging off the spans where you set them.

## Where to go next

[Test a reconciler](/guides/testing) shows how to drive these outputs under envtest. [When the DSL isn't enough](/guides/escape-hatch) covers reaching for the raw client mid-step, as `syncStatus` does above. For the design rationale behind one wide event per reconcile, see [the understanding section](/understanding/observability).
