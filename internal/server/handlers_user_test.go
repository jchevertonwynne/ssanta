package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/jchevertonwynne/ssanta/internal/store"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

func TestHandleCreateUser_Success_SetsSessionAndRenders(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	svc.EXPECT().CreateUser(gomock.Any(), "Alice", "secret123").Return(int64(42), nil)
	sessions.EXPECT().Set(gomock.Any(), int64(42))
	svc.EXPECT().GetContentView(gomock.Any(), int64(42)).Return(stubContentView("Alice"), nil)

	r := newFormRequest(t, http.MethodPost, "/users", url.Values{
		"username":         {"Alice"},
		"password":         {"secret123"},
		"password_confirm": {"secret123"},
	})
	w := serve(t, handleCreateUser(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Logged in as") {
		t.Fatalf("expected rendered content to include logged-in bar")
	}
}

func TestHandleCreateUser_PasswordMismatch_RendersError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	svc.EXPECT().GetContentView(gomock.Any(), int64(0)).Return(stubContentView(""), nil)

	r := newFormRequest(t, http.MethodPost, "/users", url.Values{
		"username":         {"alice"},
		"password":         {"secret123"},
		"password_confirm": {"different"},
	})
	w := serve(t, handleCreateUser(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "passwords do not match") {
		t.Fatalf("expected password mismatch error")
	}
}

func TestHandleCreateUser_InvalidUsername_RendersFormError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	svc.EXPECT().CreateUser(gomock.Any(), "bad name", "secret123").Return(int64(0), store.ErrUsernameInvalid)
	svc.EXPECT().GetContentView(gomock.Any(), int64(0)).Return(stubContentView(""), nil)

	r := newFormRequest(t, http.MethodPost, "/users", url.Values{
		"username":         {"bad name"},
		"password":         {"secret123"},
		"password_confirm": {"secret123"},
	})
	w := serve(t, handleCreateUser(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), store.ErrUsernameInvalid.Error()) {
		t.Fatalf("expected error to be rendered")
	}
}

func TestHandleDeleteUser_Unauthorized_Returns401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(int64(0), false)

	r := httptest.NewRequest(http.MethodDelete, "/users/1", nil)
	r.SetPathValue("id", "1")
	w := serve(t, handleDeleteUser(svc, sessions), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}

func TestHandleDeleteUser_CannotDeleteOtherUser_Returns403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	expectLoggedIn(t, svc, sessions, 1)

	r := httptest.NewRequest(http.MethodDelete, "/users/2", nil)
	r.SetPathValue("id", "2")
	w := serve(t, handleDeleteUser(svc, sessions), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestHandleDeleteUser_Success_ClearsSession(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	expectLoggedIn(t, svc, sessions, 7)
	svc.EXPECT().DeleteUser(gomock.Any(), int64(7)).Return(nil)
	sessions.EXPECT().Clear(gomock.Any())
	svc.EXPECT().GetContentView(gomock.Any(), int64(0)).Return(stubContentView(""), nil)

	r := httptest.NewRequest(http.MethodDelete, "/users/7", nil)
	r.SetPathValue("id", "7")
	w := serve(t, handleDeleteUser(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestHandleLogin_InvalidCredentials_RendersError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	svc.EXPECT().LoginUser(gomock.Any(), "alice", "wrongpass").Return(int64(0), store.ErrInvalidCredentials)
	svc.EXPECT().GetContentView(gomock.Any(), int64(0)).Return(stubContentView(""), nil)

	r := newFormRequest(t, http.MethodPost, "/login", url.Values{
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
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	svc.EXPECT().LoginUser(gomock.Any(), "alice", "correctpass").Return(int64(5), nil)
	sessions.EXPECT().Set(gomock.Any(), int64(5))
	svc.EXPECT().GetContentView(gomock.Any(), int64(5)).Return(stubContentView("alice"), nil)

	r := newFormRequest(t, http.MethodPost, "/login", url.Values{
		"username": {"alice"},
		"password": {"correctpass"},
	})
	w := serve(t, handleLogin(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestHandleLogout_ClearsSessionAndRendersLoggedOut(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().Clear(gomock.Any())
	svc.EXPECT().GetContentView(gomock.Any(), int64(0)).Return(stubContentView(""), nil)

	r := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := serve(t, handleLogout(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "Logged in as") {
		t.Fatalf("expected logged-out view")
	}
}

func TestHandleDeleteUser_InvalidPathID_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	expectLoggedIn(t, svc, sessions, 7)

	r := httptest.NewRequest(http.MethodDelete, "/users/abc", nil)
	r.SetPathValue("id", "abc")
	w := serve(t, handleDeleteUser(svc, sessions), r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}
