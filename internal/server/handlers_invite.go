package server

import (
	"errors"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/jchevertonwynne/ssanta/internal/observability"
	"github.com/jchevertonwynne/ssanta/internal/store"
	"github.com/jchevertonwynne/ssanta/internal/ws"
)

//nolint:cyclop,funlen
func handleCreateInvite(svc InviteHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}

		ctx, span := otel.Tracer("ssanta").Start(r.Context(), "CreateInvite")
		defer span.End()

		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		attempted := r.FormValue("invitee_username")
		span.SetAttributes(
			attribute.Int64("room_id", roomID.Int64()),
			attribute.Int64("inviter_id", currentID.Int64()),
			attribute.String("invitee_username", attempted),
		)

		err := svc.CreateInvite(ctx, roomID, currentID, attempted)
		switch {
		case errors.Is(err, store.ErrInviteeNotFound),
			errors.Is(err, store.ErrAlreadyMember),
			errors.Is(err, store.ErrAlreadyInvited),
			errors.Is(err, store.ErrCannotInviteSelf),
			errors.Is(err, store.ErrNotAllowedToInvite):
			span.SetStatus(codes.Error, err.Error())
			renderRoomSidebarWithInviteError(w, ctx, svc, currentID, roomID, attempted, err.Error())
			return
		case errors.Is(err, store.ErrRoomNotFound):
			span.SetStatus(codes.Error, err.Error())
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrOperationNotAllowedOnDM):
			span.SetStatus(codes.Error, err.Error())
			http.Error(w, "not supported for DM rooms", http.StatusForbidden)
			return
		case err != nil:
			span.SetStatus(codes.Error, err.Error())
			loggerFromContext(ctx).Error("create invite", "err", err, "room_id", roomID, "invitee", attempted)
			http.Error(w, "failed to create invite", http.StatusInternalServerError)
			return
		}

		loggerFromContext(ctx).Info("invite created", "room_id", roomID, "inviter_id", currentID, "invitee", attempted)

		if metrics := observability.GetMetrics(); metrics != nil {
			metrics.InvitesSent.Add(ctx, 1)
		}

		if inviterName, err := svc.GetUsername(ctx, currentID); err == nil {
			hub.BroadcastSystemMessage(roomID, inviterName+" invited "+attempted)
		} else {
			loggerFromContext(ctx).Error("get inviter username", "err", err)
		}

		// Notify the invitee if they're online
		if inviteeUser, err := svc.GetUserByUsername(ctx, attempted); err == nil {
			hub.NotifyUser(inviteeUser.ID, ws.MsgTypeInviteReceived, "")
		}

		renderRoomSidebar(w, ctx, svc, currentID, roomID)
	}
}

func handleAcceptInvite(svc InviteHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		inviteID, ok := pathInviteID(w, r, "id")
		if !ok {
			return
		}

		roomID, err := svc.AcceptInvite(r.Context(), inviteID, currentID)
		switch {
		case errors.Is(err, store.ErrInviteNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrInviteExpired):
			http.Error(w, err.Error(), http.StatusGone)
			return
		case err != nil:
			loggerFromContext(r.Context()).Error("accept invite", "err", err)
			http.Error(w, "failed to accept invite", http.StatusInternalServerError)
			return
		}

		// Username is only needed for the announcement; fetch after the
		// state change so we don't pay for it on the unhappy paths.
		if username, err := svc.GetUsername(r.Context(), currentID); err == nil {
			hub.BroadcastSystemMessage(roomID, username+" joined the room")
		} else {
			loggerFromContext(r.Context()).Error("get username", "err", err)
		}
		hub.NotifyRoomUpdate(roomID)

		renderRoomDetail(w, r.Context(), svc, currentID, roomID)
	}
}

func handleDeclineInvite(svc InviteHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		inviteID, ok := pathInviteID(w, r, "id")
		if !ok {
			return
		}
		err := svc.DeclineInvite(r.Context(), inviteID, currentID)
		switch {
		case errors.Is(err, store.ErrInviteNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case err != nil:
			loggerFromContext(r.Context()).Error("decline invite", "err", err)
			http.Error(w, "failed to decline invite", http.StatusInternalServerError)
			return
		}
		renderContent(w, r.Context(), svc, currentID)
	}
}

func handleCancelInvite(svc InviteHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		inviteID, ok := pathInviteID(w, r, "id")
		if !ok {
			return
		}

		roomID, inviteeID, err := svc.CancelInvite(r.Context(), inviteID, currentID)
		switch {
		case errors.Is(err, store.ErrInviteNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrNotAllowedToCancelInvite):
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		case err != nil:
			loggerFromContext(r.Context()).Error("cancel invite", "err", err)
			http.Error(w, "failed to cancel invite", http.StatusInternalServerError)
			return
		}

		if inviteeID > 0 {
			hub.NotifyUser(inviteeID, ws.MsgTypeInviteCancelled, "")
		}

		renderRoomDynamic(w, r.Context(), svc, currentID, roomID)
	}
}
