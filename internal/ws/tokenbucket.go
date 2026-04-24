package ws

import (
	"sync"
	"time"
)

// tokenBucket is a single-consumer token bucket used to rate-limit inbound
// WebSocket frames on a per-connection basis. It is cheap (no goroutines, no
// channels) and safe for concurrent use.
type tokenBucket struct {
	mu       sync.Mutex
	capacity float64
	refill   float64 // tokens per second
	tokens   float64
	last     time.Time
	now      func() time.Time
}

func newTokenBucket(capacity, refillPerSecond float64) *tokenBucket {
	if capacity <= 0 {
		capacity = 1
	}
	if refillPerSecond <= 0 {
		refillPerSecond = capacity
	}
	return &tokenBucket{
		capacity: capacity,
		refill:   refillPerSecond,
		tokens:   capacity,
		last:     time.Now(),
		now:      time.Now,
	}
}

// Take consumes a single token. It returns false when the bucket is empty.
func (b *tokenBucket) Take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	if b.last.IsZero() {
		b.last = now
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.refill
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
