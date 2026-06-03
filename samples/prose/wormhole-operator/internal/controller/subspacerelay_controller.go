package controller

import (
	"github.com/onsi/gomega"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spechtlabs/prose/pkg/prose"
	"go.opentelemetry.io/otel"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	universev1alpha1 "example.com/prose/wormhole-operator/api/v1alpha1"
)

// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=subspacerelays,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=subspacerelays/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=subspacerelays/finalizers,verbs=update

// SetupSubspaceRelayWithManager builds the SubspaceRelay pipeline. The relay
// aggregates every Wormhole that references it (cluster-wide), and reports whether
// it is saturated. It carries no finalizer, to show a finalizer is optional. The
// survey returns prose.Done when the relay is Offline, stopping the pipeline
// successfully without touching anything.
func SetupSubspaceRelayWithManager(mgr ctrl.Manager) error {
	_, err := prose.For[*universev1alpha1.SubspaceRelay](mgr).
		// The relay rewrites its own status every survey; don't let that re-trigger.
		WithPredicates(prose.IgnoreStatusOnlyUpdates()).
		WithObservability(
			prose.Otel(otel.Tracer("subspacerelay")),
			prose.WideEvents(mgr.GetLogger().WithName("subspacerelay")),
			prose.Recorder(mgr.GetEventRecorderFor("subspacerelay")),
		).
		Step("survey", surveyConnectedWormholes).
		When("saturated",
			prose.Match[*universev1alpha1.SubspaceRelay](gomega.HaveField("Status.Saturated", gomega.BeTrue())),
			func(g *prose.Group[*universev1alpha1.SubspaceRelay]) {
				g.Step("warn", emitSaturationEvent)
			}).
		Step("status", syncRelayStatus).
		Complete()

	return err
}

// surveyConnectedWormholes lists Wormholes in all namespaces, sums the throughput
// of those that reference this relay, and records whether it is saturated.
func surveyConnectedWormholes(rctx *prose.Context[*universev1alpha1.SubspaceRelay]) (prose.Outcome, error) {
	relay := rctx.Object()

	if relay.Status.Phase == "Offline" {
		rctx.Set("relay.offline", true)
		return prose.Done, nil
	}

	var wormholes universev1alpha1.WormholeList
	if err := rctx.Client().List(rctx.Context(), &wormholes); err != nil {
		return prose.Requeue, humane.Wrap(err, "list wormholes for relay survey",
			"verify the controller has RBAC to list Wormholes cluster-wide")
	}

	var connected, consumed int32
	for i := range wormholes.Items {
		w := &wormholes.Items[i]
		if w.Spec.RelayRef != relay.Name {
			continue
		}
		connected++
		consumed += w.Spec.Throughput
	}

	relay.Status.ConnectedWormholes = connected
	relay.Status.ConsumedBandwidth = consumed
	relay.Status.Saturated = consumed > relay.Spec.Bandwidth

	rctx.Set("relay.connected", connected)
	rctx.Set("relay.consumed", consumed)
	rctx.Set("relay.saturated", relay.Status.Saturated)
	return prose.Continue, nil
}

// emitSaturationEvent surfaces saturation to a human at kubectl describe.
func emitSaturationEvent(rctx *prose.Context[*universev1alpha1.SubspaceRelay]) (prose.Outcome, error) {
	relay := rctx.Object()
	rctx.Event(corev1.EventTypeWarning, "Saturated",
		"relay carrying %d/%d bandwidth units", relay.Status.ConsumedBandwidth, relay.Spec.Bandwidth)
	return prose.Continue, nil
}

func syncRelayStatus(rctx *prose.Context[*universev1alpha1.SubspaceRelay]) (prose.Outcome, error) {
	relay := rctx.Object()
	if relay.Status.Saturated {
		relay.Status.Phase = "Saturated"
	} else {
		relay.Status.Phase = "Online"
	}
	if err := rctx.Client().Status().Update(rctx.Context(), relay); err != nil {
		return prose.Requeue, humane.Wrap(err, "update relay status",
			"verify the SubspaceRelay CRD has its status subresource enabled")
	}
	return prose.Continue, nil
}
