package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

var templates = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

type contentData struct {
	CurrentUserID       store.UserID
	CurrentUsername     string
	Users               []store.User
	CreatedRooms        []store.Room
	MemberRooms         []store.Room
	Invites             []store.InviteForUser
	RoomFormError       string
	RoomFormAttempted   string
	UserFormError       string
	UserFormAttempted   string
	LoginFormError      string
	LoginFormAttempted  string
	PasswordFormError   string
	PasswordFormSuccess bool
}

type roomDetailData struct {
	CurrentUserID       store.UserID
	CurrentUsername     string
	Room                store.RoomDetail
	IsCreator           bool
	IsMember            bool
	CanInvite           bool
	Members             []store.RoomMember
	PendingInvites      []store.InviteForRoom
	InviteFormError     string
	InviteFormAttempted string
	PGPKeyFormError     string
	PGPKeyFormAttempted string
	PGPVerifyFormError  string
	PGPVerifyAttempted  string
	PGPRemoveFormError  string
}

type roomRenderOpts struct {
	template           string
	inviteAttempted    string
	inviteErr          string
	pgpKeyAttempted    string
	pgpKeyErr          string
	pgpVerifyAttempted string
	pgpVerifyErr       string
	pgpRemoveErr       string
}

func New(svc ServerService, sessions SessionManager, serviceName string, metricsHandler http.Handler) (http.Handler, func()) {
	hub := NewChatHub()
	go hub.Run()
	hubAPI := Hub(hub)

	mux := http.NewServeMux()

	// Health & pages
	mux.HandleFunc("GET /healthz", handleHealth(svc))
	mux.Handle("GET /metrics", metricsHandler)
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("GET /content", handleContent(svc, sessions))
	mux.HandleFunc("GET /content/invites", handleContentInvites(svc, sessions))
	mux.HandleFunc("GET /content/ws", handleContentWebSocket(hub, svc, sessions))

	// Users
	mux.HandleFunc("POST /users", handleCreateUser(svc, sessions))
	mux.HandleFunc("DELETE /users/{id}", handleDeleteUser(svc, sessions))
	mux.HandleFunc("POST /login", handleLogin(svc, sessions))
	mux.HandleFunc("POST /logout", handleLogout(svc, sessions))
	mux.HandleFunc("POST /password", handleChangePassword(svc, sessions))

	// Rooms
	mux.HandleFunc("POST /rooms", handleCreateRoom(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}", handleRoomDetail(svc, sessions))
	mux.HandleFunc("DELETE /rooms/{id}", handleDeleteRoom(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/sidebar", handleRoomSidebar(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/dynamic", handleRoomDynamic(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))
	mux.HandleFunc("POST /rooms/{id}/join", handleJoinRoom(svc, sessions, hubAPI))
	mux.HandleFunc("POST /rooms/{id}/leave", handleLeaveRoom(svc, sessions, hubAPI))
	mux.HandleFunc("POST /rooms/{id}/members-can-invite", handleSetMembersCanInvite(svc, sessions))
	mux.HandleFunc("DELETE /rooms/{id}/members/{memberid}", handleRemoveMember(svc, sessions, hubAPI))

	// PGP keys
	mux.HandleFunc("POST /rooms/{id}/pgp-key", handleSetRoomPGPKey(svc, sessions, hubAPI))
	mux.HandleFunc("POST /rooms/{id}/pgp-key/verify", handleVerifyRoomPGPKey(svc, sessions, hubAPI))
	mux.HandleFunc("DELETE /rooms/{id}/pgp-key", handleRemoveRoomPGPKey(svc, sessions, hubAPI))
	mux.HandleFunc("DELETE /rooms/{id}/members/{memberid}/pgp-key", handleRemoveMemberPGPKey(svc, sessions, hubAPI))

	// Invites
	mux.HandleFunc("POST /rooms/{id}/invites", handleCreateInvite(svc, sessions, hubAPI))
	mux.HandleFunc("POST /invites/{id}/accept", handleAcceptInvite(svc, sessions, hubAPI))
	mux.HandleFunc("POST /invites/{id}/decline", handleDeclineInvite(svc, sessions))
	mux.HandleFunc("POST /invites/{id}/cancel", handleCancelInvite(svc, sessions, hubAPI))

	// Apply middleware stack (outermost first)
	handler := Chain(mux,
		RecoverPanic,
		TracingMiddleware(serviceName),
		MetricsMiddleware,
		WithRequestLogger(nil),
	)

	return handler, hub.Stop
}

func handleHealth(svc HealthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Ping(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	render(w, "index.html", nil)
}

func handleContent(svc ContentHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, _ := resolveSessionUser(r.Context(), svc, sessions, w, r)
		renderContent(w, r.Context(), svc, currentID)
	}
}

func handleContentInvites(svc ContentHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		renderContentInvites(w, r.Context(), svc, currentID)
	}
}

// resolveSessionUser returns the logged-in user ID, or 0 if no valid session.
// If the cookie is signed but references a user that no longer exists (e.g.
// after a DB wipe), the cookie is cleared so the caller sees a logged-out state.
func resolveSessionUser(ctx context.Context, svc UserExistsService, sessions SessionManager, w http.ResponseWriter, r *http.Request) (store.UserID, bool) {
	id, ok := sessions.UserID(r)
	if !ok {
		return 0, false
	}
	exists, err := svc.UserExists(ctx, id)
	if err != nil {
		slog.Error("check user exists", "err", err)
		return 0, false
	}
	if !exists {
		sessions.Clear(w)
		return 0, false
	}
	return id, true
}

func renderContent(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID store.UserID) {
	renderContentData(w, ctx, svc, contentData{CurrentUserID: currentID})
}

func renderContentInvites(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID store.UserID) {
	view, err := svc.GetContentView(ctx, currentID)
	if err != nil {
		slog.Error("get content view", "err", err)
		http.Error(w, "failed to load invites", http.StatusInternalServerError)
		return
	}
	render(w, "content_invites.html", contentData{
		CurrentUserID: currentID,
		Invites:       view.Invites,
	})
}

func renderContentWithRoomFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID store.UserID, attempted, formErr string) {
	renderContentData(w, ctx, svc, contentData{
		CurrentUserID:     currentID,
		RoomFormAttempted: attempted,
		RoomFormError:     formErr,
	})
}

func renderContentWithUserFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID store.UserID, attempted, formErr string) {
	renderContentData(w, ctx, svc, contentData{
		CurrentUserID:     currentID,
		UserFormAttempted: attempted,
		UserFormError:     formErr,
	})
}

func renderContentWithLoginFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, attempted, formErr string) {
	renderContentData(w, ctx, svc, contentData{
		LoginFormAttempted: attempted,
		LoginFormError:     formErr,
	})
}

func renderContentWithPasswordFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID store.UserID, formErr string) {
	renderContentData(w, ctx, svc, contentData{
		CurrentUserID:     currentID,
		PasswordFormError: formErr,
	})
}

func renderContentWithPasswordSuccess(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID store.UserID) {
	renderContentData(w, ctx, svc, contentData{
		CurrentUserID:       currentID,
		PasswordFormSuccess: true,
	})
}

func renderRoom(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID, opts roomRenderOpts) {
	view, err := svc.GetRoomDetailView(ctx, roomID, currentID)
	switch {
	case errors.Is(err, store.ErrRoomNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, store.ErrNotRoomMember):
		http.Error(w, "not a member of this room", http.StatusForbidden)
		return
	case err != nil:
		slog.Error("get room detail view", "err", err)
		http.Error(w, "failed to load room", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Push-Url", fmt.Sprintf("/rooms/%d", roomID))
	render(w, opts.template, roomDetailData{
		CurrentUserID:       currentID,
		CurrentUsername:     view.CurrentUsername,
		Room:                view.Room,
		IsCreator:           view.IsCreator,
		IsMember:            view.IsMember,
		CanInvite:           view.CanInvite,
		Members:             view.Members,
		PendingInvites:      view.PendingInvites,
		InviteFormAttempted: opts.inviteAttempted,
		InviteFormError:     opts.inviteErr,
		PGPKeyFormAttempted: opts.pgpKeyAttempted,
		PGPKeyFormError:     opts.pgpKeyErr,
		PGPVerifyAttempted:  opts.pgpVerifyAttempted,
		PGPVerifyFormError:  opts.pgpVerifyErr,
		PGPRemoveFormError:  opts.pgpRemoveErr,
	})
}

func renderRoomDetail(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{template: "room_detail.html"})
}

func renderRoomDetailPage(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{template: "room_detail_page.html"})
}

func renderRoomDynamic(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{template: "room_dynamic.html"})
}

func renderRoomDynamicWithPGPKeyError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID, attempted, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:        "room_dynamic.html",
		pgpKeyAttempted: attempted,
		pgpKeyErr:       formErr,
	})
}

func renderRoomDynamicWithPGPVerifyError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID, attempted, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:           "room_dynamic.html",
		pgpVerifyAttempted: attempted,
		pgpVerifyErr:       formErr,
	})
}

func renderRoomDynamicWithPGPRemoveError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:     "room_dynamic.html",
		pgpRemoveErr: formErr,
	})
}

func renderRoomSidebar(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{template: "room_sidebar.html"})
}

func renderRoomSidebarWithInviteError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID, attempted, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:        "room_sidebar.html",
		inviteAttempted: attempted,
		inviteErr:       formErr,
	})
}

func renderContentData(w http.ResponseWriter, ctx context.Context, svc ContentViewService, data contentData) {
	view, err := svc.GetContentView(ctx, data.CurrentUserID)
	if err != nil {
		slog.Error("get content view", "err", err)
		http.Error(w, "failed to load content", http.StatusInternalServerError)
		return
	}

	data.CurrentUsername = view.CurrentUsername
	data.Users = view.Users
	data.CreatedRooms = view.CreatedRooms
	data.MemberRooms = view.MemberRooms
	data.Invites = view.Invites

	w.Header().Set("HX-Push-Url", "/")
	render(w, "content.html", data)
}

func render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("render", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
