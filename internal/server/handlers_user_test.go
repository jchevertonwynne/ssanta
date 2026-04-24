package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/jchevertonwynne/ssanta/internal/store"
	"github.com/jchevertonwynne/ssanta/internal/ws"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

func TestHandleCreateUser_Success_SetsSessionAndRenders(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	svc.EXPECT().CreateUser(gomock.Any(), "Alice", "secret123").Return(store.UserID(42), nil)
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), store.UserID(42)).Return(0, nil)
	sessions.EXPECT().Set(gomock.Any(), store.UserID(42), 0)
	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(42)).Return(stubContentView("Alice"), nil)
	hub.EXPECT().NotifyContentUpdate(ws.MsgTypeUsersUpdated)

	r := newFormRequest(t, "/users", url.Values{
		"username":         {"Alice"},
		"password":         {"secret123"},
		"password_confirm": {"secret123"},
	})
	w := serve(t, handleCreateUser(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Logged in as") {
		t.Fatalf("expected rendered content to include logged-in bar")
	}
}

func TestHandleCreateUser_PasswordMismatch_RendersError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(0)).Return(stubContentView(""), nil)

	r := newFormRequest(t, "/users", url.Values{
		"username":         {"alice"},
		"password":         {"secret123"},
		"password_confirm": {"different"},
	})
	w := serve(t, handleCreateUser(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "passwords do not match") {
		t.Fatalf("expected password mismatch error")
	}
}

func TestHandleCreateUser_InvalidUsername_RendersFormError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	svc.EXPECT().CreateUser(gomock.Any(), "bad name", "secret123").Return(store.UserID(0), store.ErrUsernameInvalid)
	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(0)).Return(stubContentView(""), nil)

	r := newFormRequest(t, "/users", url.Values{
		"username":         {"bad name"},
		"password":         {"secret123"},
		"password_confirm": {"secret123"},
	})
	w := serve(t, handleCreateUser(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), store.ErrUsernameInvalid.Error()) {
		t.Fatalf("expected error to be rendered")
	}
}

func TestHandleDeleteUser_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/users/1", nil)
	r.SetPathValue("id", "1")
	w := serve(t, handleDeleteUser(svc, sessions, hub), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}

func TestHandleDeleteUser_CannotDeleteOtherUser_Returns403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	expectLoggedIn(t, svc, sessions, 1)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/users/2", nil)
	r.SetPathValue("id", "2")
	w := serve(t, handleDeleteUser(svc, sessions, hub), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestHandleDeleteUser_Success_ClearsSession(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	expectLoggedIn(t, svc, sessions, 7)
	svc.EXPECT().VerifyPassword(gomock.Any(), store.UserID(7), "mypassword").Return(nil)
	svc.EXPECT().DeleteUser(gomock.Any(), store.UserID(7)).Return(nil)
	sessions.EXPECT().Clear(gomock.Any())
	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(0)).Return(stubContentView(""), nil)
	hub.EXPECT().NotifyContentUpdate(ws.MsgTypeUsersUpdated)

	r := newFormRequest(t, "/users/7", url.Values{"current_password": {"mypassword"}})
	r.Method = http.MethodDelete
	r.SetPathValue("id", "7")
	w := serve(t, handleDeleteUser(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestHandleLogin_InvalidCredentials_RendersError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	svc.EXPECT().LoginUser(gomock.Any(), "alice", "wrongpass").Return(store.UserID(0), store.ErrInvalidCredentials)
	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(0)).Return(stubContentView(""), nil)

	r := newFormRequest(t, "/login", url.Values{
		"username": {"alice"},
		"password": {"wrongpass"},
	})
	w := serve(t, handleLogin(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), store.ErrInvalidCredentials.Error()) {
		t.Fatalf("expected invalid credentials error in response")
	}
}

func TestHandleLogin_Success_SetsSessionAndRenders(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	svc.EXPECT().LoginUser(gomock.Any(), "alice", "correctpass").Return(store.UserID(5), nil)
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), store.UserID(5)).Return(0, nil)
	sessions.EXPECT().Set(gomock.Any(), store.UserID(5), 0)
	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(5)).Return(stubContentView("alice"), nil)

	r := newFormRequest(t, "/login", url.Values{
		"username": {"alice"},
		"password": {"correctpass"},
	})
	w := serve(t, handleLogin(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestHandleLogout_ClearsSessionAndRendersLoggedOut(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().Clear(gomock.Any())
	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(0)).Return(stubContentView(""), nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/logout", nil)
	w := serve(t, handleLogout(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "Logged in as") {
		t.Fatalf("expected logged-out view")
	}
}

func TestHandleDeleteUser_InvalidPathID_Returns400(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	expectLoggedIn(t, svc, sessions, 7)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/users/abc", nil)
	r.SetPathValue("id", "abc")
	w := serve(t, handleDeleteUser(svc, sessions, hub), r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestHandleChangePassword_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	expectLoggedIn(t, svc, sessions, 10)
	svc.EXPECT().ChangePassword(gomock.Any(), store.UserID(10), "oldpass12", "newpass12").Return(nil)
	// ChangePassword bumped the server-side version; handler re-reads it and
	// re-signs the cookie to the new version.
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), store.UserID(10)).Return(1, nil)
	sessions.EXPECT().Set(gomock.Any(), store.UserID(10), 1)
	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(10)).Return(stubContentView("alice"), nil)

	r := newFormRequest(t, "/password", url.Values{
		"current_password":     {"oldpass12"},
		"new_password":         {"newpass12"},
		"new_password_confirm": {"newpass12"},
	})
	w := serve(t, handleChangePassword(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Password changed successfully") {
		t.Fatalf("expected success message in response")
	}
}

func TestHandleChangePassword_Unauthorized(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r := newFormRequest(t, "/password", url.Values{
		"current_password":     {"old"},
		"new_password":         {"new12345"},
		"new_password_confirm": {"new12345"},
	})
	w := serve(t, handleChangePassword(svc, sessions), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}

func TestHandleChangePassword_NewPasswordMismatch(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	expectLoggedIn(t, svc, sessions, 10)
	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(10)).Return(stubContentView("alice"), nil)

	r := newFormRequest(t, "/password", url.Values{
		"current_password":     {"oldpass12"},
		"new_password":         {"newpass12"},
		"new_password_confirm": {"different"},
	})
	w := serve(t, handleChangePassword(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "new passwords do not match") {
		t.Fatalf("expected mismatch error in response")
	}
}

func testChangePasswordError(t *testing.T, currentPass, newPass string, expectedErr error) {
	t.Helper()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	expectLoggedIn(t, svc, sessions, 10)
	svc.EXPECT().ChangePassword(gomock.Any(), store.UserID(10), currentPass, newPass).Return(expectedErr)
	svc.EXPECT().GetContentView(gomock.Any(), store.UserID(10)).Return(stubContentView("alice"), nil)

	r := newFormRequest(t, "/password", url.Values{
		"current_password":     {currentPass},
		"new_password":         {newPass},
		"new_password_confirm": {newPass},
	})
	w := serve(t, handleChangePassword(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), expectedErr.Error()) {
		t.Fatalf("expected error in response")
	}
}

func TestHandleChangePassword_IncorrectCurrentPassword(t *testing.T) {
	t.Parallel()
	testChangePasswordError(t, "wrongpass", "newpass12", store.ErrCurrentPasswordIncorrect)
}

func TestHandleChangePassword_PasswordTooShort(t *testing.T) {
	t.Parallel()
	testChangePasswordError(t, "oldpass12", "short", store.ErrPasswordTooShort)
}
