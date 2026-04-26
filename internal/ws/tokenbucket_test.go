package ws

import (
	"sync"
	"testing"
	"time"
)

func TestTokenBucket_InitialBurst(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	b := newTokenBucket(3, 1)
	b.now = func() time.Time { return now }
	b.last = now

	for i := range 3 {
		if !b.Take() {
			t.Fatalf("expected Take %d to succeed", i+1)
		}
	}
	if b.Take() {
		t.Fatal("expected 4th Take to fail")
	}
}

func TestTokenBucket_RefillsOverTime(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	b := newTokenBucket(3, 1)
	b.now = func() time.Time { return now }
	b.last = now

	// Exhaust bucket
	for range 3 {
		_ = b.Take()
	}
	if b.Take() {
		t.Fatal("expected Take to fail after exhaustion")
	}

	// Advance 2 seconds → 2 tokens refilled
	now = now.Add(2 * time.Second)
	for i := range 2 {
		if !b.Take() {
			t.Fatalf("expected Take %d after refill to succeed", i+1)
		}
	}
	if b.Take() {
		t.Fatal("expected Take to fail after partial refill exhausted")
	}
}

func TestTokenBucket_RefillCapsAtCapacity(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	b := newTokenBucket(2, 10)
	b.now = func() time.Time { return now }
	b.last = now

	// Exhaust bucket
	for range 2 {
		_ = b.Take()
	}

	// Advance 10 seconds → would refill 100 tokens, but cap is 2
	now = now.Add(10 * time.Second)
	for i := range 2 {
		if !b.Take() {
			t.Fatalf("expected Take %d after refill to succeed", i+1)
		}
	}
	if b.Take() {
		t.Fatal("expected Take to fail after capacity exhausted")
	}
}

func TestTokenBucket_DefaultGuards(t *testing.T) {
	t.Parallel()
	b := newTokenBucket(0, 0)
	if b.capacity != 1 {
		t.Fatalf("expected capacity default 1, got %v", b.capacity)
	}
	if b.refill != 1 {
		t.Fatalf("expected refill default 1, got %v", b.refill)
	}
}

func TestTokenBucket_PartialRefill(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	b := newTokenBucket(3, 2)
	b.now = func() time.Time { return now }
	b.last = now

	// Exhaust
	for range 3 {
		_ = b.Take()
	}

	// Advance 0.25s → 0.5 tokens refilled (not enough for 1)
	now = now.Add(250 * time.Millisecond)
	if b.Take() {
		t.Fatal("expected Take to fail with partial token")
	}

	// Advance another 0.25s → 1.0 token total refilled
	now = now.Add(250 * time.Millisecond)
	if !b.Take() {
		t.Fatal("expected Take to succeed after full token refilled")
	}
}

func TestTokenBucket_ConcurrentTakeNoRace(t *testing.T) {
	t.Parallel()
	b := newTokenBucket(100, 100)
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			for range 100 {
				_ = b.Take()
			}
		})
	}
	wg.Wait()
}
