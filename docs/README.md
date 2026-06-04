---
pageLayout: home
externalLinkIcon: false

config:
  - type: doc-hero
    hero:
      name: Kubernetes operators that read like prose.
      text: A thin DSL over controller-runtime
      tagline: Describe what a reconcile does as a sequence of named steps. prose handles the boilerplate and gives you tracing, wide-event logging, metrics, and Kubernetes events for free.
      image: /logo.png
      actions:
        - text: Your First Reconciler →
          link: /getting-started/quick
          theme: brand
          icon: mdi:flash
        - text: Read the Overview →
          link: /getting-started/overview
          theme: alt
          icon: mdi:book-open-page-variant

  - type: features
    title: Why prose?
    description: A reconcile is a single observable transaction. Every other decision falls out of that one sentence.
    features:
      - title: Linear and explicit
        icon: mdi:format-list-numbered
        details: One sentence per thing the operator does. No Reconcile method to write, no SetupWithManager to wire. What runs, and when, is what you read top to bottom.

      - title: Observability is the boundary
        icon: mdi:telescope
        details: Steps set fields and return outcomes; the framework turns that into one wide event, OpenTelemetry spans, per-step metrics, and Kubernetes events, without a single telemetry call in your business logic.

      - title: Requeue is a result
        icon: mdi:directions-fork
        details: Come back in 30s is a normal reconcile result, never smuggled into an error type. Backoff stays a first-class signal.

      - title: Every hard case has a door out
        icon: mdi:door-open
        details: Watches, custom predicates, and raw client access are designed in, not bolted on. The DSL never traps you when the operator gets interesting.

  - type: VPListCompare
    title: "Raw controller-runtime vs. prose"
    description: "Same reconcile, minus the noise that buries the intent."
    left:
      title: "Raw controller-runtime"
      description: "The interesting logic, buried in plumbing"
      items:
        - title: "Boilerplate prologue"
          description: "Every reconciler opens with the same Get + IgnoreNotFound and pause/finalizer gating"
        - title: "Scattered observability"
          description: "Log lines, span annotations, and metric increments interleaved through the logic"
        - title: "Implicit control flow"
          description: "Requeue smuggled into error types; early returns that skip half the logic"
        - title: "Blind spots"
          description: "Reconcile-level metrics only; a slow or flapping step is invisible"
        - title: "Repeated wiring"
          description: "Owns / Watches / predicates re-assembled by hand in every SetupWithManager"

    right:
      title: "prose"
      description: "The intent, on the page"
      items:
        - title: "Framework-owned prologue"
          description: "The Get, the requeue plumbing, and the setup wiring are handled for you"
        - title: "One wide event"
          description: "Every field, fed once, emitted unmissably at the transaction boundary"
        - title: "Explicit outcomes"
          description: "Continue / Requeue / RequeueAfter / Done; backoff is a result, not an error"
        - title: "Per-step telemetry"
          description: "A span, a duration, and a metric for every step, for free"
        - title: "One sentence per step"
          description: "The builder chain is the controller: what it watches, the order of work, where telemetry goes"

  - type: custom

  - type: VPReleases
    repo: SpechtLabs/prose

  - type: VPContributors
    repo: SpechtLabs/prose
---

## One sentence per thing the operator does

There is no `Reconcile` method to write and no `SetupWithManager` to wire up. You describe the reconcile as a linear, observable sequence of named steps, and prose handles the boilerplate around them.

```go
prose.For[*v1alpha1.Foo](mgr).
    Owns(&appsv1.Deployment{}).
    WithObservability(
        prose.Otel(tracer),
        prose.WideEvents(logger),
        prose.Recorder(mgr.GetEventRecorderFor("foo")),
    ).
    When("paused", isPaused).Skip().
    Describe("dependencies", func(g *prose.Group[*v1alpha1.Foo]) {
        g.Step("configmap", upsertConfigMap)
        g.Step("deployment", upsertDeployment)
    }).
    Step("status", syncStatus).
    Complete()
```

A step holds only business logic. It contributes fields with `rctx.Set` and returns an outcome; it never logs, traces, or counts.

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

When the reconcile returns, the framework emits exactly one structured record describing everything that happened, flattened into dotted keys that mirror your group nesting:

::: terminal one wide event per reconcile

```text
controller=foo namespace=team-a name=widget generation=7 result=requeue requeue_after=30s duration=412ms
  dependencies.configmap.duration=8ms  dependencies.configmap.outcome=continue
  dependencies.deployment.duration=190ms dependencies.deployment.outcome=continue dependencies.deployment.image=ghcr.io/...:v2
  status.duration=14ms status.outcome=requeue
```

:::

One reconcile, one queryable row. The same `rctx.Set` call also lands on the OpenTelemetry span, so you write the field once and it shows up in both your logs and your traces.

::: tip Ready to build one?
The [five-minute guide](/getting-started/quick) gets you from `prose.For` to a running reconciler. If you want the why first, start with [the mental model](/understanding/mental-model).
:::

::: info Early and moving
prose is early and the API surface is still settling. The concepts are stable; signatures may change before a tagged release. Signatures live on [pkg.go.dev](https://pkg.go.dev/github.com/spechtlabs/prose/pkg/prose); these docs are the narrative around them.
:::
