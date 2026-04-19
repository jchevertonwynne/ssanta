package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

func handleCreateUser(svc UserHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		attempted := r.FormValue("username")
		password := r.FormValue("password")
		confirm := r.FormValue("password_confirm")
		if password != confirm {
			renderContentWithUserFormError(w, r.Context(), svc, 0, attempted, "passwords do not match")
			return
		}
		id, err := svc.CreateUser(r.Context(), attempted, password)
		switch {
		case errors.Is(err, store.ErrUsernameInvalid):
			renderContentWithUserFormError(w, r.Context(), svc, 0, attempted, err.Error())
			return
		case errors.Is(err, store.ErrUsernameTaken):
			renderContentWithUserFormError(w, r.Context(), svc, 0, attempted, err.Error())
			return
		case errors.Is(err, store.ErrPasswordTooShort):
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

func handleDeleteUser(svc UserHandlersService, sessions SessionManager) http.HandlerFunc {
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

func handleLogin(svc UserHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		username := r.FormValue("username")
		password := r.FormValue("password")
		id, err := svc.LoginUser(r.Context(), username, password)
		if errors.Is(err, store.ErrInvalidCredentials) {
			renderContentWithLoginFormError(w, r.Context(), svc, username, err.Error())
			return
		}
		if err != nil {
			slog.Error("login user", "err", err)
			http.Error(w, "failed to log in", http.StatusInternalServerError)
			return
		}
		sessions.Set(w, id)
		renderContent(w, r.Context(), svc, id)
	}
}

func handleLogout(svc UserHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions.Clear(w)
		renderContent(w, r.Context(), svc, 0)
	}
}
