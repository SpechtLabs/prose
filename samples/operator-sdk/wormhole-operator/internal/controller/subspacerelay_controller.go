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
	"reflect"

	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	universev1alpha1 "example.com/operator-sdk/wormhole-operator/api/v1alpha1"
)

// SubspaceRelayReconciler reconciles a SubspaceRelay object.
type SubspaceRelayReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Tracer   trace.Tracer
}

// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=subspacerelays,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=subspacerelays/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=subspacerelays/finalizers,verbs=update
// +kubebuilder:rbac:groups=universe.specht-labs.de,resources=wormholes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile surveys all Wormholes that reference this cluster-scoped relay and
// reflects aggregate bandwidth usage into the relay status.
func (r *SubspaceRelayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	log := logf.FromContext(ctx)

	relay := &universev1alpha1.SubspaceRelay{}
	if err = r.Get(ctx, req.NamespacedName, relay); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("SubspaceRelay resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get SubspaceRelay")
		return ctrl.Result{}, err
	}

	ctx, obs := beginReconcile(ctx, log, r.Tracer, "subspacerelay", req, relay.Generation)
	outcome := ""
	defer func() {
		obs.Finish(outcome, result, err)
	}()

	if relay.Status.Phase == "Offline" {
		obs.Set("relay.offline", true)
		outcome = "done"
		return ctrl.Result{}, nil
	}

	originalStatus := relay.Status
	_, err = obs.Step(ctx, "survey", func(ctx context.Context) (string, error) {
		var wormholes universev1alpha1.WormholeList
		if err := r.List(ctx, &wormholes); err != nil {
			return "requeue", err
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
		if relay.Status.Saturated {
			relay.Status.Phase = "Saturated"
		} else {
			relay.Status.Phase = "Online"
		}

		obs.Set("relay.connected", connected)
		obs.Set("relay.consumed", consumed)
		obs.Set("relay.saturated", relay.Status.Saturated)
		return "continue", nil
	})
	if err != nil {
		log.Error(err, "Failed to list Wormholes for relay survey")
		return ctrl.Result{}, err
	}

	if relay.Status.Saturated {
		_, err = obs.Step(ctx, "warn", func(ctx context.Context) (string, error) {
			if !originalStatus.Saturated && r.Recorder != nil {
				r.Recorder.Eventf(relay, corev1.EventTypeWarning, "Saturated",
					"relay carrying %d/%d bandwidth units", relay.Status.ConsumedBandwidth, relay.Spec.Bandwidth)
			}
			return "continue", nil
		})
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	_, err = obs.Step(ctx, "status", func(ctx context.Context) (string, error) {
		obs.Set("status.phase", relay.Status.Phase)
		if !reflect.DeepEqual(originalStatus, relay.Status) {
			if err := r.Status().Update(ctx, relay); err != nil {
				return "requeue", err
			}
		}
		return "continue", nil
	})
	if err != nil {
		log.Error(err, "Failed to update SubspaceRelay status")
		return ctrl.Result{}, err
	}

	outcome = "done"
	return ctrl.Result{}, nil
}

// mapWormholeToRelay maps a changed Wormhole to a reconcile of the relay it
// references. SubspaceRelay is cluster-scoped, so the request has no namespace.
func mapWormholeToRelay(_ context.Context, obj client.Object) []reconcile.Request {
	w, ok := obj.(*universev1alpha1.Wormhole)
	if !ok || w.Spec.RelayRef == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: w.Spec.RelayRef}}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubspaceRelayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&universev1alpha1.SubspaceRelay{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&universev1alpha1.Wormhole{},
			handler.EnqueueRequestsFromMapFunc(mapWormholeToRelay),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Named("subspacerelay").
		Complete(r)
}
