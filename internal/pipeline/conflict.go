package pipeline

import (
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// maxQuietConflicts is the default streak length a single object may hit an
// optimistic-concurrency conflict before prose stops treating it as an expected,
// transient retry and lets the error propagate loudly. A streak this long is no
// longer "another writer touched the object between our Get and our Update"; it
// points at a real problem — a hot loop fighting itself, a stale cache — that is
// worth a stack trace. Override per controller with Builder.WithConflictTolerance.
const maxQuietConflicts = 3

// conflictTracker counts consecutive conflicts per object key. The runner is shared
// across concurrent reconciles, so access is mutex-guarded; reconciles for a single
// object key are already serialized by controller-runtime, so the per-key value is
// a clean consecutive streak rather than a racy tally.
type conflictTracker struct {
	mu    sync.Mutex
	count map[string]int
}

func newConflictTracker() *conflictTracker {
	return &conflictTracker{count: make(map[string]int)}
}

// inc records one more conflict for key and returns the new streak length.
func (c *conflictTracker) inc(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count[key]++
	return c.count[key]
}

// reset clears the streak for key, called whenever a reconcile ends in anything
// other than a conflict.
func (c *conflictTracker) reset(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.count, key)
}

// resolveConflict turns an optimistic-concurrency conflict into a quiet requeue
// rather than a loud, stack-traced error. Conflicts are expected and transient: the
// next reconcile reads a fresh object and usually succeeds. The per-object streak is
// surfaced in the wide event as conflict.count (so it stays observable without
// writing anything back to the object, which would itself conflict and churn the
// watch) and is reset on any non-conflict outcome. Only once the same object
// conflicts more than maxConflicts times in a row does the error propagate loudly,
// because by then it is no longer a benign race.
//
// It deliberately handles only conflicts. A NotFound is not swallowed here: the
// runner sees the error but not which object it concerns, and a NotFound from a Get
// on some other resource inside a step (a missing referenced dependency) is the
// step's business, not a signal that the reconciled object vanished. Swallowing it
// would mask a misconfiguration as a successful reconcile. The reconciled object
// actually being deleted mid-reconcile self-heals on the next pass, where the
// top-of-loop Get returns NotFound and exits via IgnoreNotFound.
func (r *runner[T]) resolveConflict(rctx *Context[T], key string, outcome Outcome, err error) (Outcome, error) {
	if err != nil && apierrors.IsConflict(err) {
		n := r.conflicts.inc(key)
		rctx.fields.Set("conflict.count", n)
		if n > r.maxConflicts {
			rctx.fields.Set("conflict.exhausted", true)
			return outcome, err // no longer transient: let controller-runtime log it loudly
		}
		return Requeue, nil // quiet retry; the streak is recorded in the wide event
	}

	r.conflicts.reset(key)
	return outcome, err
}
