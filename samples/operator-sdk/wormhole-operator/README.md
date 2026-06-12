# Wormhole Operator: Operator SDK

This sample is the idiomatic Operator SDK/controller-runtime version of the
fictional Wormhole operator. It is the hand-written counterpart to
[`samples/prose/wormhole-operator`](../../prose/wormhole-operator).

The two samples intentionally manage the same API types and scenario:

- `Wormhole` reserves coordinates, creates two owned `Anchor` resources, charges
  over time, opens a tunnel `ConfigMap`, and optionally routes traffic.
- `Anchor` creates a backing field-generator `Deployment` and tuning `ConfigMap`,
  then climbs toward a target stability.
- `SubspaceRelay` is cluster-scoped and aggregates throughput from every
  `Wormhole` that references it.

Nothing here controls real infrastructure. The fake domain is intentionally rich
enough to exercise common operator concerns: owned resources, status updates,
timed requeues, cross-resource watches, cluster-scoped fan-out, finalizers,
cleanup, Kubernetes Events, trace spans, and structured reconcile logs.

## Why this version matters

This implementation follows the normal Operator SDK shape:

- each controller is a reconciler struct
- each reconciler implements `Reconcile(ctx, req)`
- each reconciler wires watches in `SetupWithManager`
- owned resources are converged with controller-runtime client calls
- finalizers, status updates, requeues, events, and fan-out watches are explicit

The sample also hand-wires observability that `prose` normally injects from the
pipeline structure. A small local helper in
[`internal/controller/observability.go`](./internal/controller/observability.go)
provides:

- one OpenTelemetry root span per reconcile, named `reconcile.<controller>`
- child spans for explicit step blocks such as `reserve-coordinates`, `ignite`,
  `route-traffic`, `survey`, and `status`
- span attributes for recorded domain fields
- `operator_sdk.outcome` attributes on step and reconcile spans
- one structured wide-event log line per fetched reconcile
- per-step duration and outcome fields

This makes the comparison direct: both samples emit similar telemetry, but this
version has to call the helper at every step where the prose version gets that
from the framework.

## Key files

- [Wormhole reconciler](./internal/controller/wormhole_controller.go)
- [Anchor reconciler](./internal/controller/anchor_controller.go)
- [SubspaceRelay reconciler](./internal/controller/subspacerelay_controller.go)
- [Observability helper](./internal/controller/observability.go)
- [Manager wiring](./cmd/main.go)
- [API types](./api/v1alpha1)
- [Demo manifests](./config/samples)

## Running locally

From this directory:

```sh
kind create cluster --name wormhole-demo
make install
make run
```

In another shell:

```sh
kubectl apply -k config/samples
```

Watch the domain converge:

```sh
kubectl get wormholes -A -w
kubectl get anchors -A -w
kubectl get subspacerelays -w
kubectl get events -A --field-selector reason=Saturated
```

The default scenario creates one active wormhole, one paused wormhole, one
standalone anchor, and one relay. The active wormhole charges for roughly 90
seconds before opening. Re-enable `wormhole-sample` in
`config/samples/kustomization.yaml` to push the relay into saturation.

## Observability

Wide logs are emitted through controller-runtime's logger. A Wormhole reconcile
will include fields such as:

- `result`
- `requeue_after`
- `duration`
- `reserve-coordinates.duration`
- `reserve-coordinates.outcome`
- `coordinates.id`
- `charge.level`
- `relay.saturated`
- `status.phase`

Trace export is enabled when either `OTEL_EXPORTER_OTLP_ENDPOINT` or
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set. Without an endpoint, the tracer
provider remains a no-op and local `make run` does not try to contact a
collector.

Example:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 make run
```

The service name defaults to `operator-sdk-wormhole-operator`, and can be
overridden with standard OpenTelemetry environment variables such as
`OTEL_SERVICE_NAME` or `OTEL_RESOURCE_ATTRIBUTES`.

## Cleanup

```sh
kubectl delete -k config/samples
kind delete cluster --name wormhole-demo
```

## Compare with prose

Read this sample next to
[`samples/prose/wormhole-operator`](../../prose/wormhole-operator). Both
implement the same behavior and emit similar spans, wide logs, and Kubernetes
Events. The difference is operational overhead in the controller code: this
sample makes the instrumentation explicit, while the prose sample derives it
from named pipeline steps.
