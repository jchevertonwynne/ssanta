// Package ratelimit provides a small in-memory token-bucket rate limiter.
//
// One Limiter holds a separate bucket per key. Buckets fill at refillPerSec up
// to capacity tokens; each Allow call costs one token. Idle buckets are
// reclaimed lazily when their tokens have refilled past capacity.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

type Limiter struct {
	capacity     float64
	refillPerSec float64
	now          func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

// New returns a Limiter that allows up to capacity events per key with a
// refill rate of refillPerSec per second.
func New(capacity int, refillPerSec float64) *Limiter {
	if capacity < 1 {
		capacity = 1
	}
	if refillPerSec < 0 {
		refillPerSec = 0
	}
	return &Limiter{
		capacity:     float64(capacity),
		refillPerSec: refillPerSec,
		now:          time.Now,
		buckets:      make(map[string]*bucket),
	}
}

// SetNowFn is for tests that need a virtual clock.
func (l *Limiter) SetNowFn(fn func() time.Time) { l.now = fn }

// Allow returns true if the caller has a token to spend for the given key.
func (l *Limiter) Allow(key string) bool {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		// First hit: bucket starts full minus the cost of this call.
		l.buckets[key] = &bucket{tokens: l.capacity - 1, last: now}
		return true
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.refillPerSec
		if b.tokens > l.capacity {
			b.tokens = l.capacity
		}
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--

	// Reclaim buckets that have filled back up — keeps the map bounded.
	if b.tokens >= l.capacity-0.0001 {
		delete(l.buckets, key)
	}
	return true
}
