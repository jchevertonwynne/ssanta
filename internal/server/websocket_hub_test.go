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
	"github.com/jchevertonwynne/ssanta/internal/store"
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

func TestWebSocket_E2E_PreEncryptedMessageForwarded(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(2)
	roomID := store.RoomID(10)
	verifiedAt := time.Now()

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true).AnyTimes()
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userID, Username: "alice", PGPPublicKey: "armoredkey", PGPVerifiedAt: &verifiedAt},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(true, nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close() //nolint:errcheck

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial websocket: %v (http %d)", err, resp.StatusCode)
			_ = resp.Body.Close() //nolint:errcheck
		}
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const ciphertext = "-----BEGIN PGP MESSAGE-----\nfakeciphertext\n-----END PGP MESSAGE-----"
	msg := ChatMessagePayload{Type: "message", Message: ciphertext, PreEncrypted: true}
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
	if payload.Message != ciphertext {
		t.Fatalf("expected ciphertext forwarded unchanged, got %q", payload.Message)
	}
}

func TestWebSocket_E2E_PreEncryptedMessageForwardedToAllMembers(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	roomID := store.RoomID(10)
	userA := store.UserID(2)
	userB := store.UserID(3)
	verifiedAt := time.Now()

	sessions.EXPECT().UserID(gomock.Any()).DoAndReturn(func(r *http.Request) (store.UserID, bool) {
		switch r.Header.Get("X-Test-User") {
		case "alice":
			return userA, true
		case "bob":
			return userB, true
		default:
			return 0, false
		}
	}).AnyTimes()

	svc.EXPECT().UserExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, gomock.Any()).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userA).Return("alice", nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userB).Return("bob", nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userA, Username: "alice", PGPPublicKey: "keyA", PGPVerifiedAt: &verifiedAt},
		{ID: userB, Username: "bob", PGPPublicKey: ""},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(true, nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close() //nolint:errcheck

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"

	dial := func(userHeader string) *websocket.Conn {
		hdr := http.Header{}
		hdr.Set("Origin", srv.URL)
		hdr.Set("X-Test-User", userHeader)
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
		if err != nil {
			if resp != nil {
				t.Fatalf("dial websocket %s: %v (http %d)", userHeader, err, resp.StatusCode)
				_ = resp.Body.Close() //nolint:errcheck
			}
			t.Fatalf("dial websocket %s: %v", userHeader, err)
		}
		return conn
	}

	connA := dial("alice")
	defer connA.Close() //nolint:errcheck
	connB := dial("bob")
	defer connB.Close() //nolint:errcheck

	const ciphertext = "-----BEGIN PGP MESSAGE-----\nfakeciphertext\n-----END PGP MESSAGE-----"
	msg := ChatMessagePayload{Type: "message", Message: ciphertext, PreEncrypted: true}
	b, _ := json.Marshal(msg)
	if err := connA.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, tc := range []struct {
		conn *websocket.Conn
		name string
	}{
		{connA, "alice"},
		{connB, "bob"},
	} {
		_ = tc.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, got, err := tc.conn.ReadMessage()
		if err != nil {
			t.Fatalf("read %s: %v", tc.name, err)
		}
		var p ChatMessagePayload
		if err := json.Unmarshal(got, &p); err != nil {
			t.Fatalf("unmarshal %s: %v", tc.name, err)
		}
		if p.Type != "message" {
			t.Fatalf("%s: expected type=message, got %q", tc.name, p.Type)
		}
		if p.Username != "alice" {
			t.Fatalf("%s: expected username=alice, got %q", tc.name, p.Username)
		}
		if p.Message != ciphertext {
			t.Fatalf("%s: expected ciphertext forwarded unchanged, got %q", tc.name, p.Message)
		}
	}
}

