// Package controller implements the wormhole-operator's three reconcilers as
// prose pipelines: linear, observable sequences of named steps. There is no
// Reconcile method and no reconciler struct; each SetupXWithManager function is
// the whole controller, and the step functions hold only business logic.
package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/gomega"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spechtlabs/prose/pkg/prose"
	"go.opentelemetry.io/otel"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	universev1alpha1 "example.com/prose/wormhole-operator/api/v1alpha1"
	"example.com/prose/wormhole-operator/internal/subspace"
)

const (
	// ignitionThreshold is the charge a wormhole must reach before its tunnel opens.
	ignitionThreshold = 88
	// chargeStep is how much charge a wormhole gains per chargeInterval of real time.
	chargeStep = 10
	// chargeInterval paces the charge climb in wall-clock time so it is observable
	// in `kubectl get` rather than finishing in a single burst of reconciles.
	chargeInterval = 15 * time.Second
)

// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=wormholes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=wormholes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=wormholes/finalizers,verbs=update
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=anchors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=subspacerelays,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// SetupWormholeWithManager builds the Wormhole reconcile pipeline. Read it top to
// bottom and you have the whole story: skip while paused, anchor both ends
// (handing the coordinate back if anchoring fails), then once both ends exist
// open the tunnel when it is charged and route traffic when there is any to
// route. On delete, drain and release; the finalizer drops once that succeeds.
func SetupWormholeWithManager(mgr ctrl.Manager) error {
	_, err := prose.For[*universev1alpha1.Wormhole](mgr).
		// Don't let our own status writes (the charge ticks) re-trigger the reconcile.
		WithPredicates(prose.IgnoreStatusOnlyUpdates()).
		// Tunnel ConfigMap: no status filter — a ConfigMap has no generation, so a
		// data edit would look status-only; we want drift on it to re-reconcile.
		Owns(&corev1.ConfigMap{}).
		// Owns a custom kind, and drop the Anchors' stability-status churn from
		// re-triggering this controller (Anchor has a real status subresource).
		Owns(&universev1alpha1.Anchor{}, builder.WithPredicates(prose.IgnoreStatusOnlyUpdates())).
		Watches(&universev1alpha1.SubspaceRelay{},
			handler.EnqueueRequestsFromMapFunc(mapRelayToWormholes(mgr.GetClient())),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		WithObservability(
			prose.Otel(otel.Tracer("wormhole")),
			prose.WideEvents(mgr.GetLogger().WithName("wormhole")),
			prose.Recorder(mgr.GetEventRecorderFor("wormhole")),
		).
		When("paused", isPaused).Skip().
		Describe("anchors", func(g *prose.Group[*universev1alpha1.Wormhole]) {
			g.Step("reserve-coordinates", reserveCoordinates)
			g.Step("entry-anchor", upsertEntryAnchor)
			g.Step("exit-anchor", upsertExitAnchor)
		}).
		Context("now that both ends exist", func(g *prose.Group[*universev1alpha1.Wormhole]) {
			g.Step("subspace-link", openSubspaceLink)
			g.Step("ignite", chargeUp)

			g.When("charged past the ignition threshold",
				prose.Match[*universev1alpha1.Wormhole](gomega.HaveField("Status.Charge", gomega.BeNumerically(">=", ignitionThreshold))),
				func(g *prose.Group[*universev1alpha1.Wormhole]) {
					g.Step("open-tunnel", openTunnel)

					g.When("downstream traffic is requested",
						prose.Match[*universev1alpha1.Wormhole](gomega.HaveField("Spec.Throughput", gomega.BeNumerically(">", 0))),
						func(g *prose.Group[*universev1alpha1.Wormhole]) {
							g.Step("route-traffic", routeTraffic)
						})
				})
		}).
		Step("status", syncStatus).
		Finalize("collapse", func(g *prose.Group[*universev1alpha1.Wormhole]) {
			g.Step("drain-traffic", drainTraffic)
			g.Step("release-coordinates", releaseCoordinates)
		}).
		Complete()

	return err
}

// isPaused gates the skip. A pure boolean question over the object.
func isPaused(w *universev1alpha1.Wormhole) bool {
	return w.Spec.Paused
}

// mapRelayToWormholes enqueues every Wormhole that routes through the changed
// relay. Because SubspaceRelay is cluster-scoped, this fan-out crosses namespaces.
func mapRelayToWormholes(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		var wormholes universev1alpha1.WormholeList
		if err := c.List(ctx, &wormholes); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for i := range wormholes.Items {
			w := &wormholes.Items[i]
			if w.Spec.RelayRef != obj.GetName() {
				continue
			}
			reqs = append(reqs, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: w.Namespace, Name: w.Name},
			})
		}
		return reqs
	}
}

