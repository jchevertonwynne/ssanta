package session

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testTTL = time.Hour

func TestManager_SetAndUserID_RoundTrip(t *testing.T) {
	m := NewManager("secret", false, testTTL)

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

func TestManager_Set_RefusesZeroUserID(t *testing.T) {
	m := NewManager("secret", false, testTTL)
	rr := httptest.NewRecorder()
	m.Set(rr, 0)
	if cookies := rr.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("expected no cookie for userID=0, got %d", len(cookies))
	}
}

func TestManager_UserID_Expired(t *testing.T) {
	m := NewManager("secret", false, time.Minute)
	current := time.Unix(1_700_000_000, 0)
	m.SetNowFn(func() time.Time { return current })

	rr := httptest.NewRecorder()
	m.Set(rr, 123)
	cookie := rr.Result().Cookies()[0]

	// Move time forward past TTL.
	m.SetNowFn(func() time.Time { return current.Add(2 * time.Minute) })

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.AddCookie(cookie)
	if _, ok := m.UserID(req); ok {
		t.Fatalf("expected expired session to be rejected")
	}
}

func TestManager_Set_CookieAttributes(t *testing.T) {
	m := NewManager("secret", true, testTTL)
	rr := httptest.NewRecorder()
	m.Set(rr, 42)

	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie")
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Fatalf("expected HttpOnly=true")
	}
	if !c.Secure {
		t.Fatalf("expected Secure=true")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected SameSite=Lax")
	}
	if c.MaxAge != int(testTTL.Seconds()) {
		t.Fatalf("expected MaxAge=%d, got %d", int(testTTL.Seconds()), c.MaxAge)
	}
	if c.Path != "/" {
		t.Fatalf("expected Path=/")
	}
}

func TestManager_UserID_TamperedSignatureRejected(t *testing.T) {
	m := NewManager("secret", false, testTTL)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "123|1700000000.bad"})

	_, ok := m.UserID(req)
	if ok {
		t.Fatalf("expected ok=false")
	}
}

func TestManager_UserID_MalformedRejected(t *testing.T) {
	m := NewManager("secret", false, testTTL)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "123"})

	_, ok := m.UserID(req)
	if ok {
		t.Fatalf("expected ok=false")
	}
}

func TestManager_UserID_ZeroIDRejected(t *testing.T) {
	m := NewManager("secret", false, testTTL)
	current := time.Unix(1_700_000_000, 0)
	m.SetNowFn(func() time.Time { return current })

	// Hand-craft a cookie for userID=0; should be rejected even with a valid signature.
	payload := "0|" + "1700000000"
	sig := m.sign(payload)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: payload + "." + sig})

	if _, ok := m.UserID(req); ok {
		t.Fatalf("expected userID=0 to be rejected")
	}
}

func TestManager_Clear_DeletesCookie(t *testing.T) {
	m := NewManager("secret", false, testTTL)

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
