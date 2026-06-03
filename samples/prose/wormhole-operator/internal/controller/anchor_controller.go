package controller

import (
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spechtlabs/prose/pkg/prose"
	"go.opentelemetry.io/otel"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"

	universev1alpha1 "example.com/prose/wormhole-operator/api/v1alpha1"
)

const (
	// stabilityStep is how much stability an anchor gains per stabilityInterval.
	stabilityStep = 10
	// stabilityInterval paces the stability climb in wall-clock time so it is
	// observable in `kubectl get` instead of snapping to the target instantly.
	stabilityInterval = 10 * time.Second
)

// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=anchors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=anchors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=anchors/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

// SetupAnchorWithManager builds the Anchor pipeline. An anchor converges a
// backing "field generator" Deployment plus a tuning ConfigMap, then climbs its
// stability toward the target on a timer. The "still forming" gate uses a plain
// predicate (not a matcher) so it can compare two fields of the object.
func SetupAnchorWithManager(mgr ctrl.Manager) error {
	_, err := prose.For[*universev1alpha1.Anchor](mgr).
		// Skip our own stability-status writes, and the backing Deployment's status
		// churn, from re-triggering this reconcile.
		WithPredicates(prose.IgnoreStatusOnlyUpdates()).
		Owns(&appsv1.Deployment{}, builder.WithPredicates(prose.IgnoreStatusOnlyUpdates())).
		WithObservability(
			prose.Otel(otel.Tracer("anchor")),
			prose.WideEvents(mgr.GetLogger().WithName("anchor")),
			prose.Recorder(mgr.GetEventRecorderFor("anchor")),
		).
		Describe("field-generator", func(g *prose.Group[*universev1alpha1.Anchor]) {
			g.Step("configmap", upsertAnchorConfigMap)
			g.Step("deployment", upsertAnchorDeployment)
		}).
		When("still forming", stillForming, func(g *prose.Group[*universev1alpha1.Anchor]) {
			g.Step("charge-stability", climbStability)
		}).
		Step("status", syncAnchorStatus).
		Complete()

	return err
}

// stillForming is true while the anchor has not reached its target stability.
func stillForming(a *universev1alpha1.Anchor) bool {
	return a.Status.Stability < a.Spec.TargetStability
}

func upsertAnchorConfigMap(rctx *prose.Context[*universev1alpha1.Anchor]) (prose.Outcome, error) {
	a := rctx.Object()
	cm := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: a.Name + "-tuning", Namespace: a.Namespace},
		Data:       map[string]string{"end": a.Spec.End, "coordinates": a.Spec.Coordinates},
	}
	if err := rctx.Apply(cm); err != nil {
		return prose.Requeue, humane.Wrap(err, "apply anchor tuning configmap",
			"verify the controller can create ConfigMaps in this namespace")
	}
	return prose.Continue, nil
}

func upsertAnchorDeployment(rctx *prose.Context[*universev1alpha1.Anchor]) (prose.Outcome, error) {
	a := rctx.Object()
	if err := rctx.Apply(anchorDeployment(a)); err != nil {
		return prose.Requeue, humane.Wrap(err, "apply anchor field generator",
			"verify the controller can create Deployments in this namespace")
	}
	rctx.Set("anchor.end", a.Spec.End)
	return prose.Continue, nil
}

// climbStability raises stability toward the target, returning RequeueAfter until
// it lands. It emits a Stable event on the final step.
func climbStability(rctx *prose.Context[*universev1alpha1.Anchor]) (prose.Outcome, error) {
	a := rctx.Object()

	// Like the wormhole charge, stability is paced by wall-clock time since creation
	// and only written when the value changes, so it climbs one stabilityStep per
	// stabilityInterval in `kubectl get` rather than snapping to the target in a
	// burst of reconciles triggered by the owned Deployment.
	target := int32(time.Since(a.CreationTimestamp.Time)/stabilityInterval) * stabilityStep
	if target > a.Spec.TargetStability {
		target = a.Spec.TargetStability
	}

	if target > a.Status.Stability {
		a.Status.Stability = target
		if a.Status.Stability >= a.Spec.TargetStability {
			a.Status.Phase = "Stable"
		} else {
			a.Status.Phase = "Forming"
		}
		if err := rctx.Client().Status().Update(rctx.Context(), a); err != nil {
			return prose.Requeue, humane.Wrap(err, "persist anchor stability",
				"verify the Anchor CRD has its status subresource enabled")
		}
		if a.Status.Stability >= a.Spec.TargetStability {
			rctx.Event(corev1.EventTypeNormal, "Stable", "anchor reached %d%% stability", a.Status.Stability)
		}
	}
	rctx.Set("stability", a.Status.Stability)

	if a.Status.Stability >= a.Spec.TargetStability {
		return prose.Continue, nil
	}
	return prose.RequeueAfter(stabilityInterval), nil
}

func syncAnchorStatus(rctx *prose.Context[*universev1alpha1.Anchor]) (prose.Outcome, error) {
	a := rctx.Object()
	if a.Status.Phase == "" {
		a.Status.Phase = "Forming"
	}
	if err := rctx.Client().Status().Update(rctx.Context(), a); err != nil {
		return prose.Requeue, humane.Wrap(err, "update anchor status",
			"verify the Anchor CRD has its status subresource enabled")
	}
	return prose.Continue, nil
}

// anchorDeployment builds the desired field-generator Deployment. It uses the
// pause image because the pod does nothing; the anchor only needs something to own.
func anchorDeployment(a *universev1alpha1.Anchor) *appsv1.Deployment {
	replicas := int32(1)
	ls := map[string]string{"app": "anchor", "anchor": a.Name}
	return &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: a.Name, Namespace: a.Namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: ls},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: ls},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:    "field-generator",
					Image:   "registry.k8s.io/pause:3.9",
					Command: []string{"/pause"},
				}}},
			},
		},
	}
}
