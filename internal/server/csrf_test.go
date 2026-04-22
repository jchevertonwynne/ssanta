package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRF_BlocksRequestWithoutToken(t *testing.T) {
	t.Parallel()
	h := CSRF([]byte("test-secret"), false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	h := CSRF(secret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	token := computeCSRFToken(secret, csrfID)

	// Now make a POST with the token header
	r2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test", strings.NewReader(""))
	r2.Header.Set("X-CSRF-Token", token)
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