func TestWebSocket_E2E_PlaintextRejectedInPGPRoom(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	roomID := store.RoomID(10)
	userA := store.UserID(2)
	userB := store.UserID(3)

	sessions.EXPECT().UserID(gomock.Any()).DoAndReturn(func(r *http.Request) (store.UserID, bool) {
		switch r.Header.Get("X-Test-User") {
		case "alice":
			return userA, true
		case "bob":
			return userB, true
		default:
			return 0, false
		}
	}).AnyTimes()

	svc.EXPECT().UserExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, gomock.Any()).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userA).Return("alice", nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userB).Return("bob", nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userA, Username: "alice"},
		{ID: userB, Username: "bob"},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(true, nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close() //nolint:errcheck

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"

	dial := func(userHeader string) *websocket.Conn {
		hdr := http.Header{}
		hdr.Set("Origin", srv.URL)
		hdr.Set("X-Test-User", userHeader)
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
		if err != nil {
			if resp != nil {
				t.Fatalf("dial websocket %s: %v (http %d)", userHeader, err, resp.StatusCode)
				_ = resp.Body.Close() //nolint:errcheck
			}
			t.Fatalf("dial websocket %s: %v", userHeader, err)
		}
		return conn
	}

	connA := dial("alice")
	defer connA.Close() //nolint:errcheck
	connB := dial("bob")
	defer connB.Close() //nolint:errcheck

	// Send without pre_encrypted: true — server must reject this.
	msg := ChatMessagePayload{Type: "message", Message: "hello plaintext"}
	b, _ := json.Marshal(msg)
	if err := connA.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, gotA, err := connA.ReadMessage()
	if err != nil {
		t.Fatalf("read alice: %v", err)
	}
	var payloadA ChatMessagePayload
	if err := json.Unmarshal(gotA, &payloadA); err != nil {
		t.Fatalf("unmarshal alice: %v", err)
	}
	if payloadA.Type != "system" {
		t.Fatalf("expected type=system, got %q", payloadA.Type)
	}
	if !strings.Contains(payloadA.Message, "PGP") {
		t.Fatalf("expected system message to mention PGP, got %q", payloadA.Message)
	}

	_ = connB.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err = connB.ReadMessage()
	if err == nil {
		t.Fatalf("expected bob to receive no message")
	}
}

func TestWebSocket_E2E_NonMemberRejected403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := store.UserID(2)
	roomID := store.RoomID(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true).AnyTimes()
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, false, nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close() //nolint:errcheck

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err == nil {
		conn.Close() //nolint:errcheck
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

	userID := store.UserID(2)
	roomID := store.RoomID(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true).AnyTimes()
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close() //nolint:errcheck

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial websocket: %v (http %d)", err, resp.StatusCode)
			_ = resp.Body.Close() //nolint:errcheck
		}
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close() //nolint:errcheck

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

