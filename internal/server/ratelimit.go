package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a simple per-IP in-memory rate limiter using a sliding-window
// counter. It is safe for concurrent use.
type rateLimiter struct {
	mu         sync.RWMutex
	clients    map[string]*clientLimit
	max        int
	window     time.Duration
	trustProxy bool

	stopOnce sync.Once
	stop     chan struct{}
}

type clientLimit struct {
	attempts int
	resetAt  time.Time
}

func newRateLimiter(maxRequests int, window time.Duration, trustProxy bool) *rateLimiter {
	rl := &rateLimiter{
		clients:    make(map[string]*clientLimit),
		max:        maxRequests,
		window:     window,
		trustProxy: trustProxy,
		stop:       make(chan struct{}),
	}
	go rl.sweepLoop()
	return rl
}

// Close terminates the background sweeper. Idempotent.
func (rl *rateLimiter) Close() {
	if rl == nil {
		return
	}
	rl.stopOnce.Do(func() { close(rl.stop) })
}

// sweepLoop evicts entries whose window has elapsed to bound memory usage.
func (rl *rateLimiter) sweepLoop() {
	interval := rl.window
	if interval < time.Minute {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-rl.stop:
			return
		case now := <-t.C:
			rl.mu.Lock()
			for ip, cl := range rl.clients {
				if now.After(cl.resetAt) {
					delete(rl.clients, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// Allow returns true if the given IP has not exceeded the rate limit.
func (rl *rateLimiter) Allow(ip string) bool {
	if rl == nil || rl.max <= 0 {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cl, ok := rl.clients[ip]
	if !ok || now.After(cl.resetAt) {
		rl.clients[ip] = &clientLimit{attempts: 1, resetAt: now.Add(rl.window)}
		return true
	}

	if cl.attempts >= rl.max {
		return false
	}

	cl.attempts++
	return true
}

// RateLimit wraps a handler with per-IP rate limiting.
func RateLimit(rl *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, rl.trustProxy)
			if !rl.Allow(ip) {
				w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the client IP from the request. When trustProxy is true,
// X-Forwarded-For / X-Real-Ip are consulted (DigitalOcean App Platform etc.).
// Otherwise we use RemoteAddr directly so forwarded headers sent by an
// attacker don't bypass per-IP limits.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// Leftmost entry is the originating client when the proxy
			// populates XFF authoritatively.
			if idx := strings.Index(fwd, ","); idx != -1 {
				fwd = strings.TrimSpace(fwd[:idx])
			}
			if fwd != "" {
				return fwd
			}
		}
		if realIP := r.Header.Get("X-Real-Ip"); realIP != "" {
			return realIP
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
