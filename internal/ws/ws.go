package ws

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/websocket"

	"github.com/jchevertonwynne/ssanta/internal/model"
)

func RunWS(hub *ChatHub, sessions SessionReader, svc Service, currentID model.UserID, username string, roomID model.RoomID, w http.ResponseWriter, r *http.Request) {
	conn, err := websocketUpgrader(sessions.Secure()).Upgrade(w, r, nil)
	if err != nil {
		slog.Error("upgrade websocket", "err", err)
		return
	}

	// Catch up on missed messages before joining the live room.
	lastSeenStr := r.URL.Query().Get("last_seen_id")
	var lastSeenID model.MessageID
	if lastSeenStr != "" {
		lastSeenID = 0
	} else {
		var parsed int64
		parsed, err = strconv.ParseInt(lastSeenStr, 10, 64)
		lastSeenID = model.MessageID(parsed)
	}
	if err == nil && lastSeenID > 0 {
		catchUp, err := svc.ListMessagesAfterID(r.Context(), roomID, currentID, lastSeenID, 200)
		if err != nil {
			slog.Error("list messages after id", "err", err, "room_id", roomID, "user_id", currentID)
		}
		for _, m := range catchUp {
			msg := ChatMessagePayload{
				Type:         MsgTypeMessage,
				ID:           m.ID,
				Username:     m.Username,
				Message:      m.Message,
				CreatedAt:    m.CreatedAt,
				Whisper:      m.Whisper,
				PreEncrypted: m.PreEncrypted,
			}
			if err := conn.WriteJSON(msg); err != nil {
				slog.Error("write catch-up message", "err", err, "room_id", roomID, "user_id", currentID)
				_ = conn.Close()
				return
			}
		}
	}

	client := &ChatClient{
		hub:      hub,
		conn:     conn,
		send:     make(chan []byte, 256),
		roomID:   roomID,
		userID:   currentID,
		username: username,
		svc:      svc,
		bucket:   newTokenBucket(float64(hub.msgBurst), hub.msgRefill),
	}

	if !hub.tryRegister(client) {
		_ = conn.Close()
		return
	}

	hub.wg.Go(client.writePump)
	hub.wg.Go(func() { //nolint:contextcheck
		client.readPump(context.WithValue(hub.lifetimeCtx, ctxKeyWSSide, "readPump"))
	})
}

func RunContentWS(hub *ChatHub, sessions SessionReader, currentID model.UserID, username string, w http.ResponseWriter, r *http.Request) {
	conn, err := websocketUpgrader(sessions.Secure()).Upgrade(w, r, nil)
	if err != nil {
		slog.Error("upgrade websocket", "err", err)
		return
	}

	client := &ChatClient{
		hub:      hub,
		conn:     conn,
		send:     make(chan []byte, 256),
		roomID:   0, // Not in a room, just on content page
		userID:   currentID,
		username: username,
		bucket:   newTokenBucket(float64(hub.msgBurst), hub.msgRefill),
	}

	if !hub.tryRegister(client) {
		_ = conn.Close()
		return
	}

	hub.wg.Go(client.writePump)
	hub.wg.Go(func() {
		client.readPump(context.WithValue(hub.lifetimeCtx, ctxKeyWSSide, "readPump"))
	})
}

func websocketUpgrader(secure bool) *websocket.Upgrader {
	return &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return !secure
			}
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return u.Host == r.Host
		},
	}
}
