package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestLimiter_AllowsUpToCapacityThenDenies(t *testing.T) {
	l := New(3, 1.0)
	now := time.Unix(1_700_000_000, 0)
	l.SetNowFn(func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("expected allow on call %d", i)
		}
	}
	if l.Allow("k") {
		t.Fatalf("expected deny once capacity exhausted")
	}
}

func TestLimiter_RefillsOverTime(t *testing.T) {
	l := New(2, 1.0)
	now := time.Unix(1_700_000_000, 0)
	l.SetNowFn(func() time.Time { return now })

	l.Allow("k")
	l.Allow("k")
	if l.Allow("k") {
		t.Fatalf("expected deny")
	}

	// One second passes; one token refills.
	now = now.Add(time.Second)
	if !l.Allow("k") {
		t.Fatalf("expected allow after refill")
	}
	if l.Allow("k") {
		t.Fatalf("expected deny after using refilled token")
	}
}

func TestLimiter_KeysAreIsolated(t *testing.T) {
	l := New(1, 0)
	now := time.Unix(1_700_000_000, 0)
	l.SetNowFn(func() time.Time { return now })

	if !l.Allow("a") {
		t.Fatalf("expected allow for a")
	}
	if l.Allow("a") {
		t.Fatalf("expected deny for a")
	}
	if !l.Allow("b") {
		t.Fatalf("expected allow for b (separate key)")
	}
}

func TestLimiter_ConcurrentSafe(t *testing.T) {
	l := New(100, 0)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				l.Allow("shared")
			}
		}()
	}
	wg.Wait()
	// No assertion beyond "did not race / deadlock" — race detector enforces it.
}
