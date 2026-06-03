// Package subspace is an in-memory fake of the external "subspace" system the
// wormhole operator pretends to talk to. It has no real effect on anything; it
// exists so the reconcile steps have a believable external dependency to reserve
// coordinates against and dial links through. State is package-global and guarded
// by a mutex; there is no randomness, so behavior is reproducible.
package subspace

import (
	"context"
	"fmt"
	"sync"

	humane "github.com/sierrasoftworks/humane-errors-go"
)

// maxCoordinates caps the registry so the saturation path is reachable in tests
// and demos. Deliberately small.
const maxCoordinates = 64

// Coordinate is a reserved slot in the registry.
type Coordinate struct {
	ID string
}

var (
	mu       sync.Mutex
	counter  int
	reserved = map[string]string{} // id -> destination
)

// reset clears the registry. Test-only helper.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	counter = 0
	reserved = map[string]string{}
}

// Reserve claims a coordinate for destination. It fails once the registry is
// saturated, which is how the demo exercises the error and cleanup paths.
func Reserve(destination string) (Coordinate, error) {
	mu.Lock()
	defer mu.Unlock()

	if len(reserved) >= maxCoordinates {
		return Coordinate{}, humane.New("subspace registry saturated",
			"wait for other wormholes to collapse, or raise the registry cap")
	}

	counter++
	id := fmt.Sprintf("coord-%04d", counter)
	reserved[id] = destination
	return Coordinate{ID: id}, nil
}

// Release returns a coordinate to the registry. Releasing an unknown coordinate
// is a no-op, so cleanup is safe to call more than once.
func Release(c Coordinate) error {
	mu.Lock()
	defer mu.Unlock()
	delete(reserved, c.ID)
	return nil
}

// Link is a per-reconcile connection to the subspace relay. Closing it has no
// effect any other reconcile can observe.
type Link struct {
	session string
	closed  bool
}

// SessionID returns the link's session identifier.
func (l *Link) SessionID() string { return l.session }

// Close tears the link down. Idempotent.
func (l *Link) Close() error {
	l.closed = true
	return nil
}

// Dial opens a Link scoped to one reconcile.
func Dial(_ context.Context) (*Link, error) {
	mu.Lock()
	defer mu.Unlock()
	counter++
	return &Link{session: fmt.Sprintf("sess-%04d", counter)}, nil
}
