package server

import (
	"errors"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/jchevertonwynne/ssanta/internal/observability"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

func handleCreateRoom(svc RoomHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}

		ctx, span := otel.Tracer("ssanta").Start(r.Context(), "CreateRoom")
		defer span.End()
		span.SetAttributes(attribute.Int64("user_id", currentID.Int64()))

		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		attempted := r.FormValue("display_name")
		span.SetAttributes(attribute.String("room_name", attempted))

		_, err := svc.CreateRoom(ctx, attempted, currentID)
		switch {
		case errors.Is(err, store.ErrRoomNameEmpty):
			span.SetStatus(codes.Error, err.Error())
			renderContentWithRoomFormError(w, ctx, svc, currentID, attempted, err.Error())
			return
		case errors.Is(err, store.ErrRoomNameTooLong):
			span.SetStatus(codes.Error, err.Error())
			renderContentWithRoomFormError(w, ctx, svc, currentID, attempted, err.Error())
			return
		case errors.Is(err, store.ErrRoomNameTaken):
			span.SetStatus(codes.Error, err.Error())
			renderContentWithRoomFormError(w, ctx, svc, currentID, attempted, err.Error())
			return
		case err != nil:
			span.SetStatus(codes.Error, err.Error())
			loggerFromContext(ctx).Error("create room", "err", err, "room_name", attempted)
			http.Error(w, "failed to create room", http.StatusInternalServerError)
			return
		}

		loggerFromContext(ctx).Info("room created", "room_name", attempted, "creator_id", currentID)

		if metrics := observability.GetMetrics(); metrics != nil {
			metrics.RoomsCreated.Add(ctx, 1)
		}

		renderContent(w, ctx, svc, currentID)
	}
}

func handleDeleteRoom(svc RoomHandlersService, sessions SessionManager) http.HandlerFunc {
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
		err := svc.DeleteRoom(r.Context(), roomID, currentID)
		switch {
		case errors.Is(err, store.ErrRoomNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case err != nil:
			loggerFromContext(r.Context()).Error("delete room", "err", err)
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
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}

		// Get username before joining
		username, err := svc.GetUsername(r.Context(), currentID)
		if err != nil {
			loggerFromContext(r.Context()).Error("get username", "err", err)
			http.Error(w, "failed to get user info", http.StatusInternalServerError)
			return
		}

		isCreator, isMember, err := svc.GetRoomAccess(r.Context(), roomID, currentID)
		if err != nil {
			loggerFromContext(r.Context()).Error("check room access", "err", err)
			http.Error(w, "failed to check room status", http.StatusInternalServerError)
			return
		}

		if !isCreator && !isMember {
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		}

		err = svc.JoinRoom(r.Context(), roomID, currentID)
		if err != nil {
			loggerFromContext(r.Context()).Error("join room", "err", err)
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
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}

		// Get username before leaving
		username, err := svc.GetUsername(r.Context(), currentID)
		if err != nil {
			loggerFromContext(r.Context()).Error("get username", "err", err)
			http.Error(w, "failed to get user info", http.StatusInternalServerError)
			return
		}

		// Check if user is the creator before leaving
		isCreator, _, err := svc.GetRoomAccess(r.Context(), roomID, currentID)
		if err != nil {
			loggerFromContext(r.Context()).Error("check room access", "err", err)
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
			loggerFromContext(r.Context()).Error("leave room", "err", err)
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
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
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
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
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
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
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
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		value := r.FormValue("value") == "true"
		err := svc.SetRoomMembersCanInvite(r.Context(), roomID, currentID, value)
		switch {
		case errors.Is(err, store.ErrNotRoomCreator):
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		case err != nil:
			loggerFromContext(r.Context()).Error("set members_can_invite", "err", err)
			http.Error(w, "failed to update room", http.StatusInternalServerError)
			return
		}
		renderRoomSidebar(w, r.Context(), svc, currentID, roomID)
	}
}

func handleSetPGPRequired(svc RoomHandlersService, sessions SessionManager) http.HandlerFunc {
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
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		value := r.FormValue("value") == "true"
		err := svc.SetRoomPGPRequired(r.Context(), roomID, currentID, value)
		switch {
		case errors.Is(err, store.ErrNotRoomCreator):
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		case err != nil:
			loggerFromContext(r.Context()).Error("set pgp_required", "err", err)
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
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}
		memberID, ok := pathUserID(w, r, "memberid")
		if !ok {
			return
		}
		err := svc.RemoveMember(r.Context(), roomID, memberID, currentID)
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
			loggerFromContext(r.Context()).Error("remove member", "err", err)
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
