package session

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManager_SetAndUserID_RoundTrip(t *testing.T) {
	m := NewManager("secret")

	rr := httptest.NewRecorder()
	m.Set(rr, 123)

	res := rr.Result()
	defer res.Body.Close()

	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.AddCookie(cookies[0])

	gotID, ok := m.UserID(req)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if gotID != 123 {
		t.Fatalf("expected id=123, got %d", gotID)
	}
}

func TestManager_UserID_TamperedSignatureRejected(t *testing.T) {
	m := NewManager("secret")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "123.bad"})

	_, ok := m.UserID(req)
	if ok {
		t.Fatalf("expected ok=false")
	}
}

func TestManager_UserID_MalformedRejected(t *testing.T) {
	m := NewManager("secret")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "123"})

	_, ok := m.UserID(req)
	if ok {
		t.Fatalf("expected ok=false")
	}
}

func TestManager_Clear_DeletesCookie(t *testing.T) {
	m := NewManager("secret")

	rr := httptest.NewRecorder()
	m.Clear(rr)

	res := rr.Result()
	defer res.Body.Close()

	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Name != cookieName {
		t.Fatalf("expected cookie name %q, got %q", cookieName, cookies[0].Name)
	}
	if cookies[0].MaxAge != -1 {
		t.Fatalf("expected MaxAge=-1, got %d", cookies[0].MaxAge)
	}
}
