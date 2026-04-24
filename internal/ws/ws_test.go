package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	msgTypePresence = "presence"
	msgTypeMessage  = "message"
	usernameAlice   = "alice"
	usernameBob     = "bob"
)

func TestUpgraderCheckOrigin_AllowsEmptyOrigin(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", nil)
	if !websocketUpgrader(false).CheckOrigin(r) {
		t.Fatalf("expected empty origin to be allowed")
	}
}

func TestUpgraderCheckOrigin_AllowsSameHost(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", nil)
	r.Header.Set("Origin", "http://example.com")
	if !websocketUpgrader(false).CheckOrigin(r) {
		t.Fatalf("expected same-host origin to be allowed")
	}
}

func TestUpgraderCheckOrigin_RejectsDifferentHost(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", nil)
	r.Header.Set("Origin", "http://evil.com")
	if websocketUpgrader(false).CheckOrigin(r) {
		t.Fatalf("expected different-host origin to be rejected")
	}
}

func TestUpgraderCheckOrigin_RejectsEmptyOriginWhenSecure(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", nil)
	if websocketUpgrader(true).CheckOrigin(r) {
		t.Fatalf("expected empty origin to be rejected when secure")
	}
}

//nolint:funlen
func TestChatHub_RegisterBroadcastUnregister(t *testing.T) {
	t.Parallel()
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

	// Drain the presence broadcast fired on registration.
	select {
	case msg := <-client.send:
		var p struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(msg, &p)
		if p.Type != msgTypePresence {
			t.Fatalf("expected presence broadcast after registration, got: %s", msg)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for initial presence broadcast")
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

func TestChatHub_DisconnectRoom_EvictsClientsAndEmitsRoomDeleted(t *testing.T) {
	t.Parallel()
	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	client := &ChatClient{hub: hub, roomID: 42, userID: 7, send: make(chan []byte, 4)}
	hub.register <- client

	// Wait until the hub processed the registration (room entry exists).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		_, ok := hub.rooms[42]
		hub.mu.RUnlock()
		if ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Drain the initial presence broadcast fired on registration.
	select {
	case <-client.send:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for initial presence broadcast")
	}

	hub.DisconnectRoom(42)

	// First message should be the room_deleted notice.
	select {
	case msg, ok := <-client.send:
		if !ok {
			t.Fatalf("expected room_deleted before channel close")
		}
		var p struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &p); err != nil || p.Type != "room_deleted" {
			t.Fatalf("unexpected frame: %q err=%v", msg, err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for room_deleted frame")
	}

	// Channel closes immediately after.
	select {
	case _, ok := <-client.send:
		if ok {
			t.Fatalf("expected send channel closed")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for channel close")
	}

	hub.mu.RLock()
	_, stillThere := hub.rooms[42]
	hub.mu.RUnlock()
	if stillThere {
		t.Fatalf("room 42 should have been removed")
	}
}


func TestChatHub_NotifyUser_FanoutToAllConnections(t *testing.T) {
	t.Parallel()
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

