package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/mock/gomock"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

func TestHandleHealth_OK(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockHealthService(ctrl)
	svc.EXPECT().Ping(gomock.Any()).Return(nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)

	h := handleHealth(svc)
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("expected status=ok, got %q", payload["status"])
	}
}

func TestHandleHealth_DBUnavailable(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockHealthService(ctrl)
	svc.EXPECT().Ping(gomock.Any()).Return(errors.New("db down")) //nolint:err113 // test error

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)

	h := handleHealth(svc)
	h.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if payload["status"] != "db_unavailable" {
		t.Fatalf("expected status=db_unavailable, got %q", payload["status"])
	}
}
