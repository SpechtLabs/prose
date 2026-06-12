# Wormhole Operator: prose

This sample is a deliberately non-trivial Kubernetes operator written with
`prose`. It exists as the framework-native half of a side-by-side comparison with
[`samples/operator-sdk/wormhole-operator`](../../operator-sdk/wormhole-operator).

The operator manages a small fictional domain:

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

The controllers are expressed as prose pipelines: named, ordered steps grouped
around the domain story. The Wormhole controller, for example, reads as:

1. skip paused objects
2. reserve coordinates
3. create entry and exit anchors
4. open a subspace link
5. charge toward ignition
6. open the tunnel
7. route traffic when requested
8. write status
9. on delete, drain and release coordinates

The important comparison point is that `prose` owns the repeated reconcile
machinery at the framework boundary. The controller code names steps and records
domain fields with `rctx.Set`; the framework automatically turns that into:

- one OpenTelemetry root span per reconcile, named `reconcile.<controller>`
- child spans for each group and step
- span attributes from the same fields used in logs
- one structured wide-event log line per reconcile
- per-step duration and outcome fields
- step metrics
- Kubernetes Events through `rctx.Event`
- cleanup and finalizer ordering

In other words, observability is a property of the pipeline structure, not
something each reconciler has to remember to hand-wire.

## Key files

- [Wormhole controller](./internal/controller/wormhole_controller.go)
- [Anchor controller](./internal/controller/anchor_controller.go)
- [SubspaceRelay controller](./internal/controller/subspacerelay_controller.go)
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
- `anchors.reserve-coordinates.duration`
- `anchors.reserve-coordinates.outcome`
- `coordinates.id`
- `charge.level`
- `relay.saturated`
- `status.phase`

Trace export is enabled when either `OTEL_EXPORTER_OTLP_ENDPOINT` or
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set. Without an endpoint, the global
tracer remains a no-op and local `make run` does not try to contact a collector.

Example:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 make run
```

The service name defaults to `wormhole-operator`, and can be overridden with
standard OpenTelemetry environment variables such as `OTEL_SERVICE_NAME` or
`OTEL_RESOURCE_ATTRIBUTES`.

## Cleanup

```sh
kubectl delete -k config/samples
kind delete cluster --name wormhole-demo
```

## Compare with Operator SDK

Read this sample next to
[`samples/operator-sdk/wormhole-operator`](../../operator-sdk/wormhole-operator).
Both implement the same behavior and emit similar telemetry. The difference is
where the work lives: in this sample, the pipeline structure drives the
instrumentation automatically; in the Operator SDK sample, equivalent spans and
wide logs are written explicitly in each reconciler.
