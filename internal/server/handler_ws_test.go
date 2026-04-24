package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	servermocks "github.com/jchevertonwynne/ssanta/internal/server/mocks"
	"github.com/jchevertonwynne/ssanta/internal/store"
	"github.com/jchevertonwynne/ssanta/internal/ws"
)

const (
	msgTypePresence = "presence"
	msgTypeMessage  = "message"
	usernameAlice   = "alice"
	usernameBob     = "bob"
)

//nolint:funlen
func TestWebSocket_E2E_PreEncryptedMessageForwarded(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	sessions.EXPECT().Secure().Return(false).AnyTimes()

	userID := store.UserID(2)
	roomID := store.RoomID(10)
	verifiedAt := time.Now()

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, 0, true).AnyTimes()
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), userID).Return(0, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return(usernameAlice, nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userID, Username: usernameAlice, PGPPublicKey: "armoredkey", PGPVerifiedAt: &verifiedAt},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(true, nil).AnyTimes()
	svc.EXPECT().CreateMessage(gomock.Any(), roomID, userID, usernameAlice, gomock.Any(), false, gomock.Any(), true).Return(store.MessageID(1), nil).AnyTimes()

	hub := ws.NewChatHubWithLimits(ws.DefaultWSBurst, ws.DefaultWSRefillPerSec)
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
	if resp != nil {
		defer resp.Body.Close() //nolint:errcheck
	}
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const ciphertext = "-----BEGIN PGP MESSAGE-----\nfakeciphertext\n-----END PGP MESSAGE-----"
	msg := ws.ChatMessagePayload{Type: msgTypeMessage, Message: ciphertext, PreEncrypted: true}
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	payload := readNextNonPresenceMessage(t, conn)
	if payload.Type != msgTypeMessage {
		t.Fatalf("expected type=message, got %q", payload.Type)
	}
	if payload.Username != usernameAlice {
		t.Fatalf("expected username=alice, got %q", payload.Username)
	}
	if payload.Message != ciphertext {
		t.Fatalf("expected ciphertext forwarded unchanged, got %q", payload.Message)
	}
}

//nolint:funlen
func TestWebSocket_E2E_PreEncryptedMessageForwardedToAllMembers(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	sessions.EXPECT().Secure().Return(false).AnyTimes()

	roomID := store.RoomID(10)
	userA := store.UserID(2)
	userB := store.UserID(3)
	verifiedAt := time.Now()

	sessions.EXPECT().UserID(gomock.Any()).DoAndReturn(func(r *http.Request) (store.UserID, int, bool) {
		switch r.Header.Get("X-Test-User") {
		case usernameAlice:
			return userA, 0, true
		case usernameBob:
			return userB, 0, true
		default:
			return 0, 0, false
		}
	}).AnyTimes()

	svc.EXPECT().UserExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, gomock.Any()).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userA).Return(usernameAlice, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userB).Return(usernameBob, nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userA, Username: usernameAlice, PGPPublicKey: "keyA", PGPVerifiedAt: &verifiedAt},
		{ID: userB, Username: usernameBob, PGPPublicKey: ""},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(true, nil).AnyTimes()
	svc.EXPECT().CreateMessage(gomock.Any(), roomID, gomock.Any(), gomock.Any(), gomock.Any(), false, gomock.Any(), true).Return(store.MessageID(1), nil).AnyTimes()

	hub := ws.NewChatHubWithLimits(ws.DefaultWSBurst, ws.DefaultWSRefillPerSec)
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
		if resp != nil {
			defer resp.Body.Close() //nolint:errcheck
		}
		if err != nil {
			t.Fatalf("dial websocket %s: %v", userHeader, err)
		}
		return conn
	}

	connA := dial(usernameAlice)
	defer connA.Close() //nolint:errcheck
	connB := dial(usernameBob)
	defer connB.Close() //nolint:errcheck

	const ciphertext = "-----BEGIN PGP MESSAGE-----\nfakeciphertext\n-----END PGP MESSAGE-----"
	msg := ws.ChatMessagePayload{Type: msgTypeMessage, Message: ciphertext, PreEncrypted: true}
	b, err := json.Marshal(msg)
	require.NoError(t, err)
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
		p := readNextNonPresenceMessage(t, tc.conn)
		if p.Type != msgTypeMessage {
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

//nolint:funlen
func TestWebSocket_E2E_PlaintextRejectedInPGPRoom(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	sessions.EXPECT().Secure().Return(false).AnyTimes()

	roomID := store.RoomID(10)
	userA := store.UserID(2)
	userB := store.UserID(3)

	sessions.EXPECT().UserID(gomock.Any()).DoAndReturn(func(r *http.Request) (store.UserID, int, bool) {
		switch r.Header.Get("X-Test-User") {
		case usernameAlice:
			return userA, 0, true
		case usernameBob:
			return userB, 0, true
		default:
			return 0, 0, false
		}
	}).AnyTimes()

	svc.EXPECT().UserExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, gomock.Any()).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userA).Return(usernameAlice, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userB).Return(usernameBob, nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userA, Username: usernameAlice},
		{ID: userB, Username: usernameBob},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(true, nil).AnyTimes()

	hub := ws.NewChatHubWithLimits(ws.DefaultWSBurst, ws.DefaultWSRefillPerSec)
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
		if resp != nil {
			defer resp.Body.Close() //nolint:errcheck
		}
		if err != nil {
			t.Fatalf("dial websocket %s: %v", userHeader, err)
		}
		return conn
	}

	connA := dial(usernameAlice)
	defer connA.Close() //nolint:errcheck
	connB := dial(usernameBob)
	defer connB.Close() //nolint:errcheck

	// Send without pre_encrypted: true — server must reject this.
	msg := ws.ChatMessagePayload{Type: msgTypeMessage, Message: "hello plaintext"}
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	if err := connA.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	payloadA := readNextNonPresenceMessage(t, connA)
	if payloadA.Type != "system" {
		t.Fatalf("expected type=system, got %q", payloadA.Type)
	}
	if !strings.Contains(payloadA.Message, "PGP") {
		t.Fatalf("expected system message to mention PGP, got %q", payloadA.Message)
	}

	assertNoNonPresenceMessage(t, connB, 200*time.Millisecond)
}

