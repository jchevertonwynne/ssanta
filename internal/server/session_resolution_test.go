package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/jchevertonwynne/ssanta/internal/store"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

func TestResolveSessionUser_NoCookie_ReturnsLoggedOut(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockUserExistsService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), false)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

	id, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
	if ok {
		t.Fatalf("expected ok=false")
	}
	if id != 0 {
		t.Fatalf("expected id=0, got %d", id)
	}
}

func TestResolveSessionUser_SignedCookieForDeletedUser_ClearsCookie(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockUserExistsService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(123), true)
	svc.EXPECT().UserExists(gomock.Any(), store.UserID(123)).Return(false, nil)
	sessions.EXPECT().Clear(gomock.Any())

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

	id, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
	if ok {
		t.Fatalf("expected ok=false")
	}
	if id != 0 {
		t.Fatalf("expected id=0, got %d", id)
	}
}
