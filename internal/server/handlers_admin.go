package server

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/model"
	"github.com/jchevertonwynne/ssanta/internal/store"
	"github.com/jchevertonwynne/ssanta/internal/ws"
)

type adminData struct {
	CurrentUserID   model.UserID
	CurrentUsername string
	Users           []model.AdminUser
	Rooms           []model.RoomDetail
	ScriptNonce     string
}

// requireAdmin resolves the session user and verifies they have admin status.
func requireAdmin(svc AdminHandlersService, sessions SessionManager, w http.ResponseWriter, r *http.Request) (model.UserID, bool) {
	id, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
	if !ok {
		http.Error(w, "login required", http.StatusUnauthorized)
		return 0, false
	}
	isAdmin, err := svc.IsUserAdmin(r.Context(), id)
	if err != nil {
		slog.ErrorContext(r.Context(), "check admin", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return 0, false
	}
	if !isAdmin {
		http.Error(w, "admin required", http.StatusForbidden)
		return 0, false
	}
	return id, true
}

func renderAdmin(w http.ResponseWriter, r *http.Request, svc AdminHandlersService, adminID model.UserID) {
	view, err := svc.GetAdminView(r.Context(), adminID)
	if err != nil {
		slog.ErrorContext(r.Context(), "get admin view", "err", err)
		http.Error(w, "failed to load admin page", http.StatusInternalServerError)
		return
	}
	render(w, "admin.html", adminData{
		CurrentUserID:   adminID,
		CurrentUsername: view.CurrentUsername,
		Users:           view.Users,
		Rooms:           view.Rooms,
		ScriptNonce:     scriptNonceFromContext(r.Context()),
	})
}

func handleAdminPage(svc AdminHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Hx-Request") == "" {
			render(w, "index.html", indexData{BootstrapURL: "/admin", CSRFToken: CSRFTokenFromContext(r.Context()), ScriptNonce: scriptNonceFromContext(r.Context())})
			return
		}
		adminID, ok := requireAdmin(svc, sessions, w, r)
		if !ok {
			return
		}
		w.Header().Set("Hx-Push-Url", "/admin")
		renderAdmin(w, r, svc, adminID)
	}
}

func handleAdminDeleteUser(svc AdminHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		adminID, ok := requireAdmin(svc, sessions, w, r)
		if !ok {
			return
		}
		targetID, ok := pathUserID(w, r, "id")
		if !ok {
			return
		}
		if err := svc.AdminDeleteUser(r.Context(), adminID, targetID); err != nil {
			switch {
			case errors.Is(err, store.ErrUserNotFound):
				http.Error(w, "user not found", http.StatusNotFound)
			default:
				slog.ErrorContext(r.Context(), "admin delete user", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		if notifier, ok := hub.(interface{ HandleAccountDeletion(userID model.UserID) }); ok {
			notifier.HandleAccountDeletion(targetID)
		}
		hub.NotifyContentUpdate(ws.MsgTypeUsersUpdated)
		renderAdmin(w, r, svc, adminID)
	}
}

func handleAdminDeleteRoom(svc AdminHandlersService, sessions SessionManager, hub Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		adminID, ok := requireAdmin(svc, sessions, w, r)
		if !ok {
			return
		}
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}
		if err := svc.AdminDeleteRoom(r.Context(), adminID, roomID); err != nil {
			switch {
			case errors.Is(err, store.ErrRoomNotFound):
				http.Error(w, "room not found", http.StatusNotFound)
			case errors.Is(err, store.ErrOperationNotAllowedOnDM):
				http.Error(w, err.Error(), http.StatusBadRequest)
			default:
				slog.ErrorContext(r.Context(), "admin delete room", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		hub.DisconnectRoom(roomID)
		renderAdmin(w, r, svc, adminID)
	}
}

func handleAdminSetUserAdmin(svc AdminHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		adminID, ok := requireAdmin(svc, sessions, w, r)
		if !ok {
			return
		}
		targetID, ok := pathUserID(w, r, "id")
		if !ok {
			return
		}
		grant := r.FormValue("grant") == "true"
		if err := svc.SetUserAdmin(r.Context(), adminID, targetID, grant); err != nil {
			switch {
			case errors.Is(err, store.ErrCannotSelfDemote):
				http.Error(w, err.Error(), http.StatusBadRequest)
			case errors.Is(err, store.ErrUserNotFound):
				http.Error(w, "user not found", http.StatusNotFound)
			default:
				slog.ErrorContext(r.Context(), "set user admin", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		renderAdmin(w, r, svc, adminID)
	}
}
