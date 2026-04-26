package ratelimit

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimiter is a simple per-IP in-memory rate limiter using a sliding-window
// counter. It is safe for concurrent use.
type RateLimiter struct {
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

// Middleware wraps a handler with per-IP rate limiting.
func Middleware(rl *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, rl.trustProxy)
			if !rl.Allow(ip) {
				slog.WarnContext(r.Context(), "rate limit exceeded", "ip", ip, "path", r.URL.Path, "method", r.Method)
				w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// New creates a new RateLimiter and starts its background sweeper.
func New(maxRequests int, window time.Duration, trustProxy bool) *RateLimiter {
	rl := &RateLimiter{
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
func (rl *RateLimiter) Close() {
	if rl == nil {
		return
	}
	rl.stopOnce.Do(func() { close(rl.stop) })
}

// Allow returns true if the given IP has not exceeded the rate limit.
func (rl *RateLimiter) Allow(ip string) bool {
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

// sweepLoop evicts entries whose window has elapsed to bound memory usage.
func (rl *RateLimiter) sweepLoop() {
	interval := max(rl.window, time.Minute)
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

// clientIP extracts the client IP from the request. When trustProxy is true,
// X-Forwarded-For / X-Real-Ip are consulted (DigitalOcean App Platform etc.).
// Otherwise we use RemoteAddr directly so forwarded headers sent by an
// attacker don't bypass per-IP limits.
func clientIP(r *http.Request, trustProxy bool) string {
	//nolint: nestif // idc
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