func TestWebSocket_E2E_NonMemberRejected403(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	sessions.EXPECT().Secure().Return(false).AnyTimes()

	userID := store.UserID(2)
	roomID := store.RoomID(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, 0, true).AnyTimes()
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), userID).Return(0, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, false, nil).AnyTimes()

	hub := ws.NewChatHubWithLimits(ws.DefaultWSBurst, ws.DefaultWSRefillPerSec)
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
		_ = conn.Close()
		t.Fatalf("expected dial to fail")
	}
	if resp == nil {
		t.Fatalf("expected HTTP response on failure")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", resp.StatusCode)
	}
}

func TestWebSocket_E2E_DisconnectUser_SendsKicked(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	sessions.EXPECT().Secure().Return(false).AnyTimes()

	userID := store.UserID(2)
	roomID := store.RoomID(10)

	sessions.EXPECT().UserID(gomock.Any()).Return(userID, 0, true).AnyTimes()
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), userID).Return(0, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, userID).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userID).Return(usernameAlice, nil).AnyTimes()

	hub := ws.NewChatHubWithLimits(ws.DefaultWSBurst, ws.DefaultWSRefillPerSec)
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
	if resp != nil {
		defer resp.Body.Close() //nolint:errcheck
	}
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	// Wait for the initial presence broadcast, which is sent only after the
	// client is registered with the hub. This prevents a race where
	// DisconnectUser runs before registration completes and finds no clients.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read initial presence: %v", err)
	}

	hub.DisconnectUser(roomID, userID)

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	payload := readNextNonPresenceMessage(t, conn)
	if payload.Type != "kicked" {
		t.Fatalf("expected type=kicked, got %q", payload.Type)
	}
	if payload.Message == "" {
		t.Fatalf("expected kicked message")
	}
}

//nolint:funlen
func TestWebSocket_E2E_WhisperPlaintext_OnlySenderAndTargetReceive(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	sessions.EXPECT().Secure().Return(false).AnyTimes()

	roomID := store.RoomID(10)
	userA := store.UserID(2)
	userB := store.UserID(3)
	userC := store.UserID(4)

	sessions.EXPECT().UserID(gomock.Any()).DoAndReturn(func(r *http.Request) (store.UserID, int, bool) {
		switch r.Header.Get("X-Test-User") {
		case usernameAlice:
			return userA, 0, true
		case usernameBob:
			return userB, 0, true
		case "charlie":
			return userC, 0, true
		default:
			return 0, 0, false
		}
	}).AnyTimes()

	svc.EXPECT().UserExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, gomock.Any()).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userA).Return(usernameAlice, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userB).Return(usernameBob, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userC).Return("charlie", nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userA, Username: usernameAlice},
		{ID: userB, Username: usernameBob},
		{ID: userC, Username: "charlie"},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(false, nil).AnyTimes()
	svc.EXPECT().CreateMessage(gomock.Any(), roomID, userA, usernameAlice, "secret", true, gomock.Any(), false).Return(store.MessageID(1), nil).AnyTimes()

	hub := ws.NewChatHubWithLimits(ws.DefaultWSBurst, ws.DefaultWSRefillPerSec)
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
		defer func() {
			_ = resp.Body.Close()
		}()
		return conn
	}

	connA := dial(usernameAlice)
	defer connA.Close() //nolint:errcheck
	connB := dial(usernameBob)
	defer connB.Close() //nolint:errcheck
	connC := dial("charlie")
	defer connC.Close() //nolint:errcheck

	// Alice whispers to bob
	msg := ws.ChatMessagePayload{Type: msgTypeMessage, Message: "secret", TargetUserID: userB}
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	if err := connA.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Alice should receive the whisper
	_ = connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	payloadA := readNextNonPresenceMessage(t, connA)
	if payloadA.Type != msgTypeMessage || payloadA.Message != "secret" || !payloadA.Whisper {
		t.Fatalf("alice: expected whisper message 'secret', got type=%q msg=%q whisper=%v", payloadA.Type, payloadA.Message, payloadA.Whisper)
	}

	// Bob should receive the whisper
	_ = connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	payloadB := readNextNonPresenceMessage(t, connB)
	if payloadB.Type != msgTypeMessage || payloadB.Message != "secret" || !payloadB.Whisper {
		t.Fatalf("bob: expected whisper message 'secret', got type=%q msg=%q whisper=%v", payloadB.Type, payloadB.Message, payloadB.Whisper)
	}

	// Charlie should NOT receive anything (presence messages are allowed, but no chat message)
	assertNoNonPresenceMessage(t, connC, 200*time.Millisecond)
}

