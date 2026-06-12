/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	universev1alpha1 "example.com/operator-sdk/wormhole-operator/api/v1alpha1"
	"example.com/operator-sdk/wormhole-operator/internal/subspace"
)

const (
	// ignitionThreshold is the charge a wormhole must reach before its tunnel opens.
	ignitionThreshold = 88
	// chargeStep is how much charge a wormhole gains per chargeInterval of real time.
	chargeStep = 10
	// chargeInterval paces the charge climb in wall-clock time.
	chargeInterval = 15 * time.Second
	// wormholeFinalizer is removed after subspace coordinates have been released.
	wormholeFinalizer = "universe.specht-labs.de/wormhole-finalizer"
)

// WormholeReconciler reconciles a Wormhole object.
type WormholeReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Tracer   trace.Tracer
}

// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=wormholes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=wormholes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=wormholes/finalizers,verbs=update
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=anchors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=subspacerelays,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile reserves subspace coordinates, creates the two Anchor resources and
// tunnel ConfigMap, advances charge over time, and routes traffic through the
// referenced SubspaceRelay once the tunnel is open.
func (r *WormholeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	log := logf.FromContext(ctx)

	wormhole := &universev1alpha1.Wormhole{}
	if err = r.Get(ctx, req.NamespacedName, wormhole); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Wormhole resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get Wormhole")
		return ctrl.Result{}, err
	}

	ctx, obs := beginReconcile(ctx, log, r.Tracer, "wormhole", req, wormhole.Generation)
	outcome := ""
	defer func() {
		obs.Finish(outcome, result, err)
	}()

	if !wormhole.ObjectMeta.DeletionTimestamp.IsZero() {
		result, err = r.reconcileDelete(ctx, obs, wormhole)
		outcome = resultOutcome(result, err)
		return result, err
	}

	if !controllerutil.ContainsFinalizer(wormhole, wormholeFinalizer) {
		_, err = obs.Step(ctx, "finalizer", func(ctx context.Context) (string, error) {
			controllerutil.AddFinalizer(wormhole, wormholeFinalizer)
			if err := r.Update(ctx, wormhole); err != nil {
				return "requeue", err
			}
			obs.Set("finalizer.added", true)
			return "requeue", nil
		})
		if err != nil {
			log.Error(err, "Failed to add Wormhole finalizer")
			return ctrl.Result{}, err
		}
		result = ctrl.Result{Requeue: true}
		outcome = "requeue"
		return ctrl.Result{Requeue: true}, nil
	}

	if wormhole.Spec.Paused {
		obs.Set("paused", true)
		outcome = "done"
		return ctrl.Result{}, nil
	}

	originalStatus := wormhole.Status

	if wormhole.Status.Coordinates == "" {
		_, err = obs.Step(ctx, "reserve-coordinates", func(ctx context.Context) (string, error) {
			coord, err := subspace.Reserve(wormhole.Spec.Destination)
			if err != nil {
				return "requeue", err
			}
			wormhole.Status.Coordinates = coord.ID
			obs.Set("coordinates.id", coord.ID)
			if err := r.Status().Update(ctx, wormhole); err != nil {
				_ = subspace.Release(coord)
				return "requeue", err
			}
			return "continue", nil
		})
		if err != nil {
			log.Error(err, "Failed to reserve subspace coordinates")
			return ctrl.Result{}, err
		}
		result = ctrl.Result{Requeue: true}
		outcome = "requeue"
		return ctrl.Result{Requeue: true}, nil
	}
	obs.Set("coordinates.id", wormhole.Status.Coordinates)

	_, err = obs.Step(ctx, "entry-anchor", func(ctx context.Context) (string, error) {
		if err := r.reconcileAnchor(ctx, wormhole, "entry"); err != nil {
			return "requeue", err
		}
		wormhole.Status.EntryAnchor = wormhole.Name + "-entry"
		obs.Set("anchor.entry", wormhole.Status.EntryAnchor)
		return "continue", nil
	})
	if err != nil {
		log.Error(err, "Failed to reconcile entry Anchor")
		return ctrl.Result{}, err
	}

	_, err = obs.Step(ctx, "exit-anchor", func(ctx context.Context) (string, error) {
		if err := r.reconcileAnchor(ctx, wormhole, "exit"); err != nil {
			return "requeue", err
		}
		wormhole.Status.ExitAnchor = wormhole.Name + "-exit"
		obs.Set("anchor.exit", wormhole.Status.ExitAnchor)
		return "continue", nil
	})
	if err != nil {
		log.Error(err, "Failed to reconcile exit Anchor")
		return ctrl.Result{}, err
	}

	var link *subspace.Link
	_, err = obs.Step(ctx, "subspace-link", func(ctx context.Context) (string, error) {
		var err error
		link, err = subspace.Dial(ctx)
		if err != nil {
			return "requeue", err
		}
		wormhole.Status.LinkSession = link.SessionID()
		obs.Set("link.session", link.SessionID())
		return "continue", nil
	})
	if err != nil {
		log.Error(err, "Failed to dial subspace link")
		return ctrl.Result{}, err
	}
	defer func() {
		if link != nil {
			if closeErr := link.Close(); closeErr != nil {
				log.Error(closeErr, "Failed to close subspace link")
				obs.Set("subspace-link.cleanup.error", closeErr.Error())
			}
		}
	}()

	requeueAfter := time.Duration(0)
	_, err = obs.Step(ctx, "ignite", func(ctx context.Context) (string, error) {
		target := int32(time.Since(wormhole.CreationTimestamp.Time)/chargeInterval) * chargeStep
		if target > 100 {
			target = 100
		}
		if target > wormhole.Status.Charge {
			wormhole.Status.Charge = target
		}
		obs.Set("charge.level", wormhole.Status.Charge)
		if wormhole.Status.Charge < ignitionThreshold {
			wormhole.Status.Phase = "Charging"
			requeueAfter = chargeInterval
			return "requeue_after", nil
		}
		return "continue", nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	if wormhole.Status.Charge >= ignitionThreshold {
		_, err = obs.Step(ctx, "open-tunnel", func(ctx context.Context) (string, error) {
			if err := r.reconcileTunnelConfigMap(ctx, wormhole); err != nil {
				return "requeue", err
			}
			firstOpen := wormhole.Status.Phase != "Open" && wormhole.Status.Phase != "Routing"
			wormhole.Status.Phase = "Open"
			if firstOpen && r.Recorder != nil {
				r.Recorder.Eventf(wormhole, corev1.EventTypeNormal, "TunnelOpened",
					"wormhole charged to %d%%; tunnel to %s is open", wormhole.Status.Charge, wormhole.Spec.Destination)
			}
			return "continue", nil
		})
		if err != nil {
			log.Error(err, "Failed to reconcile tunnel ConfigMap")
			return ctrl.Result{}, err
		}

		result, err = r.reconcileTraffic(ctx, obs, wormhole)
		if err != nil || result.Requeue || result.RequeueAfter > 0 {
			if updateErr := r.updateWormholeStatus(ctx, wormhole, originalStatus); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			outcome = resultOutcome(result, err)
			return result, err
		}
	}

	if wormhole.Status.Phase == "" {
		wormhole.Status.Phase = "Pending"
	}
	_, err = obs.Step(ctx, "status", func(ctx context.Context) (string, error) {
		obs.Set("status.phase", wormhole.Status.Phase)
		if err := r.updateWormholeStatus(ctx, wormhole, originalStatus); err != nil {
			return "requeue", err
		}
		return "continue", nil
	})
	if err != nil {
		log.Error(err, "Failed to update Wormhole status")
		return ctrl.Result{}, err
	}
	if requeueAfter > 0 {
		result = ctrl.Result{RequeueAfter: requeueAfter}
		outcome = "requeue_after"
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	outcome = "done"
	return ctrl.Result{}, nil
}

func (r *WormholeReconciler) reconcileDelete(ctx context.Context, obs *reconcileObservability, w *universev1alpha1.Wormhole) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(w, wormholeFinalizer) {
		return ctrl.Result{}, nil
	}
	_, err := obs.Step(ctx, "collapse", func(ctx context.Context) (string, error) {
		if r.Recorder != nil {
			r.Recorder.Event(w, corev1.EventTypeNormal, "Draining", "draining traffic before collapse")
		}
		if w.Status.Coordinates != "" {
			if err := subspace.Release(subspace.Coordinate{ID: w.Status.Coordinates}); err != nil {
				return "requeue", err
			}
			obs.Set("coordinates.released", w.Status.Coordinates)
		}
		controllerutil.RemoveFinalizer(w, wormholeFinalizer)
		if err := r.Update(ctx, w); err != nil {
			return "requeue", err
		}
		return "done", nil
	})
	if err != nil {
		log.Error(err, "Failed to collapse Wormhole")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *WormholeReconciler) reconcileAnchor(ctx context.Context, w *universev1alpha1.Wormhole, end string) error {
	anchor := &universev1alpha1.Anchor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      w.Name + "-" + end,
			Namespace: w.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, anchor, func() error {
		anchor.Spec.End = end
		anchor.Spec.Coordinates = w.Status.Coordinates
		anchor.Spec.TargetStability = 100
		return controllerutil.SetControllerReference(w, anchor, r.Scheme)
	})
	return err
}

func (r *WormholeReconciler) reconcileTunnelConfigMap(ctx context.Context, w *universev1alpha1.Wormhole) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      w.Name + "-tunnel",
			Namespace: w.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = map[string]string{
			"destination": w.Spec.Destination,
			"coordinates": w.Status.Coordinates,
			"throughput":  fmt.Sprintf("%d", w.Spec.Throughput),
		}
		return controllerutil.SetControllerReference(w, cm, r.Scheme)
	})
	return err
}

