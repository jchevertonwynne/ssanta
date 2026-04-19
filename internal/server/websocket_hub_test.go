package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ProtonMail/gopenpgp/v3/crypto"
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

func TestWebSocket_E2E_MessageEncryptedToSelfWithKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	userID := int64(2)
	roomID := int64(10)

	// Key for recipient (self)
	armoredPub, privKey := mustGenerateTestKeyPair(t)

	// For websocket handler auth.
	sessions.EXPECT().UserID(gomock.Any()).Return(userID, true).AnyTimes()
	svc.EXPECT().UserExists(gomock.Any(), userID).Return(true, nil).AnyTimes()
	svc.EXPECT().IsRoomMember(gomock.Any(), roomID, userID).Return(true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return("alice", nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{{ID: userID, Username: "alice", PGPPublicKey: armoredPub}}, nil).AnyTimes()

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
	plaintext := mustDecryptArmored(t, privKey, payload.Message)
	if plaintext != "hello" {
		t.Fatalf("expected decrypted plaintext=hello, got %q", plaintext)
	}
}

func TestWebSocket_E2E_MessageOnlyDeliveredToUsersWithKeys(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)

	roomID := int64(10)

	userA := int64(2)
	userB := int64(3)

	pubA, privA := mustGenerateTestKeyPair(t)
	pubB, _ := mustGenerateTestKeyPair(t)

	// Session user based on header, so we can dial two websocket conns.
	sessions.EXPECT().UserID(gomock.Any()).DoAndReturn(func(r *http.Request) (int64, bool) {
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
	svc.EXPECT().IsRoomMember(gomock.Any(), roomID, gomock.Any()).Return(true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userA).Return("alice", nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userB).Return("bob", nil).AnyTimes()

	// Only alice has a key; bob should not receive anything.
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userA, Username: "alice", PGPPublicKey: pubA},
		{ID: userB, Username: "bob", PGPPublicKey: ""},
		// Extra keyed user not connected should not matter.
		{ID: 999, Username: "ghost", PGPPublicKey: pubB},
	}, nil).AnyTimes()

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
	defer connA.Close()
	connB := dial("bob")
	defer connB.Close()

	msg := ChatMessagePayload{Type: "message", Message: "hello"}
	b, _ := json.Marshal(msg)
	requireWrite := func(c *websocket.Conn) {
		if err := c.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	requireWrite(connA)

	_ = connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, gotA, err := connA.ReadMessage()
	if err != nil {
		t.Fatalf("read alice: %v", err)
	}
	var payloadA ChatMessagePayload
	if err := json.Unmarshal(gotA, &payloadA); err != nil {
		t.Fatalf("unmarshal alice: %v", err)
	}
	plaintext := mustDecryptArmored(t, privA, payloadA.Message)
	if plaintext != "hello" {
		t.Fatalf("expected decrypted plaintext=hello, got %q", plaintext)
	}

	_ = connB.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err = connB.ReadMessage()
	if err == nil {
		t.Fatalf("expected bob to receive no message")
	}
}

func mustGenerateTestKeyPair(t *testing.T) (armoredPublicKey string, privateKey *crypto.Key) {
	t.Helper()

	pgpHandle := crypto.PGP()
	priv, err := pgpHandle.KeyGeneration().AddUserId("test", "test@example.com").New().GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	pub, err := priv.ToPublic()
	if err != nil {
		t.Fatalf("to public: %v", err)
	}

	armoredPub, err := pub.Armor()
	if err != nil {
		t.Fatalf("armor public: %v", err)
	}

	return armoredPub, priv
}

func mustDecryptArmored(t *testing.T, privateKey *crypto.Key, armoredCiphertext string) string {
	t.Helper()

	pgpHandle := crypto.PGP()
	decHandle, err := pgpHandle.Decryption().DecryptionKey(privateKey).New()
	if err != nil {
		t.Fatalf("decrypt handle: %v", err)
	}
	decrypted, err := decHandle.Decrypt([]byte(armoredCiphertext), crypto.Armor)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return string(decrypted.Bytes())
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
