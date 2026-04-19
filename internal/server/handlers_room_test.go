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

func TestHandleCreateRoom_Unauthorized_Returns401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(int64(0), false)

	r := newFormRequest(t, http.MethodPost, "/rooms", url.Values{"display_name": {"room"}})
	w := serve(t, handleCreateRoom(svc, sessions), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}

func TestHandleCreateRoom_EmptyName_RendersError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	expectLoggedIn(t, svc, sessions, 1)
	svc.EXPECT().CreateRoom(gomock.Any(), "", int64(1)).Return(store.ErrRoomNameEmpty)
	svc.EXPECT().GetContentView(gomock.Any(), int64(1)).Return(stubContentView(""), nil)

	r := newFormRequest(t, http.MethodPost, "/rooms", url.Values{"display_name": {""}})
	w := serve(t, handleCreateRoom(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), store.ErrRoomNameEmpty.Error()) {
		t.Fatalf("expected room name error rendered")
	}
}

func TestHandleJoinRoom_NonCreator_RendersRoomDetailAndNotifiesRoom(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	roomID := int64(10)
	userID := int64(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil)
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, true, nil)
	svc.EXPECT().JoinRoom(gomock.Any(), roomID, userID).Return(nil)
	hub.EXPECT().BroadcastSystemMessage(roomID, "alice joined the room")
	hub.EXPECT().NotifyRoomUpdate(roomID)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView(roomID, "alice"), nil)

	r := httptest.NewRequest(http.MethodPost, "/rooms/10/join", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleJoinRoom(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Logged in as") {
		t.Fatalf("expected room detail (with bar) to be rendered")
	}
}

func TestHandleJoinRoom_Creator_RendersSidebarAndNotifiesUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	roomID := int64(10)
	userID := int64(1)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("creator", nil)
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(true, false, nil)
	svc.EXPECT().JoinRoom(gomock.Any(), roomID, userID).Return(nil)
	hub.EXPECT().BroadcastSystemMessage(roomID, "creator joined the room")
	hub.EXPECT().NotifyRoomUpdate(roomID)
	hub.EXPECT().NotifyUser(userID, "membership_gained", "")
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView(roomID, "creator"), nil)

	r := httptest.NewRequest(http.MethodPost, "/rooms/10/join", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleJoinRoom(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "Logged in as") {
		t.Fatalf("expected sidebar fragment (no top bar)")
	}
	if !strings.Contains(w.Body.String(), "id=\"room-sidebar\"") {
		t.Fatalf("expected room sidebar to be rendered")
	}
}

func TestHandleLeaveRoom_NotMember_Returns403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	roomID := int64(10)
	userID := int64(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil)
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, false, nil)
	svc.EXPECT().LeaveRoom(gomock.Any(), roomID, userID).Return(store.ErrNotRoomMember)

	r := httptest.NewRequest(http.MethodPost, "/rooms/10/leave", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleLeaveRoom(svc, sessions, hub), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestHandleRoomDetail_LoggedOut_NonHTMX_RedirectsHome(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(int64(0), false)

	r := httptest.NewRequest(http.MethodGet, "/rooms/10", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleRoomDetail(svc, sessions), r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected status 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to '/', got %q", loc)
	}
}

func TestHandleRoomDetail_LoggedOut_HTMX_Returns401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(int64(0), false)

	r := httptest.NewRequest(http.MethodGet, "/rooms/10", nil)
	r.SetPathValue("id", "10")
	r.Header.Set("HX-Request", "true")
	w := serve(t, handleRoomDetail(svc, sessions), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}

func TestHandleSetMembersCanInvite_NonCreator_Returns403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	roomID := int64(10)
	userID := int64(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().SetRoomMembersCanInvite(gomock.Any(), roomID, userID, true).Return(store.ErrNotRoomCreator)

	r := newFormRequest(t, http.MethodPost, "/rooms/10/members-can-invite", url.Values{"value": {"true"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleSetMembersCanInvite(svc, sessions), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestHandleRemoveMember_Success_DisconnectsAndRendersDynamic(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	roomID := int64(10)
	creatorID := int64(1)
	memberID := int64(2)
	expectLoggedIn(t, svc, sessions, creatorID)
	svc.EXPECT().RemoveMember(gomock.Any(), roomID, memberID, creatorID).Return(nil)
	svc.EXPECT().GetUsername(gomock.Any(), memberID).Return("bob", nil)
	hub.EXPECT().BroadcastSystemMessage(roomID, "bob was removed from the room")
	hub.EXPECT().DisconnectUser(roomID, memberID)
	hub.EXPECT().NotifyRoomUpdate(roomID)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, creatorID).Return(stubRoomDetailView(roomID, "creator"), nil)

	r := httptest.NewRequest(http.MethodDelete, "/rooms/10/members/2", nil)
	r.SetPathValue("id", "10")
	r.SetPathValue("memberid", "2")
	w := serve(t, handleRemoveMember(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "id=\"room-dynamic\"") {
		t.Fatalf("expected room dynamic fragment")
	}
}
