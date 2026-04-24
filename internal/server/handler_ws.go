package server

import (
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/ws"
)

func handleWebSocket(hub *ws.ChatHub, svc WebSocketHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}

		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			http.Error(w, "room ID required", http.StatusBadRequest)
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

		//nolint:contextcheck // the code inside makes its own
		ws.RunContentWS(hub, sessions, currentID, username, w, r)
	}
}
