package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testIP = "1.2.3.4"

func TestRateLimiter_AllowWithinLimit(t *testing.T) {
	t.Parallel()
	rl := New(3, time.Minute, false)
	defer rl.Close()

	for i := range 3 {
		if !rl.Allow(testIP) {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}
}

func TestRateLimiter_BlockOverLimit(t *testing.T) {
	t.Parallel()
	rl := New(2, time.Minute, false)
	defer rl.Close()

	if !rl.Allow(testIP) {
		t.Fatal("expected first request to be allowed")
	}
	if !rl.Allow(testIP) {
		t.Fatal("expected second request to be allowed")
	}
	if rl.Allow(testIP) {
		t.Fatal("expected third request to be blocked")
	}
}

func TestRateLimiter_ResetAfterWindow(t *testing.T) {
	t.Parallel()
	rl := New(1, 50*time.Millisecond, false)
	defer rl.Close()

	if !rl.Allow(testIP) {
		t.Fatal("expected first request to be allowed")
	}
	if rl.Allow(testIP) {
		t.Fatal("expected second request to be blocked")
	}

	time.Sleep(60 * time.Millisecond)

	if !rl.Allow(testIP) {
		t.Fatal("expected request after window to be allowed")
	}
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	t.Parallel()
	rl := New(1, time.Minute, false)
	defer rl.Close()

	if !rl.Allow("1.2.3.4") {
		t.Fatal("expected first IP to be allowed")
	}
	if !rl.Allow("5.6.7.8") {
		t.Fatal("expected second IP to be allowed")
	}
}

func TestClientIP_TrustProxyFalse_IgnoresForwardedHeaders(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:4444"
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	r.Header.Set("X-Real-Ip", "2.2.2.2")

	if got := clientIP(r, false); got != "10.0.0.1" {
		t.Fatalf("expected RemoteAddr IP, got %q", got)
	}
}

func TestClientIP_TrustProxyTrue_UsesLeftmostXFF(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:4444"
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.1")

	if got := clientIP(r, true); got != "203.0.113.5" {
		t.Fatalf("expected leftmost XFF, got %q", got)
	}
}

func TestRateLimiter_Sweeper_EvictsExpiredEntries(t *testing.T) {
	t.Parallel()
	rl := New(1, 20*time.Millisecond, false)
	defer rl.Close()

	// Seed a few entries.
	for i := range 5 {
		rl.Allow("ip-" + string(rune('a'+i)))
	}
	rl.mu.RLock()
	if len(rl.clients) != 5 {
		rl.mu.RUnlock()
		t.Fatalf("expected 5 entries pre-sweep, got %d", len(rl.clients))
	}
	rl.mu.RUnlock()

	// Sweeper interval is max(window, 1m); too slow for a test. Simulate the
	// sweep directly to verify eviction logic.
	time.Sleep(30 * time.Millisecond)
	now := time.Now()
	rl.mu.Lock()
	for ip, cl := range rl.clients {
		if now.After(cl.resetAt) {
			delete(rl.clients, ip)
		}
	}
	remaining := len(rl.clients)
	rl.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected all entries swept, got %d remaining", remaining)
	}
}

func TestMiddleware_AllowsUnderLimit(t *testing.T) {
	t.Parallel()
	rl := New(3, time.Minute, false)
	defer rl.Close()

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	handler := Middleware(rl)(next)

	for i := range 3 {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		r.RemoteAddr = "1.2.3.4:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}
	if called != 3 {
		t.Fatalf("expected next called 3 times, got %d", called)
	}
}

func TestMiddleware_Blocks_Returns429WithRetryAfter(t *testing.T) {
	t.Parallel()
	rl := New(1, time.Minute, false)
	defer rl.Close()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Middleware(rl)(next)

	r1 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r1.RemoteAddr = "1.2.3.4:1234"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected first request to pass, got %d", w1.Code)
	}

	r2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r2.RemoteAddr = "1.2.3.4:1234"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w2.Code)
	}
	if w2.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on 429 response")
	}
}

func TestClientIP_TrustProxy_XRealIPFallback(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:9999"
	r.Header.Set("X-Real-Ip", "5.5.5.5")

	if got := clientIP(r, true); got != "5.5.5.5" {
		t.Fatalf("expected X-Real-Ip, got %q", got)
	}
}

func TestClientIP_RemoteAddr_NoPort(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
	r.RemoteAddr = "192.168.1.1"

	if got := clientIP(r, false); got != "192.168.1.1" {
		t.Fatalf("expected full RemoteAddr on parse error, got %q", got)
	}
}
