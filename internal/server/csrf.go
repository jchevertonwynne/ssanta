package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

const (
	csrfCookieName = "csrf_id"
	csrfHeaderName = "X-Csrf-Token"
	csrfFormField  = "_csrf"
)

// CSRF returns a middleware that enforces a per-cookie CSRF token on every
// state-changing request. The token is HMAC(secret, csrf_id-cookie) when the
// request is anonymous, or HMAC(secret, csrf_id-cookie|userID) when a valid
// session cookie is present. The token is either submitted as the
// X-CSRF-Token header (HTMX-friendly) or as the `_csrf` form field. On the
// first GET that goes through, a fresh csrf_id cookie is set if absent. The
// expected token is exposed to templates via the `csrf` template func.
func CSRF(sessions SessionManager, secret []byte, secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := readOrIssueCSRFID(w, r, secure)
			var userID *store.UserID
			if sessions != nil {
				if currentID, _, ok := sessions.UserID(r); ok {
					userID = &currentID
				}
			}
			ctx := context.WithValue(r.Context(), ctxKeyCSRFID, id)
			ctx = context.WithValue(ctx, ctxKeyCSRFToken, computeCSRFToken(secret, id, userID))
			r = r.WithContext(ctx)

			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			expected := computeCSRFToken(secret, id, userID)
			provided := r.Header.Get(csrfHeaderName)
			if provided == "" {
				r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
				if err := r.ParseForm(); err == nil {
					provided = r.Form.Get(csrfFormField)
				}
			}
			if provided == "" || !hmac.Equal([]byte(provided), []byte(expected)) {
				http.Error(w, "invalid csrf token", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func readOrIssueCSRFID(w http.ResponseWriter, r *http.Request, secure bool) string {
	if c, err := r.Cookie(csrfCookieName); err == nil && len(c.Value) >= 16 {
		return c.Value
	}
	id := newCSRFID()
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return id
}

func newCSRFID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; fall back to a degraded but non-empty value.
		return "csrf-randread-failed"
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func CSRFTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(ctxKeyCSRFToken).(string)
	return token
}

func computeCSRFToken(secret []byte, csrfID string, userID *store.UserID) string {
	h := hmac.New(sha256.New, secret)
	hashable := csrfID
	if userID != nil {
		hashable = fmt.Sprintf("%s|%d", csrfID, *userID)
	}
	h.Write([]byte(hashable))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
