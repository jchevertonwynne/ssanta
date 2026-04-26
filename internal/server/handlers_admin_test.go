package server

import (
	"net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/store"
	"github.com/jchevertonwynne/ssanta/internal/ws"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

func stubAdminView() *service.AdminView {
	return &service.AdminView{CurrentUsername: "admin", Users: nil, Rooms: nil}
}

const testAdminID = store.UserID(1)

func expectAdminLoggedIn(t *testing.T, svc *servermocks.MockServerService, sessions *servermocks.MockSessionManager) {
	t.Helper()
	expectLoggedIn(t, svc, sessions, testAdminID)
	svc.EXPECT().IsUserAdmin(gomock.Any(), testAdminID).Return(true, nil)
}

// handleAdminPage — non-HTMX path skips auth entirely.
func TestHandleAdminPage_NonHTMX_RendersShell(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", nil)
	// No Hx-Request header → no auth check, renders index shell.
	w := serve(t, handleAdminPage(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleAdminPage_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", nil)
	r.Header.Set("Hx-Request", "true")
	w := serve(t, handleAdminPage(svc, sessions), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleAdminPage_NotAdmin_Returns403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	expectLoggedIn(t, svc, sessions, store.UserID(1))
	svc.EXPECT().IsUserAdmin(gomock.Any(), store.UserID(1)).Return(false, nil)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", nil)
	r.Header.Set("Hx-Request", "true")
	w := serve(t, handleAdminPage(svc, sessions), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleAdminPage_Success_SetsHxPushUrl(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	adminID := testAdminID
	expectAdminLoggedIn(t, svc, sessions)
	svc.EXPECT().GetAdminView(gomock.Any(), adminID).Return(stubAdminView(), nil)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", nil)
	r.Header.Set("Hx-Request", "true")
	w := serve(t, handleAdminPage(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Hx-Push-Url") != "/admin" {
		t.Fatalf("expected Hx-Push-Url: /admin, got %q", w.Header().Get("Hx-Push-Url"))
	}
}

// handleAdminDeleteUser

func TestHandleAdminDeleteUser_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/users/99", nil)
	r.SetPathValue("id", "99")
	w := serve(t, handleAdminDeleteUser(svc, sessions, hub), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleAdminDeleteUser_NotAdmin_Returns403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	expectLoggedIn(t, svc, sessions, store.UserID(1))
	svc.EXPECT().IsUserAdmin(gomock.Any(), store.UserID(1)).Return(false, nil)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/users/99", nil)
	r.SetPathValue("id", "99")
	w := serve(t, handleAdminDeleteUser(svc, sessions, hub), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleAdminDeleteUser_UserNotFound_Returns404(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	adminID := testAdminID
	targetID := store.UserID(99)
	expectAdminLoggedIn(t, svc, sessions)
	svc.EXPECT().AdminDeleteUser(gomock.Any(), adminID, targetID).Return(store.ErrUserNotFound)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/users/99", nil)
	r.SetPathValue("id", "99")
	w := serve(t, handleAdminDeleteUser(svc, sessions, hub), r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleAdminDeleteUser_Success_NotifiesHubAndRendersAdmin(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	adminID := testAdminID
	targetID := store.UserID(99)
	expectAdminLoggedIn(t, svc, sessions)
	svc.EXPECT().AdminDeleteUser(gomock.Any(), adminID, targetID).Return(nil)
	// MockHub does not implement HandleAccountDeletion — the type assertion silently fails, which is fine.
	hub.EXPECT().NotifyContentUpdate(ws.MsgTypeUsersUpdated)
	svc.EXPECT().GetAdminView(gomock.Any(), adminID).Return(stubAdminView(), nil)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/users/99", nil)
	r.SetPathValue("id", "99")
	w := serve(t, handleAdminDeleteUser(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// handleAdminDeleteRoom

func TestHandleAdminDeleteRoom_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/rooms/10", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleAdminDeleteRoom(svc, sessions, hub), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleAdminDeleteRoom_RoomNotFound_Returns404(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	adminID := testAdminID
	roomID := store.RoomID(10)
	expectAdminLoggedIn(t, svc, sessions)
	svc.EXPECT().AdminDeleteRoom(gomock.Any(), adminID, roomID).Return(store.ErrRoomNotFound)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/rooms/10", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleAdminDeleteRoom(svc, sessions, hub), r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleAdminDeleteRoom_DMRoom_Returns400(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	adminID := testAdminID
	roomID := store.RoomID(10)
	expectAdminLoggedIn(t, svc, sessions)
	svc.EXPECT().AdminDeleteRoom(gomock.Any(), adminID, roomID).Return(store.ErrOperationNotAllowedOnDM)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/rooms/10", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleAdminDeleteRoom(svc, sessions, hub), r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleAdminDeleteRoom_Success_DisconnectsRoomAndRendersAdmin(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	adminID := testAdminID
	roomID := store.RoomID(10)
	expectAdminLoggedIn(t, svc, sessions)
	svc.EXPECT().AdminDeleteRoom(gomock.Any(), adminID, roomID).Return(nil)
	hub.EXPECT().DisconnectRoom(roomID)
	svc.EXPECT().GetAdminView(gomock.Any(), adminID).Return(stubAdminView(), nil)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/rooms/10", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleAdminDeleteRoom(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// handleAdminSetUserAdmin

func TestHandleAdminSetUserAdmin_Grant_Success_RendersAdmin(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	adminID := testAdminID
	targetID := store.UserID(2)
	expectAdminLoggedIn(t, svc, sessions)
	svc.EXPECT().SetUserAdmin(gomock.Any(), adminID, targetID, true).Return(nil)
	svc.EXPECT().GetAdminView(gomock.Any(), adminID).Return(stubAdminView(), nil)

	r := newFormRequest(t, "/admin/users/2/admin", map[string][]string{"grant": {"true"}})
	r.SetPathValue("id", "2")
	w := serve(t, handleAdminSetUserAdmin(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleAdminSetUserAdmin_Revoke_Self_Returns400(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	adminID := testAdminID
	expectAdminLoggedIn(t, svc, sessions)
	svc.EXPECT().SetUserAdmin(gomock.Any(), adminID, adminID, false).Return(store.ErrCannotSelfDemote)

	r := newFormRequest(t, "/admin/users/1/admin", map[string][]string{"grant": {"false"}})
	r.SetPathValue("id", "1")
	w := serve(t, handleAdminSetUserAdmin(svc, sessions), r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleAdminSetUserAdmin_UserNotFound_Returns404(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	adminID := testAdminID
	targetID := store.UserID(99)
	expectAdminLoggedIn(t, svc, sessions)
	svc.EXPECT().SetUserAdmin(gomock.Any(), adminID, targetID, true).Return(store.ErrUserNotFound)

	r := newFormRequest(t, "/admin/users/99/admin", map[string][]string{"grant": {"true"}})
	r.SetPathValue("id", "99")
	w := serve(t, handleAdminSetUserAdmin(svc, sessions), r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
