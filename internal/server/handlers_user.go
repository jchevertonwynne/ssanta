package server

import (
	"errors"
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/observability"
	"github.com/jchevertonwynne/ssanta/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func handleCreateUser(svc UserHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := otel.Tracer("ssanta").Start(r.Context(), "CreateUser")
		defer span.End()

		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		attempted := r.FormValue("username")
		password := r.FormValue("password")
		confirm := r.FormValue("password_confirm")
		span.SetAttributes(attribute.String("username", attempted))

		if password != confirm {
			span.SetStatus(codes.Error, "passwords do not match")
			renderContentWithUserFormError(w, ctx, svc, 0, attempted, "passwords do not match")
			return
		}
		id, err := svc.CreateUser(ctx, attempted, password)
		switch {
		case errors.Is(err, store.ErrUsernameInvalid):
			span.SetStatus(codes.Error, err.Error())
			renderContentWithUserFormError(w, ctx, svc, 0, attempted, err.Error())
			return
		case errors.Is(err, store.ErrUsernameTaken):
			span.SetStatus(codes.Error, err.Error())
			renderContentWithUserFormError(w, ctx, svc, 0, attempted, err.Error())
			return
		case errors.Is(err, store.ErrPasswordTooShort):
			span.SetStatus(codes.Error, err.Error())
			renderContentWithUserFormError(w, ctx, svc, 0, attempted, err.Error())
			return
		case err != nil:
			span.SetStatus(codes.Error, err.Error())
			loggerFromContext(ctx).Error("create user", "err", err, "username", attempted)
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}

		span.SetAttributes(attribute.Int64("user_id", id.Int64()))
		loggerFromContext(ctx).Info("user created", "user_id", id, "username", attempted)

		if metrics := observability.GetMetrics(); metrics != nil {
			metrics.UsersRegistered.Add(ctx, 1)
		}

		sessions.Set(w, id)
		renderContent(w, ctx, svc, id)
	}
}

func handleDeleteUser(svc UserHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		id, ok := pathUserID(w, r, "id")
		if !ok {
			return
		}
		if id != currentID {
			http.Error(w, "can only delete your own account", http.StatusForbidden)
			return
		}
		if err := svc.DeleteUser(r.Context(), id); err != nil {
			loggerFromContext(r.Context()).Error("delete user", "err", err)
			http.Error(w, "failed to delete user", http.StatusInternalServerError)
			return
		}
		sessions.Clear(w)
		renderContent(w, r.Context(), svc, 0)
	}
}

func handleLogin(svc UserHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := otel.Tracer("ssanta").Start(r.Context(), "Login")
		defer span.End()

		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		username := r.FormValue("username")
		password := r.FormValue("password")
		span.SetAttributes(attribute.String("username", username))

		id, err := svc.LoginUser(ctx, username, password)
		if errors.Is(err, store.ErrInvalidCredentials) {
			span.SetStatus(codes.Error, "invalid credentials")
			loggerFromContext(ctx).Warn("login failed", "username", username, "reason", "invalid_credentials")
			renderContentWithLoginFormError(w, ctx, svc, username, err.Error())
			return
		}
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			loggerFromContext(ctx).Error("login user", "err", err, "username", username)
			http.Error(w, "failed to log in", http.StatusInternalServerError)
			return
		}

		span.SetAttributes(attribute.Int64("user_id", id.Int64()))
		loggerFromContext(ctx).Info("user logged in", "user_id", id, "username", username)

		if metrics := observability.GetMetrics(); metrics != nil {
			metrics.UsersLoggedIn.Add(ctx, 1)
		}

		sessions.Set(w, id)
		renderContent(w, ctx, svc, id)
	}
}

func handleLogout(svc UserHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions.Clear(w)
		renderContent(w, r.Context(), svc, 0)
	}
}
