package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
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
	rooms           map[int64]*ChatRoom
	userConnections map[int64]map[*ChatClient]bool
	register        chan *ChatClient
	unregister      chan *ChatClient
	done            chan struct{}
	wg              sync.WaitGroup
	mu              sync.RWMutex
}

type ChatRoom struct {
	roomID  int64
	clients map[*ChatClient]bool
	mu      sync.RWMutex
}

type ChatClient struct {
	hub      *ChatHub
	conn     *websocket.Conn
	send     chan []byte
	roomID   int64
	userID   int64
	username string
}

type ChatMessagePayload struct {
	Type      string    `json:"type"` // "message", "error"
	Username  string    `json:"username,omitempty"`
	Message   string    `json:"message,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

func NewChatHub() *ChatHub {
	return &ChatHub{
		rooms:           make(map[int64]*ChatRoom),
		userConnections: make(map[int64]map[*ChatClient]bool),
		register:        make(chan *ChatClient),
		unregister:      make(chan *ChatClient),
		done:            make(chan struct{}),
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
					close(client.send)
				}
				room.mu.Unlock()
			}
			h.rooms = make(map[int64]*ChatRoom)
			h.mu.Unlock()
			return

		case client := <-h.register:
			h.mu.Lock()
			// Track user connection
			if h.userConnections[client.userID] == nil {
				h.userConnections[client.userID] = make(map[*ChatClient]bool)
			}
			h.userConnections[client.userID][client] = true

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
			// Remove from user connections
			if connections, ok := h.userConnections[client.userID]; ok {
				if _, exists := connections[client]; exists {
					delete(connections, client)
					close(client.send)
				}
				if len(connections) == 0 {
					delete(h.userConnections, client.userID)
				}
			}
			h.mu.Unlock()

			// Remove from room if in one
			if client.roomID > 0 {
				h.mu.RLock()
				room, ok := h.rooms[client.roomID]
				h.mu.RUnlock()

				if ok {
					room.mu.Lock()
					delete(room.clients, client)
					room.mu.Unlock()

					// Clean up empty rooms
					room.mu.RLock()
					isEmpty := len(room.clients) == 0
					room.mu.RUnlock()

					if isEmpty {
						h.mu.Lock()
						delete(h.rooms, client.roomID)
						h.mu.Unlock()
					}
				}
			}
		}
	}
}

func (h *ChatHub) BroadcastToRoom(roomID int64, message []byte) {
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
			close(client.send)
			delete(room.clients, client)
		}
	}
}

func (h *ChatHub) DisconnectUser(roomID, userID int64) {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	h.mu.RUnlock()

	if !ok {
		return
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	for client := range room.clients {
		if client.userID == userID {
			// Send a kicked message - use blocking send to ensure it's queued
			kickedMsg := ChatMessagePayload{
				Type:    "kicked",
				Message: "You have been removed from this room",
			}
			if msg, err := json.Marshal(kickedMsg); err == nil {
				// Try to send with timeout to avoid blocking forever
				select {
				case client.send <- msg:
					// Message queued successfully
				case <-time.After(1 * time.Second):
					// Timeout - proceed with close anyway
				}
			}
			// Give writePump time to send the message
			go func(c *ChatClient) {
				time.Sleep(200 * time.Millisecond)
				c.conn.Close()
			}(client)
			delete(room.clients, client)
		}
	}
}

func (h *ChatHub) BroadcastSystemMessage(roomID int64, message string) {
	sysMsg := ChatMessagePayload{
		Type:      "system",
		Message:   message,
		CreatedAt: time.Now(),
	}
	if msg, err := json.Marshal(sysMsg); err == nil {
		h.BroadcastToRoom(roomID, msg)
	}
}

func (h *ChatHub) NotifyRoomUpdate(roomID int64) {
	refreshMsg := ChatMessagePayload{
		Type: "refresh",
	}
	if msg, err := json.Marshal(refreshMsg); err == nil {
		h.BroadcastToRoom(roomID, msg)
	}
}

func (h *ChatHub) NotifyUser(userID int64, msgType, message string) {
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

		var payload ChatMessagePayload
		if err := json.Unmarshal(message, &payload); err != nil {
			slog.Error("unmarshal message", "err", err)
			continue
		}

		if payload.Type == "message" && payload.Message != "" {
			if len(payload.Message) > maxMessageLength {
				continue
			}
			// Broadcast to all clients in the room
			broadcast := ChatMessagePayload{
				Type:      "message",
				Username:  c.username,
				Message:   payload.Message,
				CreatedAt: time.Now(),
			}
			broadcastJSON, _ := json.Marshal(broadcast)
			c.hub.BroadcastToRoom(c.roomID, broadcastJSON)
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

		roomID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid room id", http.StatusBadRequest)
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
		}

		client.hub.register <- client

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

		client.hub.register <- client

		hub.wg.Add(2)
		go client.writePump()
		go client.readPump()
	}
}
