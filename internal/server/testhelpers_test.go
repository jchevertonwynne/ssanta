package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/store"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

func newFormRequest(t *testing.T, method, target string, values url.Values) *http.Request {
	t.Helper()
	body := values.Encode()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func serve(t *testing.T, h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func expectLoggedIn(t *testing.T, svc *servermocks.MockServerService, sessions *servermocks.MockSessionManager, userID store.UserID) {
	t.Helper()
	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true)
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil)
}

func stubContentView(username string) *service.ContentView {
	return &service.ContentView{
		CurrentUsername: username,
		Users:           nil,
		CreatedRooms:    nil,
		MemberRooms:     nil,
		Invites:         nil,
	}
}

func stubRoomDetailView(roomID store.RoomID, currentUsername string) *service.RoomDetailView {
	return &service.RoomDetailView{
		CurrentUsername: currentUsername,
		Room: store.RoomDetail{
			Room:            store.Room{ID: roomID, DisplayName: "room", CreatedAt: time.Time{}},
			CreatorID:       1,
			CreatorUsername: "creator",
		},
		IsCreator:      false,
		IsMember:       true,
		CanInvite:      true,
		Members:        nil,
		PendingInvites: nil,
	}
}
