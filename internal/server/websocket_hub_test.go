package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/mock/gomock"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
)

func TestUpgraderCheckOrigin_AllowsEmptyOrigin(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	if !upgrader.CheckOrigin(r) {
		t.Fatalf("expected empty origin to be allowed")
	}
}

func TestUpgraderCheckOrigin_AllowsSameHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	r.Header.Set("Origin", "http://example.com")
	if !upgrader.CheckOrigin(r) {
		t.Fatalf("expected same-host origin to be allowed")
	}
}

func TestUpgraderCheckOrigin_RejectsDifferentHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	r.Header.Set("Origin", "http://evil.com")
	if upgrader.CheckOrigin(r) {
		t.Fatalf("expected different-host origin to be rejected")
	}
}

func TestChatHub_RegisterBroadcastUnregister(t *testing.T) {
	hub := NewChatHub()
	go hub.Run()

	client := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 10)}
	hub.register <- client

	// Wait for registration to be processed.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		room, ok := hub.rooms[1]
		hub.mu.RUnlock()
		if ok {
			room.mu.RLock()
			count := len(room.clients)
			room.mu.RUnlock()
			if count == 1 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	payload := []byte(`{"type":"system","message":"hi"}`)
	hub.BroadcastToRoom(1, payload)

	select {
	case got := <-client.send:
		if string(got) != string(payload) {
			t.Fatalf("unexpected payload: %q", string(got))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for broadcast")
	}

	hub.unregister <- client

	// After unregister, send channel is closed.
	select {
	case _, ok := <-client.send:
		if ok {
			t.Fatalf("expected send channel to be closed")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for channel close")
	}

	// Ensure unregister was fully processed (room cleaned up) before stopping.
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		rooms := len(hub.rooms)
		hub.mu.RUnlock()
		if rooms == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.Stop()
}

func TestWebSocket_E2E_MessageBroadcastToSelf(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := int64(2)
	roomID := int64(10)

	// For websocket handler auth.
	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true).AnyTimes()
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil).AnyTimes()
	svc.EXPECT().IsRoomMember(gomock.Any(), roomID, userID).Return(true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial websocket: %v (http %d)", err, resp.StatusCode)
		}
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	msg := ChatMessagePayload{Type: "message", Message: "hello"}
	b, _ := json.Marshal(msg)
	if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var payload ChatMessagePayload
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Type != "message" {
		t.Fatalf("expected type=message, got %q", payload.Type)
	}
	if payload.Username != "alice" {
		t.Fatalf("expected username=alice, got %q", payload.Username)
	}
	if payload.Message != "hello" {
		t.Fatalf("expected message=hello, got %q", payload.Message)
	}
}

func TestWebSocket_E2E_NonMemberRejected403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := int64(2)
	roomID := int64(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true).AnyTimes()
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil).AnyTimes()
	svc.EXPECT().IsRoomMember(gomock.Any(), roomID, userID).Return(false, nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err == nil {
		conn.Close()
		t.Fatalf("expected dial to fail")
	}
	if resp == nil {
		t.Fatalf("expected HTTP response on failure")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", resp.StatusCode)
	}
}

func TestWebSocket_E2E_DisconnectUser_SendsKicked(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := int64(2)
	roomID := int64(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true).AnyTimes()
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil).AnyTimes()
	svc.EXPECT().IsRoomMember(gomock.Any(), roomID, userID).Return(true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial websocket: %v (http %d)", err, resp.StatusCode)
		}
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	hub.DisconnectUser(roomID, userID)

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var payload ChatMessagePayload
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Type != "kicked" {
		t.Fatalf("expected type=kicked, got %q", payload.Type)
	}
	if payload.Message == "" {
		t.Fatalf("expected kicked message")
	}
}

func TestChatHub_NotifyUser_FanoutToAllConnections(t *testing.T) {
	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	c1 := &ChatClient{hub: hub, roomID: 0, userID: 10, send: make(chan []byte, 10)}
	c2 := &ChatClient{hub: hub, roomID: 0, userID: 10, send: make(chan []byte, 10)}
	hub.register <- c1
	hub.register <- c2

	// Wait for both connections to be tracked.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		conns := len(hub.userConnections[10])
		hub.mu.RUnlock()
		if conns == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.NotifyUser(10, "invite_received", "")

	readOne := func(ch <-chan []byte) ChatMessagePayload {
		select {
		case b := <-ch:
			var p ChatMessagePayload
			_ = json.Unmarshal(b, &p)
			return p
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timeout")
			return ChatMessagePayload{}
		}
	}

	p1 := readOne(c1.send)
	p2 := readOne(c2.send)
	if p1.Type != "invite_received" || p2.Type != "invite_received" {
		t.Fatalf("expected fanout type invite_received")
	}
}
