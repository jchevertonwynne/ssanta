package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/jchevertonwynne/ssanta/internal/observability"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

const maxMessageLength = 4096

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	},
}

type ChatHub struct {
	rooms           map[store.RoomID]*ChatRoom
	userConnections map[store.UserID]map[*ChatClient]bool
	register        chan *ChatClient
	unregister      chan *ChatClient
	done            chan struct{}
	wg              sync.WaitGroup
	mu              sync.RWMutex
	typingStatus    map[store.RoomID]map[store.UserID]*typingSession // track who's typing
	typingSessionID int64
}

type typingSession struct {
	username   string
	lastActive time.Time
	cancelFunc context.CancelFunc
	sessionID  int64 // unique ID for this typing session
}

type ChatRoom struct {
	roomID  store.RoomID
	clients map[*ChatClient]bool
	mu      sync.RWMutex
}

type ChatClient struct {
	hub       *ChatHub
	conn      *websocket.Conn
	send      chan []byte
	closeOnce sync.Once
	roomID    store.RoomID
	userID    store.UserID
	username  string
	svc       WebSocketHandlersService
}

type ChatMessagePayload struct {
	Type         string       `json:"type"` // "message", "error"
	Username     string       `json:"username,omitempty"`
	Message      string       `json:"message,omitempty"`
	CreatedAt    time.Time    `json:"created_at,omitempty"`
	TargetUserID store.UserID `json:"target_user_id,omitempty"`
	Whisper      bool         `json:"whisper,omitempty"`
	PreEncrypted bool         `json:"pre_encrypted,omitempty"`
}

func NewChatHub() *ChatHub {
	return &ChatHub{
		rooms:           make(map[store.RoomID]*ChatRoom),
		userConnections: make(map[store.UserID]map[*ChatClient]bool),
		register:        make(chan *ChatClient),
		unregister:      make(chan *ChatClient),
		done:            make(chan struct{}),
		typingStatus:    make(map[store.RoomID]map[store.UserID]*typingSession),
	}
}

// tryRegister enqueues a client onto the hub's register channel, but bails
// out if the hub has been stopped — avoiding a goroutine leak / deadlock when
// an HTTP upgrade races with shutdown.
func (h *ChatHub) tryRegister(c *ChatClient) bool {
	select {
	case h.register <- c:
		return true
	case <-h.done:
		return false
	}
}

func (h *ChatHub) Stop() {
	close(h.done)

	// Close all WebSocket connections to unblock reads
	h.mu.Lock()
	for _, room := range h.rooms {
		room.mu.Lock()
		for client := range room.clients {
			client.conn.Close() //nolint:errcheck
		}
		room.mu.Unlock()
	}
	h.mu.Unlock()

	// Wait for all goroutines to finish with a timeout
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines finished
	case <-time.After(5 * time.Second):
		slog.Warn("websocket shutdown timeout - some goroutines may still be running")
	}
}

