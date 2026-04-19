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
	"golang.org/x/sync/errgroup"

	"github.com/jchevertonwynne/ssanta/internal/observability"
	"github.com/jchevertonwynne/ssanta/internal/pgp"
	"github.com/jchevertonwynne/ssanta/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
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
	Type      string    `json:"type"` // "message", "error"
	Username  string    `json:"username,omitempty"`
	Message   string    `json:"message,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

func NewChatHub() *ChatHub {
	return &ChatHub{
		rooms:           make(map[store.RoomID]*ChatRoom),
		userConnections: make(map[store.UserID]map[*ChatClient]bool),
		register:        make(chan *ChatClient),
		unregister:      make(chan *ChatClient),
		done:            make(chan struct{}),
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
			client.conn.Close()
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
			}
			h.mu.Unlock()
		}
	}
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

func (c *ChatClient) readPump() {
	defer func() {
		select {
		case c.hub.unregister <- c:
		case <-c.hub.done:
			// Hub is stopping; avoid blocking if Run has exited.
		}
		c.conn.Close()
		c.hub.wg.Done()
	}()

	c.conn.SetReadLimit(maxMessageLength * 2)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
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

			senderVerified := false
			for _, m := range members {
				if m.ID != c.userID {
					continue
				}
				if m.PGPPublicKey != "" && m.PGPVerifiedAt != nil {
					senderVerified = true
				}
				break
			}
			if !senderVerified {
				sys := ChatMessagePayload{
					Type:      "system",
					Message:   "You must upload and verify a PGP key to send messages in this room.",
					CreatedAt: time.Now(),
				}
				if b, err := json.Marshal(sys); err == nil {
					select {
					case c.send <- b:
					default:
					}
				}
				continue
			}

			plaintext := payload.Message
			createdAt := time.Now()

			// Parallelize per-recipient encryption using errgroup
			perUser := make(map[store.UserID][]byte, len(members))
			var mu sync.Mutex
			var g errgroup.Group

			for _, m := range members {
				m := m // capture loop variable
				g.Go(func() error {
					var b []byte
					var err error

					if m.PGPPublicKey == "" {
						out := ChatMessagePayload{
							Type:      "message",
							Username:  c.username,
							Message:   "<encrypted message>",
							CreatedAt: createdAt,
						}
						b, err = json.Marshal(out)
						if err != nil {
							return nil // skip on marshal error, don't fail the whole batch
						}
					} else {
						ciphertext, err := pgp.EncryptToPublicKey(m.PGPPublicKey, []byte(plaintext))
						if err != nil {
							slog.Error("encrypt chat message", "room_id", c.roomID, "recipient_user_id", m.ID, "err", err)
							return nil // skip on encryption error, don't fail the whole batch
						}
						out := ChatMessagePayload{
							Type:      "message",
							Username:  c.username,
							Message:   ciphertext,
							CreatedAt: createdAt,
						}
						b, err = json.Marshal(out)
						if err != nil {
							return nil // skip on marshal error, don't fail the whole batch
						}
					}

					mu.Lock()
					perUser[m.ID] = b
					mu.Unlock()
					return nil
				})
			}

			// Wait for all encryptions to complete
			g.Wait() // ignore error, we handled errors inside goroutines

			c.hub.SendToRoomUsers(c.roomID, perUser)

			// Record messages sent metric (one per recipient)
			if metrics := observability.GetMetrics(); metrics != nil {
				attrs := attribute.NewSet(
					attribute.Int64("room_id", c.roomID.Int64()),
					attribute.Int64("user_id", c.userID.Int64()),
				)
				metrics.WSMessagesSent.Add(ctx, int64(len(perUser)), metric.WithAttributeSet(attrs))
			}
			slog.InfoContext(ctx, "chat message sent", "room_id", c.roomID, "user_id", c.userID, "recipients", len(perUser))

			span.End()
		}
	}
}

func (c *ChatClient) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
		c.hub.wg.Done()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
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

		// Check if user is a member (creators who aren't members can't chat)
		isMember, err := svc.IsRoomMember(r.Context(), roomID, currentID)
		if err != nil {
			http.Error(w, "failed to check room access", http.StatusInternalServerError)
			return
		}

		if !isMember {
			http.Error(w, "must be a member to access chat", http.StatusForbidden)
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
			roomID:   roomID,
			userID:   currentID,
			username: username,
			svc:      svc,
		}

		if !hub.tryRegister(client) {
			conn.Close()
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
			conn.Close()
			return
		}

		hub.wg.Add(2)
		go client.writePump()
		go client.readPump()
	}
}