//nolint:funlen
func TestWebSocket_E2E_WhisperInvalidTarget_SystemError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	svc := servermocks.NewMockServerService(ctrl)
	sessions := servermocks.NewMockSessionManager(ctrl)
	sessions.EXPECT().Secure().Return(false).AnyTimes()

	roomID := store.RoomID(10)
	userA := store.UserID(2)
	userB := store.UserID(3)

	sessions.EXPECT().UserID(gomock.Any()).DoAndReturn(func(r *http.Request) (store.UserID, int, bool) {
		switch r.Header.Get("X-Test-User") {
		case usernameAlice:
			return userA, 0, true
		case usernameBob:
			return userB, 0, true
		default:
			return 0, 0, false
		}
	}).AnyTimes()

	svc.EXPECT().UserExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	svc.EXPECT().GetUserSessionVersion(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	svc.EXPECT().GetRoomAccess(gomock.Any(), roomID, gomock.Any()).Return(false, true, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userA).Return(usernameAlice, nil).AnyTimes()
	svc.EXPECT().GetUsername(gomock.Any(), userB).Return(usernameBob, nil).AnyTimes()
	svc.EXPECT().ListRoomMembersWithPGP(gomock.Any(), roomID).Return([]store.RoomMember{
		{ID: userA, Username: usernameAlice},
		{ID: userB, Username: usernameBob},
	}, nil).AnyTimes()
	svc.EXPECT().IsRoomPGPRequired(gomock.Any(), roomID).Return(false, nil).AnyTimes()

	hub := ws.NewChatHubWithLimits(ws.DefaultWSBurst, ws.DefaultWSRefillPerSec)
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
		defer func() {
			_ = resp.Body.Close()
		}()
		return conn
	}

	connA := dial(usernameAlice)
	defer connA.Close() //nolint:errcheck
	connB := dial(usernameBob)
	defer connB.Close() //nolint:errcheck

	// Alice whispers to a non-existent user
	msg := ws.ChatMessagePayload{Type: msgTypeMessage, Message: "secret", TargetUserID: 999}
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	if err := connA.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Alice should get a system error
	_ = connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	payloadA := readNextNonPresenceMessage(t, connA)
	if payloadA.Type != "system" {
		t.Fatalf("expected type=system, got %q", payloadA.Type)
	}
	if !strings.Contains(payloadA.Message, "not in this room") {
		t.Fatalf("expected error about user not in room, got %q", payloadA.Message)
	}

	// Bob should NOT receive anything (presence messages are allowed, but no chat message)
	assertNoNonPresenceMessage(t, connB, 200*time.Millisecond)
}

// readNextNonPresenceMessage reads from the WebSocket, skipping any presence messages,
// and returns the first non-presence message.
func readNextNonPresenceMessage(t *testing.T, conn *websocket.Conn) ws.ChatMessagePayload {
	t.Helper()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read message: %v", err)
		}
		var payload ws.ChatMessagePayload
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload.Type != msgTypePresence {
			return payload
		}
	}
}

// assertNoNonPresenceMessage reads from the WebSocket for the given timeout, skipping
// presence messages, and fails the test if any non-presence message arrives.
func assertNoNonPresenceMessage(t *testing.T, conn *websocket.Conn, timeout time.Duration) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return // deadline exceeded or connection closed — no non-presence message received
		}
		var p struct {
			Type string `json:"type"`
		}
		if jsonErr := json.Unmarshal(data, &p); jsonErr != nil || p.Type != msgTypePresence {
			t.Fatalf("expected no non-presence message, got: %s", data)
		}
	}
}
