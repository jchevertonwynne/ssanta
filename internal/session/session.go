// Package session manages signed session cookies.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

const cookieName = "session"

var (
	// ErrInvalidSession indicates a malformed or expired session cookie.
	ErrInvalidSession = errors.New("invalid session")
)

// Manager signs and validates session cookies.
type Manager struct {
	secret []byte
	secure bool
	ttl    time.Duration
	now    func() time.Time
}

// NewManager constructs a session manager.
func NewManager(secret string, secure bool, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 168 * time.Hour
	}
	return &Manager{
		secret: []byte(secret),
		secure: secure,
		ttl:    ttl,
		now:    time.Now,
	}
}

// SetNowFn lets tests override the clock.
func (m *Manager) SetNowFn(fn func() time.Time) { m.now = fn }

// Secret exposes the raw signing secret to other packages (e.g., CSRF) that
// need to derive cookie-bound tokens. Keep this internal-only.
func (m *Manager) Secret() []byte { return slices.Clone(m.secret) }

// Secure returns whether cookies should be marked Secure.
func (m *Manager) Secure() bool { return m.secure }

// Set writes a signed session cookie for the given user and session version.
// Bumping the server-side version invalidates all previously-issued cookies
// for that user — used by ChangePassword and any future "logout everywhere".
func (m *Manager) Set(w http.ResponseWriter, userID store.UserID, version int) {
	if userID == 0 {
		return
	}
	payload := strconv.FormatInt(userID.Int64(), 10) + "|" +
		strconv.FormatInt(m.now().Unix(), 10) + "|" +
		strconv.Itoa(version)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    payload + "." + m.sign(payload),
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(m.ttl.Seconds()),
	})
}

// Clear deletes the session cookie.
func (m *Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// UserID extracts and validates the current session user ID and the session
// version carried by the cookie. Callers must compare the version against the
// persisted server-side value before trusting the session.
//
//nolint:cyclop
func (m *Manager) UserID(r *http.Request) (store.UserID, int, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return 0, 0, false
	}
	payload, sig, valid := strings.Cut(cookie.Value, ".")
	if !valid {
		return 0, 0, false
	}
	if !hmac.Equal([]byte(sig), []byte(m.sign(payload))) {
		return 0, 0, false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 3 {
		return 0, 0, false
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || userID <= 0 {
		return 0, 0, false
	}
	issuedUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	version, err := strconv.Atoi(parts[2])
	if err != nil || version < 0 {
		return 0, 0, false
	}
	if m.now().Sub(time.Unix(issuedUnix, 0)) > m.ttl {
		return 0, 0, false
	}
	return store.UserID(userID), version, true
}

func (m *Manager) sign(payload string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
