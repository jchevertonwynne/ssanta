package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/session"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

func handleCreateInvite(ctx *serverContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), ctx.svc, ctx.sessions, w, r)
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
		attempted := r.FormValue("invitee_username")
		err = ctx.svc.CreateInvite(r.Context(), roomID, currentID, attempted)
		switch {
		case errors.Is(err, store.ErrInviteeNotFound),
			errors.Is(err, store.ErrAlreadyMember),
			errors.Is(err, store.ErrAlreadyInvited),
			errors.Is(err, store.ErrCannotInviteSelf),
			errors.Is(err, store.ErrNotAllowedToInvite):
			renderRoomSidebarWithInviteError(w, r.Context(), ctx.svc, currentID, roomID, attempted, err.Error())
			return
		case errors.Is(err, store.ErrRoomNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case err != nil:
			slog.Error("create invite", "err", err)
			http.Error(w, "failed to create invite", http.StatusInternalServerError)
			return
		}

		if inviterName, err := ctx.svc.GetUsername(r.Context(), currentID); err == nil {
			ctx.hub.BroadcastSystemMessage(roomID, inviterName+" invited "+attempted)
		} else {
			slog.Error("get inviter username", "err", err)
		}

		// Notify the invitee if they're online
		if inviteeUser, err := ctx.svc.GetUserByUsername(r.Context(), attempted); err == nil {
			ctx.hub.NotifyUser(inviteeUser.ID, "invite_received", "")
		}

		renderRoomSidebar(w, r.Context(), ctx.svc, currentID, roomID)
	}
}

func handleAcceptInvite(ctx *serverContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), ctx.svc, ctx.sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		inviteID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid invite id", http.StatusBadRequest)
			return
		}

		// Get room ID and username before accepting
		roomID, err := ctx.svc.RoomIDForInvite(r.Context(), inviteID)
		if err != nil {
			slog.Error("lookup invite room", "err", err)
			http.Error(w, "failed to accept invite", http.StatusInternalServerError)
			return
		}

		username, err := ctx.svc.GetUsername(r.Context(), currentID)
		if err != nil {
			slog.Error("get username", "err", err)
			http.Error(w, "failed to get user info", http.StatusInternalServerError)
			return
		}

		err = ctx.svc.AcceptInvite(r.Context(), inviteID, currentID)
		switch {
		case errors.Is(err, store.ErrInviteNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case err != nil:
			slog.Error("accept invite", "err", err)
			http.Error(w, "failed to accept invite", http.StatusInternalServerError)
			return
		}

		// Send system message to notify other members
	ctx.hub.NotifyRoomUpdate(roomID)

		ctx.hub.BroadcastSystemMessage(roomID, username+" joined the room")

		renderRoomDetail(w, r.Context(), ctx.svc, currentID, roomID)
	}
}

func handleDeclineInvite(svc *service.Service, sessions *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		inviteID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid invite id", http.StatusBadRequest)
			return
		}
		err = svc.DeclineInvite(r.Context(), inviteID, currentID)
		switch {
		case errors.Is(err, store.ErrInviteNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case err != nil:
			slog.Error("decline invite", "err", err)
			http.Error(w, "failed to decline invite", http.StatusInternalServerError)
			return
		}
		renderContent(w, r.Context(), svc, currentID)
	}
}

func handleCancelInvite(ctx *serverContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), ctx.svc, ctx.sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		inviteID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid invite id", http.StatusBadRequest)
			return
		}

		// Get room ID and invitee ID before cancelling
		roomID, err := ctx.svc.RoomIDForInvite(r.Context(), inviteID)
		if err != nil && !errors.Is(err, store.ErrInviteNotFound) {
			slog.Error("lookup invite room", "err", err)
			http.Error(w, "failed to cancel invite", http.StatusInternalServerError)
			return
		}

		inviteeID, err := ctx.svc.InviteeIDForInvite(r.Context(), inviteID)
		if err != nil && !errors.Is(err, store.ErrInviteNotFound) {
			slog.Error("lookup invitee", "err", err)
		}

		err = ctx.svc.CancelInvite(r.Context(), inviteID, currentID)
		switch {
		case errors.Is(err, store.ErrInviteNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrNotAllowedToCancelInvite):
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		case err != nil:
			slog.Error("cancel invite", "err", err)
			http.Error(w, "failed to cancel invite", http.StatusInternalServerError)
			return
		}

		// Notify the invitee if we have their ID
		if inviteeID > 0 {
			ctx.hub.NotifyUser(inviteeID, "invite_cancelled", "")
		}

		renderRoomDynamic(w, r.Context(), ctx.svc, currentID, roomID)
	}
}
