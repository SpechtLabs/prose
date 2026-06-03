# Running the wormhole demo

These samples deploy one coherent scenario so you can watch the three controllers
reconcile against a real cluster. Nothing here does anything physical; it just
exercises the full prose vocabulary.

## What gets created

- **`gateway-prime`** — a cluster-scoped `SubspaceRelay` with `bandwidth: 100`.
- **`voyager`** (namespace `milkyway`, throughput 60) — reserves coordinates, creates
  two owned `Anchor`s, charges to ignition (visibly, over ~90s), opens a tunnel
  ConfigMap, then routes traffic through `gateway-prime`.
- **`paused-probe`** (namespace `andromeda`, `paused: true`) — short-circuits at the
  `When("paused").Skip()` gate, so its reconcile does nothing.
- **`anchor-sample`** (namespace `default`) — a standalone `Anchor` to watch the
  Anchor controller climb stability on its own.
- **`wormhole-sample`** (namespace `default`, throughput 70) — present in the dir but
  commented out in `kustomization.yaml`. Re-enable it to add a second active wormhole.

Charge and stability climb on wall-clock time, not reconcile frequency: a wormhole
gains 10 charge every 15s (so it ignites around 90s in), and an anchor gains 10
stability every 10s. That is what makes the climb watchable in `kubectl get -w`.

By default only `voyager` (60) is active, which stays under the relay's 100 units, so
`gateway-prime` reports `Online` and `voyager` reaches the `Routing` phase. To watch
the saturation path instead — relay flips to `Saturated`, wormholes back off in
`route-traffic` with a `RequeueAfter` — re-enable `wormhole-sample` (70 + 60 = 130 >
100) or lower the relay's `bandwidth` below 60.

## Run it

From `samples/prose/wormhole-operator/` (with mise active so `kubectl`/`kind` resolve):

```sh
# 1. A throwaway cluster
kind create cluster --name wormhole-demo

# 2. Install the CRDs
make install

# 3. Run the operator locally against the cluster (uses your kubeconfig; leave running)
make run

# 4. In another shell: apply the whole scenario
kubectl apply -k config/samples
```

## Watch it

```sh
# Wormholes charge (note Charge climbing on RequeueAfter), then open
kubectl get wormholes -A -w

# The relay aggregates connected wormholes cluster-wide and flips to Saturated
kubectl get subspacerelays -w

# Anchors created by wormhole-sample, plus the standalone one, climbing to Stable
kubectl get anchors -A

# Owned objects: tunnel ConfigMaps + anchor field-generator Deployments
kubectl get configmaps,deployments -A -l app=anchor

# Transitions a human cares about (TunnelOpened, Saturated, Stable, Draining)
kubectl get events -A --field-selector reason=Saturated
kubectl describe wormhole wormhole-sample
```

Trigger the cross-namespace fan-out by editing the relay and watching every
referencing wormhole re-reconcile:

```sh
kubectl patch subspacerelay gateway-prime --type=merge -p '{"spec":{"bandwidth":200}}'
```

Exercise the deletion mode (drain + release coordinates, then the finalizer drops):

```sh
kubectl delete wormhole voyager -n milkyway
```

## Tear down

```sh
kubectl delete -k config/samples
kind delete cluster --name wormhole-demo
```