// reserveCoordinates claims a slot in the subspace registry. If a later anchor
// step fails, the claim has to go back or the next reconcile leaks it; that is
// DeferErrorCleanup, which runs only on the unwind path.
func reserveCoordinates(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	w := rctx.Object()

	if w.Status.Coordinates != "" {
		rctx.Set("coordinates.id", w.Status.Coordinates)
		return prose.Continue, nil
	}

	coord, err := subspace.Reserve(w.Spec.Destination)
	if err != nil {
		return prose.Requeue, humane.Wrap(err, "reserve subspace coordinates",
			"the subspace registry may be saturated; back off and retry")
	}

	w.Status.Coordinates = coord.ID
	rctx.Set("coordinates.id", coord.ID)
	rctx.DeferErrorCleanup(func() error { return subspace.Release(coord) })

	// Persist the reservation immediately. The primary watch re-reconciles on every
	// owned-object change, and the charge step now only writes status when the charge
	// value changes, so without this an early reconcile could re-enter and reserve a
	// second coordinate before the first charge write lands.
	if err := rctx.Client().Status().Update(rctx.Context(), w); err != nil {
		return prose.Requeue, humane.Wrap(err, "persist reserved coordinates",
			"verify the Wormhole CRD has its status subresource enabled")
	}
	return prose.Continue, nil
}

func upsertEntryAnchor(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	return upsertAnchor(rctx, "entry")
}

func upsertExitAnchor(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	return upsertAnchor(rctx, "exit")
}

// upsertAnchor declaratively applies one Anchor CR for the given mouth, stamping
// the reserved coordinates and recording the anchor name into status.
func upsertAnchor(rctx *prose.Context[*universev1alpha1.Wormhole], end string) (prose.Outcome, error) {
	w := rctx.Object()
	name := fmt.Sprintf("%s-%s", w.Name, end)

	anchor := &universev1alpha1.Anchor{
		TypeMeta:   metav1.TypeMeta{APIVersion: universev1alpha1.GroupVersion.String(), Kind: "Anchor"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: w.Namespace},
		Spec: universev1alpha1.AnchorSpec{
			End:             end,
			Coordinates:     w.Status.Coordinates,
			TargetStability: 100,
		},
	}

	rctx.Set("anchor."+end, name)
	if err := rctx.Apply(anchor); err != nil {
		return prose.Requeue, humane.Wrap(err, "apply "+end+" anchor",
			"verify the controller can create Anchor resources in this namespace")
	}

	if end == "entry" {
		w.Status.EntryAnchor = name
	} else {
		w.Status.ExitAnchor = name
	}
	return prose.Continue, nil
}

// openSubspaceLink dials a connection scoped to exactly this reconcile. Closing
// it has no effect any other reconcile can observe, so it is safe to close on
// every pass; that is DeferCleanup, which always runs.
func openSubspaceLink(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	link, err := subspace.Dial(rctx.Context())
	if err != nil {
		return prose.Requeue, humane.Wrap(err, "dial the subspace link",
			"check that the subspace relay is reachable from this cluster")
	}
	rctx.Object().Status.LinkSession = link.SessionID()
	rctx.Set("link.session", link.SessionID())
	rctx.DeferCleanup(func() error { return link.Close() })
	return prose.Continue, nil
}

// chargeUp climbs Status.Charge toward the ignition threshold. While below it the
// step returns RequeueAfter so the wormhole keeps charging on a timer; the nested
// "charged" gate stays shut until Charge reaches the threshold.
func chargeUp(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	w := rctx.Object()

	// Charge is paced by wall-clock time since creation, not by reconcile frequency.
	// The primary watch re-reconciles on every owned-object and status change, so
	// tying the climb to reconciles makes it finish almost instantly. Deriving the
	// target from elapsed time, and only writing when it actually changes, advances
	// charge one chargeStep per chargeInterval — visibly — without a status-update
	// storm (a no-change reconcile writes nothing and so triggers nothing).
	target := int32(time.Since(w.CreationTimestamp.Time)/chargeInterval) * chargeStep
	if target > 100 {
		target = 100
	}

	if target > w.Status.Charge {
		w.Status.Charge = target
		if w.Status.Charge < ignitionThreshold {
			w.Status.Phase = "Charging"
		}
		if err := rctx.Client().Status().Update(rctx.Context(), w); err != nil {
			return prose.Requeue, humane.Wrap(err, "persist wormhole charge",
				"verify the Wormhole CRD has its status subresource enabled")
		}
	}
	rctx.Set("charge.level", w.Status.Charge)

	if w.Status.Charge >= ignitionThreshold {
		return prose.Continue, nil
	}
	return prose.RequeueAfter(chargeInterval), nil
}

