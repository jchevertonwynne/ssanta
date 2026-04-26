package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/jchevertonwynne/ssanta/internal/model"
	"github.com/jchevertonwynne/ssanta/internal/observability"
)

const maxMessageLength = 4096

// DefaultWSBurst / DefaultWSRefillPerSec cap per-connection inbound frame
// rate. Overridable via config.
const (
	DefaultWSBurst        = 10
	DefaultWSRefillPerSec = 5
)

type ctxKey int

const ctxKeyWSSide ctxKey = iota

type ChatHub struct {
	rooms           map[model.RoomID]*ChatRoom
	userConnections map[model.UserID]map[*ChatClient]bool
	register        chan *ChatClient
	unregister      chan *ChatClient
	done            chan struct{}
	wg              sync.WaitGroup
	mu              sync.RWMutex
	typingStatus    map[model.RoomID]map[model.UserID]*typingSession // track who's typing
	typingSessionID int64
	// lifetimeCtx backs metric emissions from hub-owned goroutines/paths that
	// are not tied to a single HTTP request. Cancelled on Stop.
	//
	//nolint:containedctx // we use this for child processes
	lifetimeCtx    context.Context
	lifetimeCancel context.CancelFunc
	// WS message rate-limit parameters applied to every new ChatClient.
	msgBurst  int
	msgRefill float64
}

type typingSession struct {
	username   string
	lastActive time.Time
	cancelFunc context.CancelFunc
	sessionID  int64 // unique ID for this typing session
}

type ChatRoom struct {
	roomID  model.RoomID
	clients map[*ChatClient]bool
	mu      sync.RWMutex
}

type ChatClient struct {
	hub       *ChatHub
	conn      *websocket.Conn
	send      chan []byte
	closeOnce sync.Once
	roomID    model.RoomID
	userID    model.UserID
	username  string
	svc       Service
	bucket    *tokenBucket
}

