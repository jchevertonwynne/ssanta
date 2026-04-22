package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/jchevertonwynne/ssanta/internal/store"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

func TestHandleCreateOrGetDM_MissingPartnerID_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true)
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil)

	r := newFormRequest(t, "/dms", url.Values{})
	w := serve(t, handleCreateOrGetDM(svc, sessions), r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "partner_id required") {
		t.Fatalf("expected partner_id required error")
	}
}

func TestHandleCreateOrGetDM_URLEncoded_Redirects303(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	partnerID := store.UserID(2)
	roomID := store.RoomID(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true)
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil)
	svc.EXPECT().UserExists(gomock.Any(), partnerID).Return(true, nil)
	svc.EXPECT().GetOrCreateDMRoom(gomock.Any(), userID, partnerID).Return(roomID, nil)

	r := newFormRequest(t, "/dms", url.Values{"partner_id": {"2"}})
	w := serve(t, handleCreateOrGetDM(svc, sessions), r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected status 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/rooms/10" {
		t.Fatalf("expected redirect to /rooms/10, got %q", loc)
	}
}

func TestHandleCreateOrGetDM_Multipart_Redirects303(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	partnerID := store.UserID(2)
	roomID := store.RoomID(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true)
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil)
	svc.EXPECT().UserExists(gomock.Any(), partnerID).Return(true, nil)
	svc.EXPECT().GetOrCreateDMRoom(gomock.Any(), userID, partnerID).Return(roomID, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("partner_id", "2")
	_ = mw.Close()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/dms", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	w := serve(t, handleCreateOrGetDM(svc, sessions), r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected status 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/rooms/10" {
		t.Fatalf("expected redirect to /rooms/10, got %q", loc)
	}
}
