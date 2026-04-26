package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jchevertonwynne/ssanta/internal/model"
)

const (
	msgTypePresence = "presence"
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
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
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
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
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
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
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

	drainAll(t, c1.send)
	drainAll(t, c2.send)

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

func TestChatHub_SendToRoomUser_TargetsOnlyMatchingUser(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()

	c1 := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 4)}
	c2 := &ChatClient{hub: hub, roomID: 1, userID: 20, send: make(chan []byte, 4)}
	hub.register <- c1
	hub.register <- c2

	waitForRoomClients(t, hub, 1, 2)
	drainAll(t, c1.send)
	drainAll(t, c2.send)

	payload := []byte(`{"type":"system","message":"hi"}`)
	hub.SendToRoomUser(1, 20, payload)

	select {
	case got := <-c2.send:
		if string(got) != string(payload) {
			t.Fatalf("unexpected payload for c2: %q", string(got))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for c2 message")
	}

	select {
	case <-c1.send:
		t.Fatalf("c1 should not have received a message")
	case <-time.After(100 * time.Millisecond):
		// expected
	}

	hub.unregister <- c1
	hub.unregister <- c2
	waitForRoomEmpty(t, hub, 1)
	hub.Stop()
}

func TestChatHub_DisconnectUser_SendsKickedAndRemoves(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()

	c1 := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 4)}
	hub.register <- c1

	waitForRoomClients(t, hub, 1, 1)
	drainAll(t, c1.send)

	hub.DisconnectUser(1, 10)

	select {
	case msg, ok := <-c1.send:
		if !ok {
			t.Fatalf("expected kicked frame before channel close")
		}
		var p ChatMessagePayload
		_ = json.Unmarshal(msg, &p)
		if p.Type != MsgTypeKicked {
			t.Fatalf("expected kicked, got %q", p.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for kicked frame")
	}

	select {
	case _, ok := <-c1.send:
		if ok {
			t.Fatalf("expected channel closed")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for channel close")
	}

	waitForRoomEmpty(t, hub, 1)
	hub.Stop()
}

func TestChatHub_KickSpectators_RemovesNonMembers(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()

	member := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 4)}
	spectator := &ChatClient{hub: hub, roomID: 1, userID: 20, send: make(chan []byte, 4)}
	hub.register <- member
	hub.register <- spectator

	waitForRoomClients(t, hub, 1, 2)
	drainAll(t, member.send)
	drainAll(t, spectator.send)

	members := map[model.UserID]struct{}{10: {}}
	hub.KickSpectators(1, members)

	select {
	case msg, ok := <-spectator.send:
		if !ok {
			t.Fatalf("expected kicked frame before channel close")
		}
		var p ChatMessagePayload
		_ = json.Unmarshal(msg, &p)
		if p.Type != MsgTypeKicked {
			t.Fatalf("expected kicked, got %q", p.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for spectator kicked")
	}

	select {
	case <-member.send:
		t.Fatalf("member should not have been kicked")
	case <-time.After(100 * time.Millisecond):
		// expected
	}

	hub.unregister <- member
	hub.unregister <- spectator
	waitForRoomEmpty(t, hub, 1)
	hub.Stop()
}

func TestChatHub_BroadcastSystemMessage(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()

	c1 := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 4)}
	hub.register <- c1

	waitForRoomClients(t, hub, 1, 1)
	drainAll(t, c1.send)

	hub.BroadcastSystemMessage(1, "hello world")

	select {
	case msg := <-c1.send:
		var p ChatMessagePayload
		_ = json.Unmarshal(msg, &p)
		if p.Type != MsgTypeSystem || p.Message != "hello world" {
			t.Fatalf("unexpected frame: type=%q msg=%q", p.Type, p.Message)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out")
	}

	hub.unregister <- c1
	waitForRoomEmpty(t, hub, 1)
	hub.Stop()
}

func TestChatHub_NotifyRoomUpdate(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()

	c1 := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 4)}
	hub.register <- c1

	waitForRoomClients(t, hub, 1, 1)
	drainAll(t, c1.send)

	hub.NotifyRoomUpdate(1)

	select {
	case msg := <-c1.send:
		var p ChatMessagePayload
		_ = json.Unmarshal(msg, &p)
		if p.Type != MsgTypeRefresh {
			t.Fatalf("expected refresh, got %q", p.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out")
	}

	hub.unregister <- c1
	waitForRoomEmpty(t, hub, 1)
	hub.Stop()
}

func TestChatHub_NotifyContentUpdate(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()

	c1 := &ChatClient{hub: hub, roomID: 0, userID: 10, send: make(chan []byte, 4)}
	hub.register <- c1

	// Wait for connection tracking.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		conns := len(hub.userConnections[10])
		hub.mu.RUnlock()
		if conns == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.NotifyContentUpdate(MsgTypeUsersUpdated)

	select {
	case msg := <-c1.send:
		var p ChatMessagePayload
		_ = json.Unmarshal(msg, &p)
		if p.Type != MsgTypeUsersUpdated {
			t.Fatalf("expected content_update, got %q", p.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out")
	}

	hub.unregister <- c1
	waitForRoomEmpty(t, hub, 1)
	hub.Stop()
}

func TestChatHub_HandleAccountDeletion(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()

	c1 := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 4)}
	hub.register <- c1

	waitForRoomClients(t, hub, 1, 1)
	drainAll(t, c1.send)

	hub.HandleAccountDeletion(10)

	select {
	case _, ok := <-c1.send:
		if ok {
			t.Fatalf("expected channel closed")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out")
	}

	hub.mu.RLock()
	_, stillThere := hub.userConnections[10]
	hub.mu.RUnlock()
	if stillThere {
		t.Fatalf("user connections should be removed")
	}

	waitForRoomEmpty(t, hub, 1)
	hub.Stop()
}

func TestChatHub_SetTypingStatus(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()

	c1 := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 4)}
	hub.register <- c1

	waitForRoomClients(t, hub, 1, 1)
	drainAll(t, c1.send)

	hub.SetTypingStatus(t.Context(), 1, 10, "alice", true)

	select {
	case msg := <-c1.send:
		var p ChatMessagePayload
		_ = json.Unmarshal(msg, &p)
		if p.Type != MsgTypeTyping {
			t.Fatalf("expected typing, got %q", p.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for typing")
	}

	hub.SetTypingStatus(t.Context(), 1, 10, "alice", false)

	select {
	case msg := <-c1.send:
		var p ChatMessagePayload
		_ = json.Unmarshal(msg, &p)
		if p.Type != MsgTypeStoppedTyping {
			t.Fatalf("expected stopped_typing, got %q", p.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for stopped_typing")
	}

	hub.unregister <- c1
	waitForRoomEmpty(t, hub, 1)
	hub.Stop()
}

//nolint:unparam
func waitForRoomEmpty(t *testing.T, hub *ChatHub, roomID model.RoomID) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		room, ok := hub.rooms[roomID]
		hub.mu.RUnlock()
		if !ok {
			return
		}
		room.mu.RLock()
		count := len(room.clients)
		room.mu.RUnlock()
		if count == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for room %d to be empty", roomID)
}

//nolint:unparam
func waitForRoomClients(t *testing.T, hub *ChatHub, roomID model.RoomID, want int) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		room, ok := hub.rooms[roomID]
		hub.mu.RUnlock()
		if ok {
			room.mu.RLock()
			count := len(room.clients)
			room.mu.RUnlock()
			if count == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d clients in room %d", want, roomID)
}

func drainAll(t *testing.T, ch <-chan []byte) {
	t.Helper()
	for {
		select {
		case <-ch:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

func TestChatHub_BroadcastRoomPresence_SendsToAllClients(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()

	c1 := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 4)}
	c2 := &ChatClient{hub: hub, roomID: 1, userID: 20, send: make(chan []byte, 4)}
	hub.register <- c1
	hub.register <- c2

	waitForRoomClients(t, hub, 1, 2)
	drainAll(t, c1.send)
	drainAll(t, c2.send)

	hub.BroadcastRoomPresence(1)

	readPresence := func(ch <-chan []byte) {
		t.Helper()
		select {
		case msg := <-ch:
			var p ChatMessagePayload
			_ = json.Unmarshal(msg, &p)
			if p.Type != MsgTypePresence {
				t.Fatalf("expected presence, got %q", p.Type)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out")
		}
	}
	readPresence(c1.send)
	readPresence(c2.send)

	hub.unregister <- c1
	hub.unregister <- c2
	waitForRoomEmpty(t, hub, 1)
	hub.Stop()
}

func TestChatHub_TryRegister_ReturnsFalseWhenStopped(t *testing.T) {
	t.Parallel()
	hub := NewChatHubWithLimits(DefaultWSBurst, DefaultWSRefillPerSec)
	go hub.Run()
	hub.Stop()

	client := &ChatClient{hub: hub, roomID: 1, userID: 10, send: make(chan []byte, 4)}
	if hub.tryRegister(client) {
		t.Fatal("expected tryRegister to return false when hub is stopped")
	}
}
