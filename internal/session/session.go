package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

const (
	cookieName     = "session"
	csrfCookieName = "csrf_id"
)

var (
	ErrInvalidSession = errors.New("invalid session")
)

type Manager struct {
	secret []byte
	secure bool
	ttl    time.Duration
	now    func() time.Time
}

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
func (m *Manager) Secret() []byte { return m.secret }

// Secure returns whether cookies should be marked Secure.
func (m *Manager) Secure() bool { return m.secure }

func (m *Manager) Set(w http.ResponseWriter, userID store.UserID) {
	if userID == 0 {
		return
	}
	payload := strconv.FormatInt(userID.Int64(), 10) + "|" + strconv.FormatInt(m.now().Unix(), 10)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    payload + "." + m.sign(payload),
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.ttl.Seconds()),
	})
}

func (m *Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) UserID(r *http.Request) (store.UserID, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return 0, false
	}
	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return 0, false
	}
	if !hmac.Equal([]byte(sig), []byte(m.sign(payload))) {
		return 0, false
	}
	idStr, issuedStr, ok := strings.Cut(payload, "|")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	issuedUnix, err := strconv.ParseInt(issuedStr, 10, 64)
	if err != nil {
		return 0, false
	}
	if m.now().Sub(time.Unix(issuedUnix, 0)) > m.ttl {
		return 0, false
	}
	return store.UserID(id), true
}

func (m *Manager) sign(payload string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
