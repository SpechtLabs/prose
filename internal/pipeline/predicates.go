package pipeline

import (
	"maps"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// IgnoreStatusOnlyUpdates returns a primary-watch predicate that skips an update
// event when only the object's status (or resourceVersion) changed, while still
// reconciling on everything that matters: spec changes (via generation), creation,
// deletion, and the metadata changes that do NOT bump generation — the deletion
// timestamp, finalizers, labels, and annotations.
//
// It is the deletion-safe alternative to predicate.GenerationChangedPredicate. A
// bare generation predicate also drops the metadata-only update that sets the
// deletion timestamp (deletion does not change spec, so generation does not move),
// which means a controller with a Finalize stage would never see the object enter
// deletion and its finalizer would wedge the object forever. By reconciling on a
// deletion-timestamp or finalizer change as well, this predicate keeps Finalize
// working while removing the churn from a controller reacting to its own status
// writes.
//
// It is meant for objects that have a status subresource, where generation tracks
// spec. Do not apply it to an owned ConfigMap or Secret: those have no generation,
// so a change to their data would look status-only and be dropped. The primary CRD
// watch and owned types like a Deployment or another CRD are the right places.
func IgnoreStatusOnlyUpdates() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld == nil || e.ObjectNew == nil {
				return true // malformed event: reconcile rather than silently drop
			}
			o, n := e.ObjectOld, e.ObjectNew
			switch {
			case o.GetGeneration() != n.GetGeneration():
				return true
			case deletionTimestampChanged(o, n):
				return true
			case !slices.Equal(o.GetFinalizers(), n.GetFinalizers()):
				return true
			case !maps.Equal(o.GetLabels(), n.GetLabels()):
				return true
			case !maps.Equal(o.GetAnnotations(), n.GetAnnotations()):
				return true
			default:
				// Generation and every metadata field we care about are unchanged, so
				// the only thing that moved was status (or resourceVersion): drop it.
				return false
			}
		},
		// Create, Delete, and Generic are left nil, so predicate.Funcs returns true
		// for them and prose still reconciles on those events.
	}
}

// deletionTimestampChanged reports whether the object's deletion timestamp was set,
// cleared, or moved between the old and new versions.
func deletionTimestampChanged(o, n metav1.Object) bool {
	od, nd := o.GetDeletionTimestamp(), n.GetDeletionTimestamp()
	switch {
	case od == nil && nd == nil:
		return false
	case od == nil || nd == nil:
		return true
	default:
		return !od.Time.Equal(nd.Time)
	}
}
