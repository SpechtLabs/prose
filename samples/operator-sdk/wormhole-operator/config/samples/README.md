# Wormhole Demo Manifests: Operator SDK

These manifests run one coherent scenario for the Operator SDK wormhole
operator. The scenario is the same as the prose sample so you can compare
behavior, logs, spans, events, and status transitions directly.

## What gets created

- `gateway-prime`: a cluster-scoped `SubspaceRelay` with `bandwidth: 100`
- `voyager`: an active `Wormhole` in namespace `milkyway`, with throughput `60`
- `paused-probe`: a paused `Wormhole` in namespace `andromeda`
- `anchor-sample`: a standalone `Anchor` in namespace `default`
- `wormhole-sample`: a second `Wormhole` manifest, commented out in
  `kustomization.yaml` by default

`voyager` reserves coordinates, creates two owned anchors, charges over roughly
90 seconds, opens a tunnel `ConfigMap`, and routes traffic through
`gateway-prime`. `anchor-sample` climbs stability independently so the Anchor
controller can be observed without going through a Wormhole.

By default the relay stays under capacity. Re-enable `wormhole-sample` or lower
the relay bandwidth to exercise saturation.

## Run

From `samples/operator-sdk/wormhole-operator`:

```sh
kind create cluster --name wormhole-demo
make install
make run
```

In another shell:

```sh
kubectl apply -k config/samples
```

## Watch

```sh
kubectl get wormholes -A -w
kubectl get anchors -A -w
kubectl get subspacerelays -w
kubectl get configmaps,deployments -A
kubectl get events -A
```

Useful interactions:

```sh
# Trigger relay fan-out and watch referencing wormholes reconcile again.
kubectl patch subspacerelay gateway-prime --type=merge -p '{"spec":{"bandwidth":200}}'

# Exercise the finalizer path: drain, release coordinates, then remove finalizer.
kubectl delete wormhole voyager -n milkyway
```

## Cleanup

```sh
kubectl delete -k config/samples
kind delete cluster --name wormhole-demo
```