// openTunnel applies the tunnel manifest ConfigMap. The TunnelOpened event fires
// only on the actual transition into the open state — open-tunnel runs on every
// post-ignition reconcile, so emitting unconditionally would spam a "this happened"
// event for a steady state. The persisted phase (Open/Routing) tells us we have
// already announced it.
func openTunnel(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	w := rctx.Object()
	firstOpen := w.Status.Phase != "Open" && w.Status.Phase != "Routing"
	w.Status.Phase = "Open"

	if err := rctx.Apply(tunnelConfigMap(w)); err != nil {
		return prose.Requeue, humane.Wrap(err, "apply tunnel manifest",
			"verify the controller can create ConfigMaps in this namespace")
	}

	if firstOpen {
		rctx.Event(corev1.EventTypeNormal, "TunnelOpened",
			"wormhole charged to %d%%; tunnel to %s is open", w.Status.Charge, w.Spec.Destination)
	}
	return prose.Continue, nil
}

// routeTraffic reads the referenced relay through the raw client. A missing relay
// and a saturated relay are both normal "come back later" states, not failures, so
// they back off with RequeueAfter rather than erroring; the Watches on SubspaceRelay
// also re-triggers this reconcile the moment the relay appears or changes.
func routeTraffic(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	w := rctx.Object()

	var relay universev1alpha1.SubspaceRelay
	if err := rctx.Client().Get(rctx.Context(), types.NamespacedName{Name: w.Spec.RelayRef}, &relay); err != nil {
		if apierrors.IsNotFound(err) {
			// The relay does not exist (yet, or was deleted). Wait for it rather than
			// erroring: this is a config/timing state, not a fault.
			rctx.Set("relay.missing", w.Spec.RelayRef)
			rctx.Event(corev1.EventTypeWarning, "RelayMissing",
				"referenced relay %q not found; waiting", w.Spec.RelayRef)
			return prose.RequeueAfter(30 * time.Second), nil
		}
		return prose.Requeue, humane.Wrap(err, "get referenced subspace relay",
			"verify the controller has RBAC to read SubspaceRelays")
	}

	rctx.Set("relay.name", relay.Name)
	rctx.Set("relay.saturated", relay.Status.Saturated)

	if relay.Status.Saturated {
		// The relay can't carry this wormhole's traffic right now. Hold (RequeueAfter,
		// not an error) and reflect it in the phase. Persist + emit only on the
		// transition into Throttled so a sustained hold doesn't churn writes or events;
		// the relay's Watch re-reconciles us the moment it drops out of saturation.
		if w.Status.Phase != "Throttled" {
			w.Status.Phase = "Throttled"
			if err := rctx.Client().Status().Update(rctx.Context(), w); err != nil {
				return prose.Requeue, humane.Wrap(err, "persist throttled status",
					"verify the Wormhole CRD has its status subresource enabled")
			}
			rctx.Event(corev1.EventTypeWarning, "RelaySaturated",
				"relay %s is saturated; holding traffic", relay.Name)
		}
		return prose.RequeueAfter(15 * time.Second), nil
	}

	w.Status.Phase = "Routing"
	rctx.Set("traffic.routed", w.Spec.Throughput)
	return prose.Continue, nil
}

// syncStatus writes the accumulated status back. Always the last non-finalize step.
func syncStatus(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	w := rctx.Object()
	if w.Status.Phase == "" {
		w.Status.Phase = "Pending"
	}
	rctx.Set("status.phase", w.Status.Phase)
	if err := rctx.Client().Status().Update(rctx.Context(), w); err != nil {
		return prose.Requeue, humane.Wrap(err, "update wormhole status",
			"verify the Wormhole CRD has its status subresource enabled")
	}
	return prose.Continue, nil
}

// drainTraffic runs only on the deletion path.
func drainTraffic(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	// On the deletion path only the Finalize group runs, so a status write here would
	// never be persisted; the event is the right signal for a collapsing wormhole.
	rctx.Event(corev1.EventTypeNormal, "Draining", "draining traffic before collapse")
	return prose.Continue, nil
}

// releaseCoordinates hands the reserved coordinate back. The framework removes
// the finalizer once the collapse group succeeds.
func releaseCoordinates(rctx *prose.Context[*universev1alpha1.Wormhole]) (prose.Outcome, error) {
	w := rctx.Object()
	if w.Status.Coordinates == "" {
		return prose.Continue, nil
	}
	if err := subspace.Release(subspace.Coordinate{ID: w.Status.Coordinates}); err != nil {
		return prose.Requeue, humane.Wrap(err, "release subspace coordinates",
			"the subspace registry rejected the release; retry")
	}
	rctx.Set("coordinates.released", w.Status.Coordinates)
	return prose.Continue, nil
}

// tunnelConfigMap builds the desired tunnel manifest. Owner reference is left to
// Apply via the Owns wiring.
func tunnelConfigMap(w *universev1alpha1.Wormhole) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: w.Name + "-tunnel", Namespace: w.Namespace},
		Data: map[string]string{
			"destination": w.Spec.Destination,
			"coordinates": w.Status.Coordinates,
			"throughput":  fmt.Sprintf("%d", w.Spec.Throughput),
		},
	}
}
