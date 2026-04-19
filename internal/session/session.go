package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
)

const cookieName = "session"

type Manager struct {
	secret []byte
	secure bool
}

func NewManager(secret string, secure bool) *Manager {
	return &Manager{secret: []byte(secret), secure: secure}
}

func (m *Manager) Set(w http.ResponseWriter, userID int64) {
	payload := strconv.FormatInt(userID, 10)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    payload + "." + m.sign(payload),
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
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

func (m *Manager) UserID(r *http.Request) (int64, bool) {
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
	id, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func (m *Manager) sign(payload string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