func (r *WormholeReconciler) reconcileTraffic(ctx context.Context, obs *reconcileObservability, w *universev1alpha1.Wormhole) (ctrl.Result, error) {
	if w.Spec.Throughput <= 0 {
		return ctrl.Result{}, nil
	}

	result := ctrl.Result{}
	_, err := obs.Step(ctx, "route-traffic", func(ctx context.Context) (string, error) {
		obs.Set("traffic.requested", w.Spec.Throughput)
		relay := &universev1alpha1.SubspaceRelay{}
		if err := r.Get(ctx, types.NamespacedName{Name: w.Spec.RelayRef}, relay); err != nil {
			if apierrors.IsNotFound(err) {
				obs.Set("relay.missing", w.Spec.RelayRef)
				if r.Recorder != nil {
					r.Recorder.Eventf(w, corev1.EventTypeWarning, "RelayMissing",
						"referenced relay %q not found; waiting", w.Spec.RelayRef)
				}
				result = ctrl.Result{RequeueAfter: 30 * time.Second}
				return "requeue_after", nil
			}
			return "requeue", err
		}
		obs.Set("relay.name", relay.Name)
		obs.Set("relay.saturated", relay.Status.Saturated)
		if relay.Status.Saturated {
			if w.Status.Phase != "Throttled" && r.Recorder != nil {
				r.Recorder.Eventf(w, corev1.EventTypeWarning, "RelaySaturated",
					"relay %s is saturated; holding traffic", relay.Name)
			}
			w.Status.Phase = "Throttled"
			result = ctrl.Result{RequeueAfter: 15 * time.Second}
			return "requeue_after", nil
		}
		w.Status.Phase = "Routing"
		obs.Set("traffic.routed", w.Spec.Throughput)
		return "continue", nil
	})
	return result, err
}

func (r *WormholeReconciler) updateWormholeStatus(ctx context.Context, w *universev1alpha1.Wormhole, original universev1alpha1.WormholeStatus) error {
	if reflect.DeepEqual(original, w.Status) {
		return nil
	}
	return r.Status().Update(ctx, w)
}

// mapRelayToWormholes enqueues every Wormhole that routes through the changed
// cluster-scoped SubspaceRelay.
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

// SetupWithManager sets up the controller with the Manager.
func (r *WormholeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&universev1alpha1.Wormhole{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.ConfigMap{}).
		Owns(&universev1alpha1.Anchor{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&universev1alpha1.SubspaceRelay{},
			handler.EnqueueRequestsFromMapFunc(mapRelayToWormholes(mgr.GetClient())),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Named("wormhole").
		Complete(r)
}
