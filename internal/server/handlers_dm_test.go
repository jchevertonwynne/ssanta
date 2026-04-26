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

	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/store"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

func TestHandleCreateOrGetDM_MissingPartnerID_Returns400(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	sessions.EXPECT().UserID(gomock.Any()).Return(userID, 0, true)
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), userID).Return(0, nil)

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
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	partnerID := store.UserID(2)
	roomID := store.RoomID(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, 0, true)
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), userID).Return(0, nil)
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
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	partnerID := store.UserID(2)
	roomID := store.RoomID(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, 0, true)
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), userID).Return(0, nil)
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

func TestEscapeHTML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"hello", "hello"},
		{"<script>", "&lt;script&gt;"},
		{"foo & bar", "foo &amp; bar"},
		{`"quoted"`, "&quot;quoted&quot;"},
		{"it's", "it&#39;s"},
		{"<>&\"'", "&lt;&gt;&amp;&quot;&#39;"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := escapeHTML(tt.input); got != tt.want {
				t.Fatalf("escapeHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHandleListDMs_Unauthorized_Returns401(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	sessions.EXPECT().UserID(gomock.Any()).Return(store.UserID(0), 0, false)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/dms", nil)
	w := serve(t, handleListDMs(svc, sessions), r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleListDMs_JSONFormat_ReturnsDMRooms(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	expectLoggedIn(t, svc, sessions, userID)

	svc.EXPECT().GetContentView(gomock.Any(), userID).Return(&service.ContentView{
		DMRooms: []service.DMRoomInfo{
			{RoomID: store.RoomID(10), PartnerName: "bob"},
			{RoomID: store.RoomID(11), PartnerName: "charlie"},
		},
	}, nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/dms?format=json", nil)
	w := serve(t, handleListDMs(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("expected json content-type, got %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"bob"`) {
		t.Fatalf("expected bob in response, got %q", body)
	}
}

func TestHandleListDMs_HTMLFormat_RendersList(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(1)
	expectLoggedIn(t, svc, sessions, userID)

	svc.EXPECT().GetContentView(gomock.Any(), userID).Return(&service.ContentView{
		DMRooms: []service.DMRoomInfo{
			{RoomID: store.RoomID(10), PartnerName: "<bob>"},
		},
	}, nil)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/dms", nil)
	w := serve(t, handleListDMs(svc, sessions), r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `&lt;bob&gt;`) {
		t.Fatalf("expected escaped bob in response, got %q", body)
	}
}
