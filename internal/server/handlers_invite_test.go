package server

import (
	"context"
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

	roomID := store.RoomID(10)
	userID := store.UserID(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().CreateInvite(gomock.Any(), roomID, userID, "bob").Return(store.ErrNotAllowedToInvite)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := newFormRequest(t, "/rooms/10/invites", url.Values{"invitee_username": {"bob"}})
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

	roomID := store.RoomID(10)
	userID := store.UserID(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().CreateInvite(gomock.Any(), roomID, userID, "bob").Return(nil)
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil)
	hub.EXPECT().BroadcastSystemMessage(roomID, "alice invited bob")
	svc.EXPECT().GetUserByUsername(gomock.Any(), "bob").Return(store.User{ID: 99, Username: "bob"}, nil)
	hub.EXPECT().NotifyUser(store.UserID(99), "invite_received", "")
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := newFormRequest(t, "/rooms/10/invites", url.Values{"invitee_username": {"bob"}})
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

	userID := store.UserID(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().AcceptInvite(gomock.Any(), store.InviteID(123), userID).Return(store.RoomID(0), store.ErrInviteNotFound)

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/invites/123/accept", nil)
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

	userID := store.UserID(2)
	roomID := store.RoomID(10)
	inviteID := store.InviteID(123)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().AcceptInvite(gomock.Any(), inviteID, userID).Return(roomID, nil)
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil)
	hub.EXPECT().BroadcastSystemMessage(roomID, "alice joined the room")
	hub.EXPECT().NotifyRoomUpdate(roomID)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/invites/123/accept", nil)
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

	userID := store.UserID(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().DeclineInvite(gomock.Any(), store.InviteID(123), userID).Return(store.ErrInviteNotFound)

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/invites/123/decline", nil)
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

	userID := store.UserID(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().CancelInvite(gomock.Any(), store.InviteID(123), userID).Return(store.RoomID(0), store.UserID(0), store.ErrNotAllowedToCancelInvite)

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/invites/123/cancel", nil)
	r.SetPathValue("id", "123")
	w := serve(t, handleCancelInvite(svc, sessions, hub), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestHandleAcceptInvite_WrongUser_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(2)
	inviteID := store.InviteID(123)
	expectLoggedIn(t, svc, sessions, userID)
	// store returns ErrInviteNotFound when the invite belongs to a different user
	svc.EXPECT().AcceptInvite(gomock.Any(), inviteID, userID).Return(store.RoomID(0), store.ErrInviteNotFound)

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/invites/123/accept", nil)
	r.SetPathValue("id", "123")
	w := serve(t, handleAcceptInvite(svc, sessions, hub), r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestHandleDeclineInvite_WrongUser_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(2)
	expectLoggedIn(t, svc, sessions, userID)
	// store returns ErrInviteNotFound when invitee_id doesn't match
	svc.EXPECT().DeclineInvite(gomock.Any(), store.InviteID(123), userID).Return(store.ErrInviteNotFound)

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/invites/123/decline", nil)
	r.SetPathValue("id", "123")
	w := serve(t, handleDeclineInvite(svc, sessions), r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestHandleDeclineInvite_Success_RendersContent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(2)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().DeclineInvite(gomock.Any(), store.InviteID(123), userID).Return(nil)
	svc.EXPECT().GetContentView(gomock.Any(), userID).Return(stubContentView("alice"), nil)

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/invites/123/decline", nil)
	r.SetPathValue("id", "123")
	w := serve(t, handleDeclineInvite(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}
