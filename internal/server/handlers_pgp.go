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

func handleSetRoomPGPKey(svc RoomHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}

		ctx, span := otel.Tracer("ssanta").Start(r.Context(), "SetRoomPGPKey")
		defer span.End()

		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		attempted := r.FormValue("pgp_public_key")
		span.SetAttributes(
			attribute.Int64("room_id", roomID.Int64()),
			attribute.Int64("user_id", currentID.Int64()),
		)

		err := svc.SetRoomPGPKey(ctx, roomID, currentID, attempted)
		switch {
		case errors.Is(err, store.ErrRoomNotFound):
			span.SetStatus(codes.Error, err.Error())
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrNotRoomMember):
			span.SetStatus(codes.Error, "not a member")
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		case err != nil:
			span.SetStatus(codes.Error, err.Error())
			loggerFromContext(ctx).Error("set room pgp key", "err", err, "room_id", roomID)
			renderRoomDynamicWithPGPKeyError(w, ctx, svc, currentID, roomID, attempted, err.Error())
			return
		}

		loggerFromContext(ctx).Info("pgp key uploaded", "room_id", roomID, "user_id", currentID)

		if metrics := observability.GetMetrics(); metrics != nil {
			metrics.PGPKeysUploaded.Add(ctx, 1)
		}

		hub.NotifyRoomUpdate(roomID)
		renderRoomDynamic(w, ctx, svc, currentID, roomID)
	}
}

func handleVerifyRoomPGPKey(svc RoomHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
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
		attempted := r.FormValue("decrypted_challenge")
		err := svc.VerifyRoomPGPKey(r.Context(), roomID, currentID, attempted)
		switch {
		case errors.Is(err, store.ErrRoomNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrNotRoomMember):
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		case err != nil:
			loggerFromContext(r.Context()).Error("verify room pgp key", "err", err)
			renderRoomDynamicWithPGPVerifyError(w, r.Context(), svc, currentID, roomID, attempted, err.Error())
			return
		}

		hub.NotifyRoomUpdate(roomID)
		renderRoomDynamic(w, r.Context(), svc, currentID, roomID)
	}
}

func handleRemoveRoomPGPKey(svc RoomHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actingUserID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}

		err := svc.RemoveRoomUserPGPKey(r.Context(), roomID, actingUserID, actingUserID)
		switch {
		case errors.Is(err, store.ErrRoomNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrNotRoomMember):
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		case err != nil:
			loggerFromContext(r.Context()).Error("remove room pgp key", "err", err)
			renderRoomDynamicWithPGPRemoveError(w, r.Context(), svc, actingUserID, roomID, err.Error())
			return
		}

		hub.NotifyRoomUpdate(roomID)
		renderRoomDynamic(w, r.Context(), svc, actingUserID, roomID)
	}
}

func handleRemoveMemberPGPKey(svc RoomHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actingUserID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}
		targetUserID, ok := pathUserID(w, r, "memberid")
		if !ok {
			return
		}

		err := svc.RemoveRoomUserPGPKey(r.Context(), roomID, targetUserID, actingUserID)
		switch {
		case errors.Is(err, store.ErrNotRoomCreator):
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		case errors.Is(err, store.ErrNotRoomMember):
			http.Error(w, "user is not a member", http.StatusNotFound)
			return
		case err != nil:
			loggerFromContext(r.Context()).Error("remove member pgp key", "err", err)
			renderRoomDynamicWithPGPRemoveError(w, r.Context(), svc, actingUserID, roomID, err.Error())
			return
		}

		hub.NotifyRoomUpdate(roomID)
		renderRoomDynamic(w, r.Context(), svc, actingUserID, roomID)
	}
}
