package server

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowWithinLimit(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	ip := "1.2.3.4"

	for i := 0; i < 3; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}
}

func TestRateLimiter_BlockOverLimit(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	ip := "1.2.3.4"

	if !rl.Allow(ip) {
		t.Fatal("expected first request to be allowed")
	}
	if !rl.Allow(ip) {
		t.Fatal("expected second request to be allowed")
	}
	if rl.Allow(ip) {
		t.Fatal("expected third request to be blocked")
	}
}

func TestRateLimiter_ResetAfterWindow(t *testing.T) {
	rl := newRateLimiter(1, 50*time.Millisecond)
	ip := "1.2.3.4"

	if !rl.Allow(ip) {
		t.Fatal("expected first request to be allowed")
	}
	if rl.Allow(ip) {
		t.Fatal("expected second request to be blocked")
	}

	time.Sleep(60 * time.Millisecond)

	if !rl.Allow(ip) {
		t.Fatal("expected request after window to be allowed")
	}
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("expected first IP to be allowed")
	}
	if !rl.Allow("5.6.7.8") {
		t.Fatal("expected second IP to be allowed")
	}
}
