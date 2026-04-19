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

func TestHandleCreateInvite_NotAllowed_RendersInviteError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	roomID := int64(10)
	userID := int64(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().CreateInvite(gomock.Any(), roomID, userID, "bob").Return(store.ErrNotAllowedToInvite)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView(roomID, "alice"), nil)

	r := newFormRequest(t, http.MethodPost, "/rooms/10/invites", url.Values{"invitee_username": {"bob"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleCreateInvite(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), store.ErrNotAllowedToInvite.Error()) {
		t.Fatalf("expected invite error rendered")
	}
}

func TestHandleCreateInvite_Success_BroadcastsAndNotifiesInvitee(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	roomID := int64(10)
	userID := int64(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().CreateInvite(gomock.Any(), roomID, userID, "bob").Return(nil)
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil)
	hub.EXPECT().BroadcastSystemMessage(roomID, "alice invited bob")
	svc.EXPECT().GetUserByUsername(gomock.Any(), "bob").Return(store.User{ID: 99, Username: "bob"}, nil)
	hub.EXPECT().NotifyUser(int64(99), "invite_received", "")
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView(roomID, "alice"), nil)

	r := newFormRequest(t, http.MethodPost, "/rooms/10/invites", url.Values{"invitee_username": {"bob"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleCreateInvite(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestHandleAcceptInvite_NotFoundOnPreLookup_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := int64(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().RoomIDForInvite(gomock.Any(), int64(123)).Return(int64(0), store.ErrInviteNotFound)

	r := httptest.NewRequest(http.MethodPost, "/invites/123/accept", nil)
	r.SetPathValue("id", "123")
	w := serve(t, handleAcceptInvite(svc, sessions, hub), r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestHandleAcceptInvite_Success_NotifiesRoomAndRenders(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := int64(2)
	roomID := int64(10)
	inviteID := int64(123)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().RoomIDForInvite(gomock.Any(), inviteID).Return(roomID, nil)
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil)
	svc.EXPECT().AcceptInvite(gomock.Any(), inviteID, userID).Return(nil)
	hub.EXPECT().NotifyRoomUpdate(roomID)
	hub.EXPECT().BroadcastSystemMessage(roomID, "alice joined the room")
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView(roomID, "alice"), nil)

	r := httptest.NewRequest(http.MethodPost, "/invites/123/accept", nil)
	r.SetPathValue("id", "123")
	w := serve(t, handleAcceptInvite(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestHandleDeclineInvite_NotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := int64(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().DeclineInvite(gomock.Any(), int64(123), userID).Return(store.ErrInviteNotFound)

	r := httptest.NewRequest(http.MethodPost, "/invites/123/decline", nil)
	r.SetPathValue("id", "123")
	w := serve(t, handleDeclineInvite(svc, sessions), r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestHandleCancelInvite_Forbidden_Returns403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := int64(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().RoomIDForInvite(gomock.Any(), int64(123)).Return(int64(10), nil)
	svc.EXPECT().InviteeIDForInvite(gomock.Any(), int64(123)).Return(int64(99), nil)
	svc.EXPECT().CancelInvite(gomock.Any(), int64(123), userID).Return(store.ErrNotAllowedToCancelInvite)

	r := httptest.NewRequest(http.MethodPost, "/invites/123/cancel", nil)
	r.SetPathValue("id", "123")
	w := serve(t, handleCancelInvite(svc, sessions, hub), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}
