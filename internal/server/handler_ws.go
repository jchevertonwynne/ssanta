package server

import (
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/ws"
)

//nolint:cyclop,funlen,gocognit
func handleWebSocket(hub *ws.ChatHub, svc WebSocketHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}

		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}

		// Check if user is a creator or member (allow both to access chat)
		isCreator, isMember, err := svc.GetRoomAccess(r.Context(), roomID, currentID)
		if err != nil {
			http.Error(w, "failed to check room access", http.StatusInternalServerError)
			return
		}

		if !isCreator && !isMember {
			http.Error(w, "must be a creator or member to access chat", http.StatusForbidden)
			return
		}

		// Get username
		username, err := svc.GetUsername(r.Context(), currentID)
		if err != nil {
			http.Error(w, "failed to get user info", http.StatusInternalServerError)
			return
		}

		ws.RunWS(hub, sessions, svc, currentID, username, roomID, w, r)

		// conn, err := websocketUpgrader(sessions.Secure()).Upgrade(w, r, nil)
		// if err != nil {
		// 	slog.Error("upgrade websocket", "err", err)
		// 	return
		// }

		// // Catch up on missed messages before joining the live room.
		// lastSeenStr := r.URL.Query().Get("last_seen_id")
		// var lastSeenID store.MessageID
		// if lastSeenStr != "" {
		// 	lastSeenID = 0
		// } else {
		// 	var parsed int64
		// 	parsed, err = strconv.ParseInt(lastSeenStr, 10, 64)
		// 	lastSeenID = store.MessageID(parsed)
		// }
		// if err == nil && lastSeenID > 0 {
		// 	catchUp, err := svc.ListMessagesAfterID(r.Context(), roomID, currentID, lastSeenID, 200)
		// 	if err != nil {
		// 		slog.Error("list messages after id", "err", err, "room_id", roomID, "user_id", currentID) //nolint:gosec
		// 	}
		// 	for _, m := range catchUp {
		// 		msg := ChatMessagePayload{
		// 			Type:         WSMsgTypeMessage,
		// 			ID:           m.ID,
		// 			Username:     m.Username,
		// 			Message:      m.Message,
		// 			CreatedAt:    m.CreatedAt,
		// 			Whisper:      m.Whisper,
		// 			PreEncrypted: m.PreEncrypted,
		// 		}
		// 		if err := conn.WriteJSON(msg); err != nil {
		// 			slog.Error("write catch-up message", "err", err, "room_id", roomID, "user_id", currentID) //nolint:gosec
		// 			_ = conn.Close()
		// 			return
		// 		}
		// 	}
		// }

		// client := &ChatClient{
		// 	hub:      hub,
		// 	conn:     conn,
		// 	send:     make(chan []byte, 256),
		// 	roomID:   roomID,
		// 	userID:   currentID,
		// 	username: username,
		// 	svc:      svc,
		// 	bucket:   newTokenBucket(float64(hub.msgBurst), hub.msgRefill),
		// }

		// if !hub.tryRegister(client) {
		// 	_ = conn.Close()
		// 	return
		// }

		// hub.wg.Add(2)
		// go client.writePump()
		// //nolint: contextcheck // this is fine
		// go client.readPump(context.WithValue(hub.lifetimeCtx, ctxKeyWSSide, "readPump"))
	}
}

func handleContentWebSocket(hub *ws.ChatHub, svc WebSocketHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}

		// Get username
		username, err := svc.GetUsername(r.Context(), currentID)
		if err != nil {
			http.Error(w, "failed to get user info", http.StatusInternalServerError)
			return
		}

		ws.RunContentWS(hub, sessions, currentID, username, w, r)

		// conn, err := ws.WebsocketUpgrader(sessions.Secure(), w, r)
		// if err != nil {
		// 	slog.Error("upgrade websocket", "err", err)
		// 	return
		// }

		// client := &ChatClient{
		// 	hub:      hub,
		// 	conn:     conn,
		// 	send:     make(chan []byte, 256),
		// 	roomID:   0, // Not in a room, just on content page
		// 	userID:   currentID,
		// 	username: username,
		// 	bucket:   newTokenBucket(float64(hub.msgBurst), hub.msgRefill),
		// }

		// if !hub.tryRegister(client) {
		// 	_ = conn.Close()
		// 	return
		// }

		// hub.wg.Add(2)
		// go client.writePump()
		// //nolint: contextcheck // this is fine
		// go client.readPump(context.WithValue(hub.lifetimeCtx, ctxKeyWSSide, "readPump"))
	}
}