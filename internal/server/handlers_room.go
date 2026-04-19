package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

func handleCreateRoom(svc RoomHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		attempted := r.FormValue("display_name")
		err := svc.CreateRoom(r.Context(), attempted, currentID)
		switch {
		case errors.Is(err, store.ErrRoomNameEmpty):
			renderContentWithRoomFormError(w, r.Context(), svc, currentID, attempted, err.Error())
			return
		case errors.Is(err, store.ErrRoomNameTooLong):
			renderContentWithRoomFormError(w, r.Context(), svc, currentID, attempted, err.Error())
			return
		case errors.Is(err, store.ErrRoomNameTaken):
			renderContentWithRoomFormError(w, r.Context(), svc, currentID, attempted, err.Error())
			return
		case err != nil:
			slog.Error("create room", "err", err)
			http.Error(w, "failed to create room", http.StatusInternalServerError)
			return
		}
		renderContent(w, r.Context(), svc, currentID)
	}
}

func handleDeleteRoom(svc RoomHandlersService, sessions SessionManager) http.HandlerFunc {
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
		err = svc.DeleteRoom(r.Context(), roomID, currentID)
		switch {
		case errors.Is(err, store.ErrRoomNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case err != nil:
			slog.Error("delete room", "err", err)
			http.Error(w, "failed to delete room", http.StatusInternalServerError)
			return
		}
		renderContent(w, r.Context(), svc, currentID)
	}
}

func handleJoinRoom(svc RoomHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
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

		// Get username before joining
		username, err := svc.GetUsername(r.Context(), currentID)
		if err != nil {
			slog.Error("get username", "err", err)
			http.Error(w, "failed to get user info", http.StatusInternalServerError)
			return
		}

		isCreator, err := svc.IsRoomCreator(r.Context(), roomID, currentID)
		if err != nil {
			slog.Error("check room creator", "err", err)
			http.Error(w, "failed to check room status", http.StatusInternalServerError)
			return
		}

		err = svc.JoinRoom(r.Context(), roomID, currentID)
		if err != nil {
			slog.Error("join room", "err", err)
			http.Error(w, "failed to join room", http.StatusInternalServerError)
			return
		}

		// Send system message and notify other members to update their member lists
		hub.BroadcastSystemMessage(roomID, username+" joined the room")
		hub.NotifyRoomUpdate(roomID)

		if isCreator {
			hub.NotifyUser(currentID, "membership_gained", "")
			renderRoomSidebar(w, r.Context(), svc, currentID, roomID)
		} else {
			renderRoomDetail(w, r.Context(), svc, currentID, roomID)
		}
	}
}

func handleLeaveRoom(svc RoomHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
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

		// Get username before leaving
		username, err := svc.GetUsername(r.Context(), currentID)
		if err != nil {
			slog.Error("get username", "err", err)
			http.Error(w, "failed to get user info", http.StatusInternalServerError)
			return
		}

		// Check if user is the creator before leaving
		isCreator, err := svc.IsRoomCreator(r.Context(), roomID, currentID)
		if err != nil {
			slog.Error("check room creator", "err", err)
			http.Error(w, "failed to check room status", http.StatusInternalServerError)
			return
		}

		err = svc.LeaveRoom(r.Context(), roomID, currentID)
		switch {
		case errors.Is(err, store.ErrRoomNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrNotRoomMember):
			http.Error(w, "cannot leave room", http.StatusForbidden)
			return
		case err != nil:
			slog.Error("leave room", "err", err)
			http.Error(w, "failed to leave room", http.StatusInternalServerError)
			return
		}

		// Send system message and notify other members to update their member lists
		hub.BroadcastSystemMessage(roomID, username+" left the room")
		hub.NotifyRoomUpdate(roomID)

		// If user is the creator, stay on the room detail page
		// Otherwise, go back to the main content page
		if isCreator {
			hub.NotifyUser(currentID, "membership_lost", "")
			renderRoomSidebar(w, r.Context(), svc, currentID, roomID)
		} else {
			renderContent(w, r.Context(), svc, currentID)
		}
	}
}

func handleRoomDetail(svc RoomHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			// If not logged in and this is a direct page request, redirect to home
			if r.Header.Get("HX-Request") == "" {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		roomID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid room id", http.StatusBadRequest)
			return
		}

		// Check if this is an HTMX request
		if r.Header.Get("HX-Request") != "" {
			// HTMX request: render just the fragment
			renderRoomDetail(w, r.Context(), svc, currentID, roomID)
		} else {
			// Direct browser request: render full page with room detail content
			renderRoomDetailPage(w, r.Context(), svc, currentID, roomID)
		}
	}
}

func handleRoomDynamic(svc RoomHandlersService, sessions SessionManager) http.HandlerFunc {
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

		renderRoomDynamic(w, r.Context(), svc, currentID, roomID)
	}
}

func handleRoomSidebar(svc RoomHandlersService, sessions SessionManager) http.HandlerFunc {
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

		renderRoomSidebar(w, r.Context(), svc, currentID, roomID)
	}
}

func handleSetMembersCanInvite(svc RoomHandlersService, sessions SessionManager) http.HandlerFunc {
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
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		value := r.FormValue("value") == "true"
		err = svc.SetRoomMembersCanInvite(r.Context(), roomID, currentID, value)
		switch {
		case errors.Is(err, store.ErrNotRoomCreator):
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		case err != nil:
			slog.Error("set members_can_invite", "err", err)
			http.Error(w, "failed to update room", http.StatusInternalServerError)
			return
		}
		renderRoomSidebar(w, r.Context(), svc, currentID, roomID)
	}
}

func handleRemoveMember(svc RoomHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
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
		memberID, err := strconv.ParseInt(r.PathValue("memberid"), 10, 64)
		if err != nil {
			http.Error(w, "invalid member id", http.StatusBadRequest)
			return
		}
		err = svc.RemoveMember(r.Context(), roomID, memberID, currentID)
		switch {
		case errors.Is(err, store.ErrRoomNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrNotRoomCreator):
			http.Error(w, "only the creator can remove members", http.StatusForbidden)
			return
		case errors.Is(err, store.ErrCannotRemoveCreator):
			http.Error(w, "cannot remove the room creator", http.StatusBadRequest)
			return
		case errors.Is(err, store.ErrNotRoomMember):
			http.Error(w, "user is not a member", http.StatusNotFound)
			return
		case err != nil:
			slog.Error("remove member", "err", err)
			http.Error(w, "failed to remove member", http.StatusInternalServerError)
			return
		}

		// Get member's username for system message
		username, err := svc.GetUsername(r.Context(), memberID)
		if err == nil {
			hub.BroadcastSystemMessage(roomID, username+" was removed from the room")
		}

		// Disconnect the user's WebSocket connection
		hub.DisconnectUser(roomID, memberID)

		// Notify remaining members to update their member lists
		hub.NotifyRoomUpdate(roomID)

		renderRoomDynamic(w, r.Context(), svc, currentID, roomID)
	}
}