func (h *ChatHub) Run() {
	for {
		select {
		case <-h.done:
			h.mu.Lock()
			for _, room := range h.rooms {
				room.mu.Lock()
				for client := range room.clients {
					client.closeOnce.Do(func() { close(client.send) })
				}
				room.mu.Unlock()
			}
			h.rooms = make(map[store.RoomID]*ChatRoom)
			h.mu.Unlock()
			return

		case client := <-h.register:
			h.mu.Lock()
			// Track user connection
			if h.userConnections[client.userID] == nil {
				h.userConnections[client.userID] = make(map[*ChatClient]bool)
			}
			h.userConnections[client.userID][client] = true

			// Record active connection metric
			if metrics := observability.GetMetrics(); metrics != nil {
				metrics.WSActiveConnections.Add(context.Background(), 1)
			}
			slog.Info("websocket connected", "user_id", client.userID, "room_id", client.roomID)

			// If in a room, add to room
			if client.roomID > 0 {
				room, ok := h.rooms[client.roomID]
				if !ok {
					room = &ChatRoom{
						roomID:  client.roomID,
						clients: make(map[*ChatClient]bool),
					}
					h.rooms[client.roomID] = room
				}
				room.mu.Lock()
				room.clients[client] = true
				room.mu.Unlock()
			}
			h.mu.Unlock()
			if client.roomID > 0 {
				h.BroadcastRoomPresence(client.roomID)
			}

		case client := <-h.unregister:
			h.mu.Lock()
			// Remove from user connections.
			if connections, ok := h.userConnections[client.userID]; ok {
				if _, exists := connections[client]; exists {
					delete(connections, client)
					client.closeOnce.Do(func() { close(client.send) })

					// Record disconnection metric
					if metrics := observability.GetMetrics(); metrics != nil {
						metrics.WSActiveConnections.Add(context.Background(), -1)
					}
					slog.Info("websocket disconnected", "user_id", client.userID, "room_id", client.roomID)
				}
				if len(connections) == 0 {
					delete(h.userConnections, client.userID)
				}
			}
			// Remove from the room and delete the room atomically if empty.
			// Hold h.mu across the emptiness check + delete so a concurrent
			// register for the same roomID can't slot a client into a room
			// we're about to discard.
			if client.roomID > 0 {
				if room, ok := h.rooms[client.roomID]; ok {
					room.mu.Lock()
					delete(room.clients, client)
					empty := len(room.clients) == 0
					room.mu.Unlock()
					if empty {
						delete(h.rooms, client.roomID)
					}
				}

				// Clear typing status for this user in this room
				if typingRoom, ok := h.typingStatus[client.roomID]; ok {
					if session, ok := typingRoom[client.userID]; ok {
						session.cancelFunc()
						delete(typingRoom, client.userID)
					}
					if len(typingRoom) == 0 {
						delete(h.typingStatus, client.roomID)
					}
				}
			}
			h.mu.Unlock()
			if client.roomID > 0 {
				h.BroadcastRoomPresence(client.roomID)
			}
		}
	}
}

func (h *ChatHub) BroadcastRoomPresence(roomID store.RoomID) {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	if !ok {
		h.mu.RUnlock()
		return
	}
	room.mu.RLock()
	seen := make(map[store.UserID]bool)
	var onlineIDs []store.UserID
	for client := range room.clients {
		if !seen[client.userID] {
			seen[client.userID] = true
			onlineIDs = append(onlineIDs, client.userID)
		}
	}
	room.mu.RUnlock()
	h.mu.RUnlock()

	msg, err := json.Marshal(struct {
		Type          string         `json:"type"`
		OnlineUserIDs []store.UserID `json:"online_user_ids"`
	}{Type: "presence", OnlineUserIDs: onlineIDs})
	if err != nil {
		return
	}
	h.BroadcastToRoom(roomID, msg)
}

