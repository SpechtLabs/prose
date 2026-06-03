package subspace

import (
	"context"
	"testing"
)

func TestReserveAssignsUniqueIDs(t *testing.T) {
	reset()
	a, err := Reserve("Andromeda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := Reserve("Andromeda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.ID == b.ID {
		t.Fatalf("expected unique ids, got %q twice", a.ID)
	}
}

func TestReserveSaturates(t *testing.T) {
	reset()
	for i := 0; i < maxCoordinates; i++ {
		if _, err := Reserve("x"); err != nil {
			t.Fatalf("reserve %d failed early: %v", i, err)
		}
	}
	if _, err := Reserve("x"); err == nil {
		t.Fatal("expected saturation error, got nil")
	}
}

func TestReleaseFreesASlot(t *testing.T) {
	reset()
	var last Coordinate
	for i := 0; i < maxCoordinates; i++ {
		c, err := Reserve("x")
		if err != nil {
			t.Fatalf("reserve %d failed: %v", i, err)
		}
		last = c
	}
	if err := Release(last); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if _, err := Reserve("x"); err != nil {
		t.Fatalf("expected room after release: %v", err)
	}
}

func TestReleaseUnknownIsNoop(t *testing.T) {
	reset()
	if err := Release(Coordinate{ID: "nope"}); err != nil {
		t.Fatalf("expected no-op, got %v", err)
	}
}

func TestDialReturnsClosableSession(t *testing.T) {
	reset()
	l, err := Dial(context.Background())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	if l.SessionID() == "" {
		t.Fatal("expected non-empty session id")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}
