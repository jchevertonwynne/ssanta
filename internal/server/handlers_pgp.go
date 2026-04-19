package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

func handleSetRoomPGPKey(svc RoomHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
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
		attempted := r.FormValue("pgp_public_key")
		err = svc.SetRoomPGPKey(r.Context(), roomID, currentID, attempted)
		switch {
		case errors.Is(err, store.ErrRoomNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrNotRoomMember):
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		case err != nil:
			slog.Error("set room pgp key", "err", err)
			renderRoomDynamicWithPGPKeyError(w, r.Context(), svc, currentID, roomID, attempted, err.Error())
			return
		}

		hub.NotifyRoomUpdate(roomID)
		renderRoomDynamic(w, r.Context(), svc, currentID, roomID)
	}
}

func handleVerifyRoomPGPKey(svc RoomHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
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
		attempted := r.FormValue("decrypted_challenge")
		err = svc.VerifyRoomPGPKey(r.Context(), roomID, currentID, attempted)
		switch {
		case errors.Is(err, store.ErrRoomNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, store.ErrNotRoomMember):
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		case err != nil:
			slog.Error("verify room pgp key", "err", err)
			renderRoomDynamicWithPGPVerifyError(w, r.Context(), svc, currentID, roomID, attempted, err.Error())
			return
		}

		hub.NotifyRoomUpdate(roomID)
		renderRoomDynamic(w, r.Context(), svc, currentID, roomID)
	}
}