func (h *ChatHub) BroadcastToRoom(roomID store.RoomID, message []byte) {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	h.mu.RUnlock()

	if !ok {
		return
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	for client := range room.clients {
		select {
		case client.send <- message:
		default:
			client.closeOnce.Do(func() { close(client.send) })
			delete(room.clients, client)
		}
	}
}

func (h *ChatHub) SendToRoomUsers(roomID store.RoomID, perUserMessage map[store.UserID][]byte) {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	for client := range room.clients {
		msg, ok := perUserMessage[client.userID]
		if !ok {
			continue
		}
		select {
		case client.send <- msg:
		default:
			client.closeOnce.Do(func() { close(client.send) })
			delete(room.clients, client)
		}
	}
}

// OnlineUsersInRoom returns the set of user IDs with an active WebSocket
// connection to this room.
func (h *ChatHub) OnlineUsersInRoom(roomID store.RoomID) map[store.UserID]bool {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	h.mu.RUnlock()
	if !ok {
		return nil
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	online := make(map[store.UserID]bool, len(room.clients))
	for client := range room.clients {
		online[client.userID] = true
	}
	return online
}

func (h *ChatHub) DisconnectUser(roomID store.RoomID, userID store.UserID) {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	h.mu.RUnlock()

	if !ok {
		return
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	for client := range room.clients {
		if client.userID != userID {
			continue
		}
		// Enqueue a "kicked" frame, then close the send channel. writePump
		// will drain the queue, write the close frame, and shut the conn
		// down deterministically — no sleep needed.
		kickedMsg := ChatMessagePayload{
			Type:    "kicked",
			Message: "You have been removed from this room",
		}
		if msg, err := json.Marshal(kickedMsg); err == nil {
			select {
			case client.send <- msg:
			case <-time.After(time.Second):
				// send buffer wedged; fall through to close path.
			}
		}
		client.closeOnce.Do(func() { close(client.send) })
		delete(room.clients, client)
	}
}

func (h *ChatHub) BroadcastSystemMessage(roomID store.RoomID, message string) {
	sysMsg := ChatMessagePayload{
		Type:      "system",
		Message:   message,
		CreatedAt: time.Now(),
	}
	if msg, err := json.Marshal(sysMsg); err == nil {
		h.BroadcastToRoom(roomID, msg)
	}
}

func (h *ChatHub) NotifyRoomUpdate(roomID store.RoomID) {
	refreshMsg := ChatMessagePayload{
		Type: "refresh",
	}
	if msg, err := json.Marshal(refreshMsg); err == nil {
		h.BroadcastToRoom(roomID, msg)
	}
}

func (h *ChatHub) NotifyUser(userID store.UserID, msgType, message string) {
	h.mu.RLock()
	connections, ok := h.userConnections[userID]
	h.mu.RUnlock()

	if !ok || len(connections) == 0 {
		return
	}

	notifyMsg := ChatMessagePayload{
		Type:    msgType,
		Message: message,
	}
	if msg, err := json.Marshal(notifyMsg); err == nil {
		for client := range connections {
			select {
			case client.send <- msg:
			default:
			}
		}
	}
}

func (h *ChatHub) NotifyContentUpdate(msgType string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	notifyMsg := ChatMessagePayload{Type: msgType}
	msg, err := json.Marshal(notifyMsg)
	if err != nil {
		return
	}

	for _, connections := range h.userConnections {
		for client := range connections {
			if client.roomID != 0 {
				continue
			}
			select {
			case client.send <- msg:
			default:
			}
		}
	}
}

// HandleAccountDeletion disconnects all active connections for a user and
// notifies affected rooms so clients can refresh room state. This method is
// intentionally not on the public `Hub` interface; handlers call it via an
// optional interface assertion so tests using the mock Hub are unaffected.
func (h *ChatHub) HandleAccountDeletion(userID store.UserID) {
	var affectedRooms []store.RoomID

	h.mu.Lock()
	if conns, ok := h.userConnections[userID]; ok {
		for client := range conns {
			if client.roomID > 0 {
				affectedRooms = append(affectedRooms, client.roomID)

				if room, ok := h.rooms[client.roomID]; ok {
					room.mu.Lock()
					delete(room.clients, client)
					empty := len(room.clients) == 0
					room.mu.Unlock()
					if empty {
						delete(h.rooms, client.roomID)
					}
				}

				// Clear typing status for this user in the room
				if typingRoom, ok := h.typingStatus[client.roomID]; ok {
					if session, ok := typingRoom[client.userID]; ok {
						session.cancelFunc()
						delete(typingRoom, client.userID)
					}
					if len(typingRoom) == 0 {
						delete(h.typingStatus, client.roomID)
					}
				}
			}

			// Close the outgoing channel to terminate the connection's writePump.
			client.closeOnce.Do(func() { close(client.send) })

			// Update active connection metric
			if metrics := observability.GetMetrics(); metrics != nil {
				metrics.WSActiveConnections.Add(context.Background(), -1)
			}
		}
		delete(h.userConnections, userID)
	}
	h.mu.Unlock()

	// Deduplicate and notify affected rooms outside of the lock.
	roomSet := make(map[store.RoomID]struct{})
	for _, r := range affectedRooms {
		roomSet[r] = struct{}{}
	}
	for roomID := range roomSet {
		h.BroadcastRoomPresence(roomID)
		h.NotifyRoomUpdate(roomID)
	}
}

// SetTypingStatus updates the typing status for a user in a room
// isTyping=true means user is typing; isTyping=false means user stopped typing
// A 5-second timeout is automatically applied for typing status
func (h *ChatHub) SetTypingStatus(roomID store.RoomID, userID store.UserID, username string, isTyping bool) {
	h.mu.Lock()

	// Ensure room entry exists in typingStatus
	if h.typingStatus[roomID] == nil {
		h.typingStatus[roomID] = make(map[store.UserID]*typingSession)
	}

	// Cancel any existing typing timeout
	if session, exists := h.typingStatus[roomID][userID]; exists {
		session.cancelFunc()
	}

	if !isTyping {
		// User stopped typing, remove from tracking
		delete(h.typingStatus[roomID], userID)
		h.mu.Unlock()

		// Broadcast stopped_typing
		stoppedMsg := ChatMessagePayload{
			Type:     "stopped_typing",
			Username: username,
		}
		if msg, err := json.Marshal(stoppedMsg); err == nil {
			h.BroadcastToRoom(roomID, msg)
		}
	} else {
		// User is typing, set up with auto-timeout
		h.typingSessionID++
		sessionID := h.typingSessionID

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		h.typingStatus[roomID][userID] = &typingSession{
			username:   username,
			lastActive: time.Now(),
			cancelFunc: cancel,
			sessionID:  sessionID,
		}
		h.mu.Unlock()

		// Broadcast typing update
		typingMsg := ChatMessagePayload{
			Type:     "typing",
			Username: username,
		}
		if msg, err := json.Marshal(typingMsg); err == nil {
			h.BroadcastToRoom(roomID, msg)
		}

		// Wait for timeout and auto-clear
		go func() {
			<-ctx.Done()

			h.mu.Lock()
			// Only clear if it hasn't been updated (check sessionID)
			if session, ok := h.typingStatus[roomID][userID]; ok && session.sessionID == sessionID {
				delete(h.typingStatus[roomID], userID)
				h.mu.Unlock()

				// Broadcast stopped_typing
				stoppedMsg := ChatMessagePayload{
					Type:     "stopped_typing",
					Username: username,
				}
				if msg, err := json.Marshal(stoppedMsg); err == nil {
					h.BroadcastToRoom(roomID, msg)
				}
			} else {
				h.mu.Unlock()
			}
		}()
	}
}

func (c *ChatClient) readPump() {
	defer func() {
		select {
		case c.hub.unregister <- c:
		case <-c.hub.done:
			// Hub is stopping; avoid blocking if Run has exited.
		}
		c.conn.Close() //nolint:errcheck
		c.hub.wg.Done()
	}()

	c.conn.SetReadLimit(maxMessageLength * 2)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("websocket error", "err", err)
			}
			break
		}

		// Record message received metric
		if metrics := observability.GetMetrics(); metrics != nil {
			attrs := attribute.NewSet(
				attribute.Int64("room_id", c.roomID.Int64()),
				attribute.Int64("user_id", c.userID.Int64()),
			)
			metrics.WSMessagesReceived.Add(context.Background(), 1, metric.WithAttributeSet(attrs))
		}

		var payload ChatMessagePayload
		if err := json.Unmarshal(message, &payload); err != nil {
			slog.Error("unmarshal message", "err", err)
			continue
		}

		// Handle typing indicators
		if payload.Type == "typing" {
			c.hub.SetTypingStatus(c.roomID, c.userID, c.username, true)
			continue
		}

		if payload.Type == "stopped_typing" {
			c.hub.SetTypingStatus(c.roomID, c.userID, c.username, false)
			continue
		}

		if payload.Type == "message" && payload.Message != "" {
			ctx, span := otel.Tracer("ssanta").Start(context.Background(), "WebSocket.HandleMessage")
			span.SetAttributes(
				attribute.Int64("room_id", c.roomID.Int64()),
				attribute.Int64("user_id", c.userID.Int64()),
				attribute.String("username", c.username),
				attribute.Int("message_length", len(payload.Message)),
			)

			if len(payload.Message) > maxMessageLength {
				span.End()
				continue
			}
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			members, err := c.svc.ListRoomMembersWithPGP(ctx, c.roomID)
			cancel()
			if err != nil {
				slog.ErrorContext(ctx, "list room members for chat encryption", "err", err, "room_id", c.roomID)
				span.End()
				continue
			}

			// Fetch room PGP requirement
			ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			pgpRequired, err := c.svc.IsRoomPGPRequired(ctx, c.roomID)
			cancel()
			if err != nil {
				slog.ErrorContext(ctx, "get room pgp status", "err", err, "room_id", c.roomID)
				span.End()
				continue
			}

			plaintext := payload.Message
			createdAt := time.Now()
			targetUserID := payload.TargetUserID
			isWhisper := targetUserID != 0

			// Validate whisper target is a room member
			if isWhisper {
				found := false
				for _, m := range members {
					if m.ID == targetUserID {
						found = true
						break
					}
				}
				if !found {
					sys := ChatMessagePayload{
						Type:      "system",
						Message:   "That user is not in this room.",
						CreatedAt: time.Now(),
					}
					if b, err := json.Marshal(sys); err == nil {
						select {
						case c.send <- b:
						default:
						}
					}
					span.End()
					continue
				}
			}

			// PGP-required rooms only accept client-pre-encrypted messages
			if pgpRequired {
				if !payload.PreEncrypted {
					sys := ChatMessagePayload{
						Type:      "system",
						Message:   "This room requires PGP encryption. Your client must encrypt messages before sending.",
						CreatedAt: time.Now(),
					}
					if b, err := json.Marshal(sys); err == nil {
						select {
						case c.send <- b:
						default:
						}
					}
					span.End()
					continue
				}
				out := ChatMessagePayload{
					Type:      "message",
					Username:  c.username,
					Message:   plaintext,
					CreatedAt: createdAt,
					Whisper:   isWhisper,
				}
				outBytes, err := json.Marshal(out)
				if err != nil {
					slog.Error("marshal pre-encrypted chat message", "err", err)
					span.End()
					continue
				}
				perUser := make(map[store.UserID][]byte, len(members))
				if isWhisper {
					perUser[c.userID] = outBytes
					perUser[targetUserID] = outBytes
				} else {
					for _, m := range members {
						perUser[m.ID] = outBytes
					}
				}
				enqueueOfflineMessages(c, payload.Message, createdAt, payload.PreEncrypted, isWhisper, perUser)
				c.hub.SendToRoomUsers(c.roomID, perUser)
				if metrics := observability.GetMetrics(); metrics != nil {
					attrs := attribute.NewSet(
						attribute.Int64("room_id", c.roomID.Int64()),
						attribute.Int64("user_id", c.userID.Int64()),
					)
					metrics.WSMessagesSent.Add(ctx, int64(len(perUser)), metric.WithAttributeSet(attrs))
				}
				slog.InfoContext(ctx, "pre-encrypted chat message forwarded", "room_id", c.roomID, "user_id", c.userID, "recipients", len(perUser))
				span.End()
				continue
			}

			// PGP optional: send plaintext to all members
			perUser := make(map[store.UserID][]byte, len(members))
			out := ChatMessagePayload{
				Type:      "message",
				Username:  c.username,
				Message:   plaintext,
				CreatedAt: createdAt,
				Whisper:   isWhisper,
			}
			outBytes, err := json.Marshal(out)
			if err != nil {
				slog.Error("marshal chat message", "err", err)
				span.End()
				continue
			}
			if isWhisper {
				perUser[c.userID] = outBytes
				perUser[targetUserID] = outBytes
			} else {
				for _, m := range members {
					perUser[m.ID] = outBytes
				}
			}
			enqueueOfflineMessages(c, payload.Message, createdAt, false, isWhisper, perUser)
			c.hub.SendToRoomUsers(c.roomID, perUser)
			if metrics := observability.GetMetrics(); metrics != nil {
				attrs := attribute.NewSet(
					attribute.Int64("room_id", c.roomID.Int64()),
					attribute.Int64("user_id", c.userID.Int64()),
				)
				metrics.WSMessagesSent.Add(ctx, int64(len(perUser)), metric.WithAttributeSet(attrs))
			}
			slog.InfoContext(ctx, "plaintext chat message sent (pgp optional)", "room_id", c.roomID, "user_id", c.userID, "recipients", len(perUser))
			span.End()
		}
	}
}

