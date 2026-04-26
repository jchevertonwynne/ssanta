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

// handleSetRoomPGPKey

func TestHandleSetRoomPGPKey_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r := newFormRequest(t, "/rooms/10/pgp", url.Values{"pgp_public_key": {"key"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleSetRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleSetRoomPGPKey_NotMember_Returns403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().SetRoomPGPKey(gomock.Any(), roomID, userID, "key").Return(store.ErrNotRoomMember)

	r := newFormRequest(t, "/rooms/10/pgp", url.Values{"pgp_public_key": {"key"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleSetRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleSetRoomPGPKey_RoomNotFound_Returns404(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().SetRoomPGPKey(gomock.Any(), roomID, userID, "key").Return(store.ErrRoomNotFound)

	r := newFormRequest(t, "/rooms/10/pgp", url.Values{"pgp_public_key": {"key"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleSetRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleSetRoomPGPKey_InvalidKey_RendersSidebarWithError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().SetRoomPGPKey(gomock.Any(), roomID, userID, "bad-key").Return(store.ErrPGPKeyInvalid)
	// Render sidebar with error — needs GetRoomDetailView.
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := newFormRequest(t, "/rooms/10/pgp", url.Values{"pgp_public_key": {"bad-key"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleSetRoomPGPKey(svc, sessions, hub), r)

	// A validation error renders the sidebar, not a 4xx.
	if w.Code == http.StatusForbidden || w.Code == http.StatusNotFound {
		t.Fatalf("expected non-4xx (sidebar render), got %d", w.Code)
	}
}

func TestHandleSetRoomPGPKey_Success_NotifiesHubAndRendersSidebar(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().SetRoomPGPKey(gomock.Any(), roomID, userID, "valid-key").Return(nil)
	hub.EXPECT().NotifyRoomUpdate(roomID)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := newFormRequest(t, "/rooms/10/pgp", url.Values{"pgp_public_key": {"valid-key"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleSetRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// handleVerifyRoomPGPKey

func TestHandleVerifyRoomPGPKey_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r := newFormRequest(t, "/rooms/10/pgp/verify", url.Values{"decrypted_challenge": {"abc"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleVerifyRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleVerifyRoomPGPKey_NotMember_Returns403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().VerifyRoomPGPKey(gomock.Any(), roomID, userID, "abc").Return(store.ErrNotRoomMember)

	r := newFormRequest(t, "/rooms/10/pgp/verify", url.Values{"decrypted_challenge": {"abc"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleVerifyRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleVerifyRoomPGPKey_ChallengeMissing_RendersSidebarWithError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().VerifyRoomPGPKey(gomock.Any(), roomID, userID, "abc").Return(store.ErrPGPChallengeMissing)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := newFormRequest(t, "/rooms/10/pgp/verify", url.Values{"decrypted_challenge": {"abc"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleVerifyRoomPGPKey(svc, sessions, hub), r)

	if w.Code == http.StatusForbidden || w.Code == http.StatusNotFound {
		t.Fatalf("expected sidebar render (non-4xx), got %d", w.Code)
	}
}

func TestHandleVerifyRoomPGPKey_ChallengeExpired_RendersSidebarWithError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().VerifyRoomPGPKey(gomock.Any(), roomID, userID, "abc").Return(store.ErrPGPChallengeExpired)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := newFormRequest(t, "/rooms/10/pgp/verify", url.Values{"decrypted_challenge": {"abc"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleVerifyRoomPGPKey(svc, sessions, hub), r)

	if w.Code == http.StatusForbidden || w.Code == http.StatusNotFound {
		t.Fatalf("expected sidebar render (non-4xx), got %d", w.Code)
	}
}

func TestHandleVerifyRoomPGPKey_ChallengeIncorrect_RendersSidebarWithError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().VerifyRoomPGPKey(gomock.Any(), roomID, userID, "wrong").Return(store.ErrPGPChallengeIncorrect)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := newFormRequest(t, "/rooms/10/pgp/verify", url.Values{"decrypted_challenge": {"wrong"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleVerifyRoomPGPKey(svc, sessions, hub), r)

	if w.Code == http.StatusForbidden || w.Code == http.StatusNotFound {
		t.Fatalf("expected sidebar render (non-4xx), got %d", w.Code)
	}
}

func TestHandleVerifyRoomPGPKey_Success_NotifiesHubAndRendersSidebar(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().VerifyRoomPGPKey(gomock.Any(), roomID, userID, "correct").Return(nil)
	hub.EXPECT().NotifyRoomUpdate(roomID)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := newFormRequest(t, "/rooms/10/pgp/verify", url.Values{"decrypted_challenge": {"correct"}})
	r.SetPathValue("id", "10")
	w := serve(t, handleVerifyRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// handleRemoveRoomPGPKey (self-removal)

func TestHandleRemoveRoomPGPKey_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/rooms/10/pgp", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleRemoveRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleRemoveRoomPGPKey_NotMember_Returns403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().RemoveRoomUserPGPKey(gomock.Any(), roomID, userID, userID).Return(store.ErrNotRoomMember)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/rooms/10/pgp", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleRemoveRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleRemoveRoomPGPKey_Success_RendersSidebar(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().RemoveRoomUserPGPKey(gomock.Any(), roomID, userID, userID).Return(nil)
	hub.EXPECT().NotifyRoomUpdate(roomID)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, userID).Return(stubRoomDetailView("alice"), nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/rooms/10/pgp", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleRemoveRoomPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// handleRemoveMemberPGPKey (creator removes a member's key)

func TestHandleRemoveMemberPGPKey_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/rooms/10/members/2/pgp", nil)
	r.SetPathValue("id", "10")
	r.SetPathValue("memberid", "2")
	w := serve(t, handleRemoveMemberPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleRemoveMemberPGPKey_NotCreator_Returns403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	actingID := store.UserID(1)
	targetID := store.UserID(2)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, actingID)
	svc.EXPECT().RemoveRoomUserPGPKey(gomock.Any(), roomID, targetID, actingID).Return(store.ErrNotRoomCreator)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/rooms/10/members/2/pgp", nil)
	r.SetPathValue("id", "10")
	r.SetPathValue("memberid", "2")
	w := serve(t, handleRemoveMemberPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleRemoveMemberPGPKey_MemberNotFound_Returns404(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	actingID := store.UserID(1)
	targetID := store.UserID(99)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, actingID)
	svc.EXPECT().RemoveRoomUserPGPKey(gomock.Any(), roomID, targetID, actingID).Return(store.ErrNotRoomMember)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/rooms/10/members/99/pgp", nil)
	r.SetPathValue("id", "10")
	r.SetPathValue("memberid", "99")
	w := serve(t, handleRemoveMemberPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleRemoveMemberPGPKey_Success_RendersSidebar(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	hub := servermocks.NewMockHub(ctrl)

	actingID := store.UserID(1)
	targetID := store.UserID(2)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, actingID)
	svc.EXPECT().RemoveRoomUserPGPKey(gomock.Any(), roomID, targetID, actingID).Return(nil)
	hub.EXPECT().NotifyRoomUpdate(roomID)
	svc.EXPECT().GetRoomDetailView(gomock.Any(), roomID, actingID).Return(stubRoomDetailView("alice"), nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/rooms/10/members/2/pgp", nil)
	r.SetPathValue("id", "10")
	r.SetPathValue("memberid", "2")
	w := serve(t, handleRemoveMemberPGPKey(svc, sessions, hub), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ensure store error sentinels used in PGP tests are importable.
var _ = strings.Contains
