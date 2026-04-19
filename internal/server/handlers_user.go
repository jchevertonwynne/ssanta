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

func handleCreateUser(svc *service.Service, sessions *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		attempted := r.FormValue("username")
		id, err := svc.CreateUser(r.Context(), attempted)
		switch {
		case errors.Is(err, store.ErrUsernameInvalid):
			renderContentWithUserFormError(w, r.Context(), svc, 0, attempted, err.Error())
			return
		case errors.Is(err, store.ErrUsernameTaken):
			renderContentWithUserFormError(w, r.Context(), svc, 0, attempted, err.Error())
			return
		case err != nil:
			slog.Error("create user", "err", err)
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}
		sessions.Set(w, id)
		renderContent(w, r.Context(), svc, id)
	}
}

func handleDeleteUser(svc *service.Service, sessions *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid user id", http.StatusBadRequest)
			return
		}
		if id != currentID {
			http.Error(w, "can only delete your own account", http.StatusForbidden)
			return
		}
		if err := svc.DeleteUser(r.Context(), id); err != nil {
			slog.Error("delete user", "err", err)
			http.Error(w, "failed to delete user", http.StatusInternalServerError)
			return
		}
		sessions.Clear(w)
		renderContent(w, r.Context(), svc, 0)
	}
}

func handleLogin(svc *service.Service, sessions *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid user id", http.StatusBadRequest)
			return
		}
		exists, err := svc.UserExists(r.Context(), id)
		if err != nil {
			slog.Error("check user exists", "err", err)
			http.Error(w, "failed to log in", http.StatusInternalServerError)
			return
		}
		if !exists {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		sessions.Set(w, id)
		renderContent(w, r.Context(), svc, id)
	}
}

func handleLogout(svc *service.Service, sessions *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions.Clear(w)
		renderContent(w, r.Context(), svc, 0)
	}
}
