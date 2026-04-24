package server

import (
	"testing"
	"time"
)

func TestTokenBucket_BurstThenExhaust(t *testing.T) {
	t.Parallel()

	base := time.Unix(1_700_000_000, 0)
	b := newTokenBucket(3, 1)
	b.now = func() time.Time { return base }
	b.last = base

	for i := range 3 {
		if !b.Take() {
			t.Fatalf("take %d: expected allowed", i)
		}
	}
	if b.Take() {
		t.Fatalf("4th take should be denied")
	}
}

func TestTokenBucket_RefillOverTime(t *testing.T) {
	t.Parallel()

	var now time.Time
	b := newTokenBucket(2, 2) // 2 tokens/sec
	b.now = func() time.Time { return now }

	now = time.Unix(1_700_000_000, 0)
	b.last = now

	call1 := b.Take()
	call2 := b.Take()

	if !call1 || !call2 {
		t.Fatalf("burst of 2 should be allowed")
	}
	if b.Take() {
		t.Fatalf("3rd take should be denied at t=0")
	}

	now = now.Add(time.Second) // +2 tokens
	if !b.Take() {
		t.Fatalf("after 1s refill expected token")
	}
	if !b.Take() {
		t.Fatalf("after 1s refill expected second token")
	}
	if b.Take() {
		t.Fatalf("bucket should be empty again")
	}
}