func TestWebSocket_E2E_WhisperPlaintext_OnlySenderAndTargetReceive(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	roomID := store.RoomID(10)
	userA := store.UserID(2)
	userB := store.UserID(3)
	userC := store.UserID(4)

	sessions.EXPECT().UserID(gomock.Any()).DoAndReturn(func(r *http.Request) (store.UserID, bool) {
		switch r.Header.Get("X-Test-User") {
		case "alice":
			return userA, true
		case "bob":
			return userB, true
		case "charlie":
			return userC, true
		default:
			return 0, false
		}
	}).AnyTimes()

	svc.EXPECT().UserExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, gomock.Any()).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userA).Return("alice", nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userB).Return("bob", nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userC).Return("charlie", nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userA, Username: "alice"},
		{ID: userB, Username: "bob"},
		{ID: userC, Username: "charlie"},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(false, nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"

	dial := func(userHeader string) *websocket.Conn {
		hdr := http.Header{}
		hdr.Set("Origin", srv.URL)
		hdr.Set("X-Test-User", userHeader)
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
		if err != nil {
			if resp != nil {
				t.Fatalf("dial websocket %s: %v (http %d)", userHeader, err, resp.StatusCode)
			}
			t.Fatalf("dial websocket %s: %v", userHeader, err)
		}
		return conn
	}

	connA := dial("alice")
	defer connA.Close() //nolint:errcheck
	connB := dial("bob")
	defer connB.Close() //nolint:errcheck
	connC := dial("charlie")
	defer connC.Close() //nolint:errcheck

	// Alice whispers to bob
	msg := ChatMessagePayload{Type: "message", Message: "secret", TargetUserID: userB}
	b, _ := json.Marshal(msg)
	if err := connA.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Alice should receive the whisper
	_ = connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, gotA, err := connA.ReadMessage()
	if err != nil {
		t.Fatalf("read alice: %v", err)
	}
	var payloadA ChatMessagePayload
	if err := json.Unmarshal(gotA, &payloadA); err != nil {
		t.Fatalf("unmarshal alice: %v", err)
	}
	if payloadA.Type != "message" || payloadA.Message != "secret" || !payloadA.Whisper {
		t.Fatalf("alice: expected whisper message 'secret', got type=%q msg=%q whisper=%v", payloadA.Type, payloadA.Message, payloadA.Whisper)
	}

	// Bob should receive the whisper
	_ = connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, gotB, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("read bob: %v", err)
	}
	var payloadB ChatMessagePayload
	if err := json.Unmarshal(gotB, &payloadB); err != nil {
		t.Fatalf("unmarshal bob: %v", err)
	}
	if payloadB.Type != "message" || payloadB.Message != "secret" || !payloadB.Whisper {
		t.Fatalf("bob: expected whisper message 'secret', got type=%q msg=%q whisper=%v", payloadB.Type, payloadB.Message, payloadB.Whisper)
	}

	// Charlie should NOT receive anything
	_ = connC.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err = connC.ReadMessage()
	if err == nil {
		t.Fatalf("expected charlie to receive no message")
	}
}

func TestWebSocket_E2E_WhisperInvalidTarget_SystemError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	roomID := store.RoomID(10)
	userA := store.UserID(2)
	userB := store.UserID(3)

	sessions.EXPECT().UserID(gomock.Any()).DoAndReturn(func(r *http.Request) (store.UserID, bool) {
		switch r.Header.Get("X-Test-User") {
		case "alice":
			return userA, true
		case "bob":
			return userB, true
		default:
			return 0, false
		}
	}).AnyTimes()

	svc.EXPECT().UserExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, gomock.Any()).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userA).Return("alice", nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userB).Return("bob", nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userA, Username: "alice"},
		{ID: userB, Username: "bob"},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(false, nil).AnyTimes()

	hub := NewChatHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rooms/10/ws"

	dial := func(userHeader string) *websocket.Conn {
		hdr := http.Header{}
		hdr.Set("Origin", srv.URL)
		hdr.Set("X-Test-User", userHeader)
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
		if err != nil {
			if resp != nil {
				t.Fatalf("dial websocket %s: %v (http %d)", userHeader, err, resp.StatusCode)
			}
			t.Fatalf("dial websocket %s: %v", userHeader, err)
		}
		return conn
	}

	connA := dial("alice")
	defer connA.Close() //nolint:errcheck
	connB := dial("bob")
	defer connB.Close() //nolint:errcheck

	// Alice whispers to a non-existent user
	msg := ChatMessagePayload{Type: "message", Message: "secret", TargetUserID: 999}
	b, _ := json.Marshal(msg)
	if err := connA.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Alice should get a system error
	_ = connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, gotA, err := connA.ReadMessage()
	if err != nil {
		t.Fatalf("read alice: %v", err)
	}
	var payloadA ChatMessagePayload
	if err := json.Unmarshal(gotA, &payloadA); err != nil {
		t.Fatalf("unmarshal alice: %v", err)
	}
	if payloadA.Type != "system" {
		t.Fatalf("expected type=system, got %q", payloadA.Type)
	}
	if !strings.Contains(payloadA.Message, "not in this room") {
		t.Fatalf("expected error about user not in room, got %q", payloadA.Message)
	}

	// Bob should NOT receive anything
	_ = connB.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err = connB.ReadMessage()
	if err == nil {
		t.Fatalf("expected bob to receive no message")
	}
}
