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
	mu      sync.RWMutex
	clients map[string]*clientLimit
	max     int
	window  time.Duration
}

type clientLimit struct {
	attempts int
	resetAt  time.Time
}

func newRateLimiter(maxRequests int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		clients: make(map[string]*clientLimit),
		max:     maxRequests,
		window:  window,
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
			ip := clientIP(r)
			if !rl.Allow(ip) {
				w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the client IP from the request, preferring forwarded headers.
func clientIP(r *http.Request) string {
	// Check X-Forwarded-For (may contain multiple IPs, use the first/original)
	fwd := r.Header.Get("X-Forwarded-For")
	if fwd != "" {
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

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