type ChatMessagePayload struct {
	Type         MsgType         `json:"type"`
	ID           model.MessageID `json:"id,omitempty"`
	Username     string          `json:"username,omitempty"`
	Message      string          `json:"message,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	TargetUserID model.UserID    `json:"target_user_id,omitempty"`
	Whisper      bool            `json:"whisper,omitempty"`
	PreEncrypted bool            `json:"pre_encrypted,omitempty"`
	ClientMsgID  string          `json:"client_message_id,omitempty"`
}

func NewChatHubWithLimits(burst int, refillPerSecond float64) *ChatHub {
	if burst <= 0 {
		burst = DefaultWSBurst
	}
	if refillPerSecond <= 0 {
		refillPerSecond = DefaultWSRefillPerSec
	}
	//nolint:gosec // we call cancel later
	ctx, cancel := context.WithCancel(context.Background())
	return &ChatHub{
		rooms:           make(map[model.RoomID]*ChatRoom),
		userConnections: make(map[model.UserID]map[*ChatClient]bool),
		register:        make(chan *ChatClient),
		unregister:      make(chan *ChatClient),
		done:            make(chan struct{}),
		typingStatus:    make(map[model.RoomID]map[model.UserID]*typingSession),
		lifetimeCtx:     ctx,
		lifetimeCancel:  cancel,
		msgBurst:        burst,
		msgRefill:       refillPerSecond,
	}
}

func (h *ChatHub) Stop() {
	close(h.done)
	if h.lifetimeCancel != nil {
		h.lifetimeCancel()
	}

	// Close all WebSocket connections to unblock reads
	h.mu.Lock()
	for _, room := range h.rooms {
		room.mu.Lock()
		for client := range room.clients {
			client.conn.Close() //nolint:errcheck,gosec
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

//nolint:gocognit,cyclop,nestif,funlen
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
			h.rooms = make(map[model.RoomID]*ChatRoom)
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
				metrics.WSActiveConnections.Add(h.lifetimeCtx, 1)
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
						metrics.WSActiveConnections.Add(h.lifetimeCtx, -1)
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

func (h *ChatHub) BroadcastRoomPresence(roomID model.RoomID) {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	if !ok {
		h.mu.RUnlock()
		return
	}
	room.mu.RLock()
	seen := make(map[model.UserID]bool)
	var onlineIDs []model.UserID
	for client := range room.clients {
		if !seen[client.userID] {
			seen[client.userID] = true
			onlineIDs = append(onlineIDs, client.userID)
		}
	}
	room.mu.RUnlock()
	h.mu.RUnlock()

	msg, err := json.Marshal(struct {
		Type          MsgType        `json:"type"`
		OnlineUserIDs []model.UserID `json:"online_user_ids"`
	}{Type: MsgTypePresence, OnlineUserIDs: onlineIDs})
	if err != nil {
		return
	}
	h.BroadcastToRoom(roomID, msg)
}

func (h *ChatHub) BroadcastToRoom(roomID model.RoomID, message []byte) {
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

func (h *ChatHub) SendToRoomUser(roomID model.RoomID, user model.UserID, msg []byte) {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	for client := range room.clients {
		if client.userID != user {
			continue
		}
		select {
		case client.send <- msg:
		default:
			client.closeOnce.Do(func() { close(client.send) })
			delete(room.clients, client)
		}

		return
	}
}

func (h *ChatHub) DisconnectUser(roomID model.RoomID, userID model.UserID) {
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
			Type:    MsgTypeKicked,
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

// KickSpectators disconnects every client in the room whose userID is not in
// memberIDs, sending them a kicked frame first.
func (h *ChatHub) KickSpectators(roomID model.RoomID, memberIDs map[model.UserID]struct{}) {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	kickedMsg := ChatMessagePayload{
		Type:    MsgTypeKicked,
		Message: "This room is no longer public",
	}
	msg, err := json.Marshal(kickedMsg)
	if err != nil {
		return
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	for client := range room.clients {
		if _, isMember := memberIDs[client.userID]; isMember {
			continue
		}
		select {
		case client.send <- msg:
		case <-time.After(time.Second):
		}
		client.closeOnce.Do(func() { close(client.send) })
		delete(room.clients, client)
	}
}

// DisconnectRoom notifies every client in a room that the room has been
// deleted and then terminates their sockets. Used when the creator deletes a
// room; mirrors DisconnectUser for a whole-room scope.
func (h *ChatHub) DisconnectRoom(roomID model.RoomID) {
	h.mu.Lock()
	room, ok := h.rooms[roomID]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.rooms, roomID)
	h.mu.Unlock()

	notice := ChatMessagePayload{
		Type:      MsgTypeRoomDeleted,
		Message:   "This room has been deleted",
		CreatedAt: time.Now(),
	}
	if noticeBytes, err := json.Marshal(notice); err == nil {
		room.mu.Lock()
		defer room.mu.Unlock()
		for client := range room.clients {
			select {
			case client.send <- noticeBytes:
			case <-time.After(time.Second):
			}
			client.closeOnce.Do(func() { close(client.send) })
		}
	}
}

func (h *ChatHub) BroadcastSystemMessage(roomID model.RoomID, message string) {
	sysMsg := ChatMessagePayload{
		Type:      MsgTypeSystem,
		Message:   message,
		CreatedAt: time.Now(),
	}
	if msg, err := json.Marshal(sysMsg); err == nil {
		h.BroadcastToRoom(roomID, msg)
	}
}

func (h *ChatHub) NotifyRoomUpdate(roomID model.RoomID) {
	refreshMsg := ChatMessagePayload{
		Type: MsgTypeRefresh,
	}
	if msg, err := json.Marshal(refreshMsg); err == nil {
		h.BroadcastToRoom(roomID, msg)
	}
}

func (h *ChatHub) NotifyUser(userID model.UserID, msgType MsgType, message string) {
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

func (h *ChatHub) NotifyContentUpdate(msgType MsgType) {
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
// intentionally not on the Hub interface; handlers call it via an optional
// interface assertion so tests using the mock Hub are unaffected.
//
//nolint:gocognit,cyclop,nestif
func (h *ChatHub) HandleAccountDeletion(userID model.UserID) {
	var affectedRooms []model.RoomID

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
	roomSet := make(map[model.RoomID]struct{})
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
// A 5-second timeout is automatically applied for typing status.
//
//nolint:funlen,nestif
func (h *ChatHub) SetTypingStatus(ctx context.Context, roomID model.RoomID, userID model.UserID, username string, isTyping bool) {
	h.mu.Lock()

	// Ensure room entry exists in typingStatus
	if h.typingStatus[roomID] == nil {
		h.typingStatus[roomID] = make(map[model.UserID]*typingSession)
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
			Type:     MsgTypeStoppedTyping,
			Username: username,
		}
		if msg, err := json.Marshal(stoppedMsg); err == nil {
			h.BroadcastToRoom(roomID, msg)
		}
	} else {
		// User is typing, set up with auto-timeout
		h.typingSessionID++
		sessionID := h.typingSessionID

		ctx, cancel := context.WithTimeout(ctx, 5*time.Second) //nolint:gosec
		h.typingStatus[roomID][userID] = &typingSession{
			username:   username,
			lastActive: time.Now(),
			cancelFunc: cancel,
			sessionID:  sessionID,
		}
		h.mu.Unlock()

		// Broadcast typing update
		typingMsg := ChatMessagePayload{
			Type:     MsgTypeTyping,
			Username: username,
		}
		if msg, err := json.Marshal(typingMsg); err == nil {
			h.BroadcastToRoom(roomID, msg)
		}

		// Wait for timeout and auto-clear
		h.wg.Go(func() {
			<-ctx.Done()

			h.mu.Lock()
			// Only clear if it hasn't been updated (check sessionID)
			if session, ok := h.typingStatus[roomID][userID]; ok && session.sessionID == sessionID {
				delete(h.typingStatus[roomID], userID)
				h.mu.Unlock()

				// Broadcast stopped_typing
				stoppedMsg := ChatMessagePayload{
					Type:     MsgTypeStoppedTyping,
					Username: username,
				}
				if msg, err := json.Marshal(stoppedMsg); err == nil {
					h.BroadcastToRoom(roomID, msg)
				}
			} else {
				h.mu.Unlock()
			}
		})
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

//nolint:gocognit,cyclop,nestif,gocyclo,funlen,maintidx
func (c *ChatClient) readPump(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	defer func() {
		select {
		case c.hub.unregister <- c:
		case <-c.hub.done:
			// Hub is stopping; avoid blocking if Run has exited.
		}
		c.conn.Close() //nolint:errcheck,gosec
	}()

	c.conn.SetReadLimit(maxMessageLength * 2)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
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
			)
			metrics.WSMessagesReceived.Add(ctx, 1, metric.WithAttributeSet(attrs))
		}

		var payload ChatMessagePayload
		if err := json.Unmarshal(message, &payload); err != nil {
			slog.Error("unmarshal message", "err", err)
			continue
		}

		// Rate-limit inbound work on this connection. stopped_typing frames
		// carry presence-reset state so we let them through unconditionally —
		// they're cheap and dropping them causes lingering "typing…" UI.
		if payload.Type == MsgTypeTyping || payload.Type == MsgTypeMessage {
			if c.bucket != nil && !c.bucket.Take() {
				if metrics := observability.GetMetrics(); metrics != nil {
					attrs := attribute.NewSet(
						attribute.Int64("room_id", c.roomID.Int64()),
						attribute.Bool("dropped", true),
					)
					metrics.WSMessagesReceived.Add(ctx, 1, metric.WithAttributeSet(attrs))
				}
				continue
			}
		}

		// Handle typing indicators
		if payload.Type == MsgTypeTyping {
			c.hub.SetTypingStatus(ctx, c.roomID, c.userID, c.username, true)
			continue
		}

		if payload.Type == MsgTypeStoppedTyping {
			c.hub.SetTypingStatus(ctx, c.roomID, c.userID, c.username, false)
			continue
		}

		if payload.Type == MsgTypeMessage && payload.Message != "" {
			baseCtx := ctx
			ctx, span := otel.Tracer("ssanta").Start(baseCtx, "WebSocket.HandleMessage")
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

			// Fetch room PGP requirement
			pgpCtx, cancel := context.WithTimeout(baseCtx, 2*time.Second)
			pgpRequired, err := c.svc.IsRoomPGPRequired(pgpCtx, c.roomID)
			cancel()
			if err != nil {
				slog.ErrorContext(pgpCtx, "get room pgp status", "err", err, "room_id", c.roomID)
				span.End()
				continue
			}

			plaintext := payload.Message
			createdAt := time.Now()
			targetUserID := payload.TargetUserID
			isWhisper := targetUserID != 0

			// Validate whisper target is a room member
			if isWhisper {
				memberCtx, cancel := context.WithTimeout(baseCtx, 2*time.Second)
				members, err := c.svc.ListRoomMembersWithPGP(memberCtx, c.roomID)
				cancel()
				if err != nil {
					slog.ErrorContext(memberCtx, "list room members for whisper validation", "err", err, "room_id", c.roomID)
					span.End()
					continue
				}
				found := false
				for _, m := range members {
					if m.ID == targetUserID {
						found = true
						break
					}
				}
				if !found {
					sys := ChatMessagePayload{
						Type:      MsgTypeSystem,
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
			if pgpRequired && !payload.PreEncrypted {
				sys := ChatMessagePayload{
					Type:      MsgTypeSystem,
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

			var targetID *model.UserID
			if isWhisper {
				targetID = &targetUserID
			}
			msgID, err := persistMessage(ctx, c.svc, c.roomID, c.userID, c.username, plaintext, isWhisper, targetID, payload.PreEncrypted)
			if err != nil {
				span.End()
				continue
			}
			out := ChatMessagePayload{
				Type:        MsgTypeMessage,
				ID:          msgID,
				Username:    c.username,
				Message:     plaintext,
				CreatedAt:   createdAt,
				Whisper:     isWhisper,
				ClientMsgID: payload.ClientMsgID,
			}
			outBytes, err := json.Marshal(out)
			if err != nil {
				slog.Error("marshal chat message", "err", err)
				span.End()
				continue
			}
			if isWhisper {
				c.hub.SendToRoomUser(c.roomID, c.userID, outBytes)
				if metrics := observability.GetMetrics(); metrics != nil {
					attrs := attribute.NewSet(attribute.Int64("room_id", c.roomID.Int64()))
					metrics.WSMessagesSent.Add(ctx, 1, metric.WithAttributeSet(attrs))
				}
				slog.InfoContext(ctx, "whisper sent", "room_id", c.roomID, "user_id", c.userID)
			} else {
				c.hub.BroadcastToRoom(c.roomID, outBytes)
				if metrics := observability.GetMetrics(); metrics != nil {
					attrs := attribute.NewSet(attribute.Int64("room_id", c.roomID.Int64()))
					metrics.WSMessagesSent.Add(ctx, 1, metric.WithAttributeSet(attrs))
				}
				slog.InfoContext(ctx, "chat message broadcast", "room_id", c.roomID, "user_id", c.userID, "pgp_required", pgpRequired)
			}
			span.End()
		}
	}
}

func persistMessage(ctx context.Context, svc Service, roomID model.RoomID, userID model.UserID, username, message string, whisper bool, targetUserID *model.UserID, preEncrypted bool) (model.MessageID, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	id, err := svc.CreateMessage(ctx, roomID, userID, username, message, whisper, targetUserID, preEncrypted)
	if err != nil {
		slog.Error("persist message", "err", err, "room_id", roomID, "user_id", userID)
	}
	return id, err
}

func (c *ChatClient) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close() //nolint:errcheck,gosec
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
