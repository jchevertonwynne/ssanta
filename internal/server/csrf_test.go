package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jchevertonwynne/ssanta/internal/session"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

func TestCSRF_BlocksRequestWithoutToken(t *testing.T) {
	t.Parallel()
	sessions := session.NewManager("session-secret", false, time.Hour)
	h := CSRF(sessions, []byte("test-secret"), false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test", strings.NewReader(""))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestCSRF_AllowsRequestWithValidToken(t *testing.T) {
	t.Parallel()
	secret := []byte("test-secret")
	sessions := session.NewManager("session-secret", false, time.Hour)
	h := CSRF(sessions, secret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First, make a GET request to obtain the csrf_id cookie
	r1 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)

	cookies := w1.Result().Cookies()
	var csrfID string
	for _, c := range cookies {
		if c.Name == "csrf_id" {
			csrfID = c.Value
			break
		}
	}
	if csrfID == "" {
		t.Fatal("expected csrf_id cookie to be set")
	}

	// Compute the expected token
	token := computeCSRFToken(secret, csrfID, nil)

	// Now make a POST with the token header
	r2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test", strings.NewReader(""))
	r2.Header.Set("X-Csrf-Token", token)
	// Copy the cookie from the first response
	for _, c := range cookies {
		r2.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}
}

func TestCSRF_InvalidatesTokenAfterLogin(t *testing.T) {
	t.Parallel()
	secret := []byte("test-secret")
	sessions := session.NewManager("session-secret", false, time.Hour)
	h := CSRF(sessions, secret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First, make a GET request to obtain the csrf_id cookie before login.
	r1 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)

	cookies := w1.Result().Cookies()
	var csrfID string
	for _, c := range cookies {
		if c.Name == "csrf_id" {
			csrfID = c.Value
			break
		}
	}
	if csrfID == "" {
		t.Fatal("expected csrf_id cookie to be set")
	}

	// Token minted before login only binds to csrf_id.
	token := computeCSRFToken(secret, csrfID, nil)

	loginRecorder := httptest.NewRecorder()
	sessions.Set(loginRecorder, store.UserID(42), 0)
	var sessionCookie *http.Cookie
	for _, c := range loginRecorder.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be set")
	}

	// Now make a POST after login with the pre-login CSRF token.
	r2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test", strings.NewReader(""))
	r2.Header.Set("X-Csrf-Token", token)
	for _, c := range cookies {
		r2.AddCookie(c)
	}
	r2.AddCookie(sessionCookie)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)

	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 after login, got %d", w2.Code)
	}
}