// enqueueOfflineMessages stores rawMessage in the queue for each recipient in
// perUser that is not currently connected to the room. The sender (c.userID)
// is always skipped — they already see their own message in their local UI.
func enqueueOfflineMessages(c *ChatClient, rawMessage string, createdAt time.Time, preEncrypted, whisper bool, perUser map[store.UserID][]byte) {
	onlineUsers := c.hub.OnlineUsersInRoom(c.roomID)
	var offlineIDs []store.UserID
	for uid := range perUser {
		if uid == c.userID {
			continue
		}
		if !onlineUsers[uid] {
			offlineIDs = append(offlineIDs, uid)
		}
	}
	if len(offlineIDs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.svc.EnqueueMessages(ctx, c.roomID, c.username, rawMessage, createdAt, preEncrypted, whisper, offlineIDs); err != nil {
		slog.Error("enqueue offline messages", "err", err, "room_id", c.roomID)
	}
}

func (c *ChatClient) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close() //nolint:errcheck
		c.hub.wg.Done()
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{}) //nolint:errcheck
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func handleWebSocket(hub *ChatHub, svc WebSocketHandlersService, sessions SessionManager) http.HandlerFunc {
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

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Error("upgrade websocket", "err", err)
			return
		}

		// Flush any queued messages before joining the live room, so replayed
		// messages are strictly ordered before new incoming messages.
		queued, err := svc.FlushMessageQueue(r.Context(), roomID, currentID)
		if err != nil {
			slog.Error("flush message queue", "err", err, "room_id", roomID, "user_id", currentID)
		}
		for _, q := range queued {
			msg := ChatMessagePayload{
				Type:         "message",
				Username:     q.SenderUsername,
				Message:      q.Message,
				CreatedAt:    q.CreatedAt,
				Whisper:      q.Whisper,
				PreEncrypted: q.PreEncrypted,
			}
			if err := conn.WriteJSON(msg); err != nil {
				slog.Error("write queued message", "err", err, "room_id", roomID, "user_id", currentID)
				conn.Close() //nolint:errcheck
				return
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
		}

		if !hub.tryRegister(client) {
			conn.Close() //nolint:errcheck
			return
		}

		hub.wg.Add(2)
		go client.writePump()
		go client.readPump()
	}
}

func handleContentWebSocket(hub *ChatHub, svc WebSocketHandlersService, sessions SessionManager) http.HandlerFunc {
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

		conn, err := upgrader.Upgrade(w, r, nil)
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
		}

		if !hub.tryRegister(client) {
			conn.Close() //nolint:errcheck
			return
		}

		hub.wg.Add(2)
		go client.writePump()
		go client.readPump()
	}
}
