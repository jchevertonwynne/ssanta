package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/jchevertonwynne/ssanta/internal/model"
	"github.com/jchevertonwynne/ssanta/internal/store"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

// handleListMessages

func TestHandleListMessages_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/10/messages", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleListMessages(svc, sessions), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleListMessages_NotMember_Returns403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, false, nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/10/messages", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleListMessages(svc, sessions), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleListMessages_Success_ReturnsJSON(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, true, nil)
	svc.EXPECT().ListMessages(gomock.Any(), roomID, userID, model.MessageID(0), 50).Return([]model.Message{{ID: 1, Username: "alice", Message: "hi"}}, nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/10/messages", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleListMessages(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected JSON content-type, got %q", ct)
	}
	var msgs []messageResponse
	if err := json.NewDecoder(w.Body).Decode(&msgs); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Username != "alice" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
}

func TestHandleListMessages_QueryParams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		query    string
		beforeID model.MessageID
	}{
		{name: "limit clamped to 50 when over 200", query: "?limit=999", beforeID: 0},
		{name: "before_id parsed correctly", query: "?before_id=42", beforeID: 42},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc := servermocks.NewMockServerService(ctrl)
			sessions := servermocks.NewMockSessionManager(ctrl)

			userID := store.UserID(1)
			roomID := store.RoomID(10)
			expectLoggedIn(t, svc, sessions, userID)
			svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(true, false, nil)
			svc.EXPECT().ListMessages(gomock.Any(), roomID, userID, tc.beforeID, 50).Return(nil, nil)

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/10/messages"+tc.query, nil)
			r.SetPathValue("id", "10")
			w := serve(t, handleListMessages(svc, sessions), r)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
		})
	}
}

// handleSearchMessages

func TestHandleSearchMessages_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/10/messages/search?q=hello", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleSearchMessages(svc, sessions), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleSearchMessages_NotMember_Returns403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, false, nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/10/messages/search?q=hello", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleSearchMessages(svc, sessions), r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleSearchMessages_QueryTooShort_Returns400(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, true, nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/10/messages/search?q=a", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleSearchMessages(svc, sessions), r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleSearchMessages_QueryTooLong_Returns400(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, true, nil)

	query := strings.Repeat("x", 129)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/10/messages/search?q="+query, nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleSearchMessages(svc, sessions), r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleSearchMessages_Success_ReturnsJSON(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	roomID := store.RoomID(10)
	expectLoggedIn(t, svc, sessions, userID)
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, true, nil)
	svc.EXPECT().SearchMessages(gomock.Any(), roomID, userID, "hello", 50).Return([]model.Message{{ID: 5, Username: "bob", Message: "hello world"}}, nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/10/messages/search?q=hello", nil)
	r.SetPathValue("id", "10")
	w := serve(t, handleSearchMessages(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected JSON content-type, got %q", ct)
	}
	var msgs []messageResponse
	if err := json.NewDecoder(w.Body).Decode(&msgs); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Username != "bob" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
}
