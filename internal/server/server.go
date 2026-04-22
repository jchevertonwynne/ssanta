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
	"time"

	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

var templates = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

type indexData struct {
	BootstrapURL string
	CSRFToken    string
}

type contentData struct {
	CurrentUserID       store.UserID
	CurrentUsername     string
	Users               []store.User
	CreatedRooms        []store.Room
	MemberRooms         []store.Room
	DMRooms             []service.DMRoomInfo
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
	IsDMRoom            bool
	CanInvite           bool
	Members             []store.RoomMember
	PendingInvites      []store.InviteForRoom
	DMPartnerName       string
	InvitableUsers      []store.User
	InviteFormError     string
	InviteFormAttempted string
	PGPKeyFormError     string
	PGPKeyFormAttempted string
	PGPVerifyFormError  string
	PGPVerifyAttempted  string
	PGPRemoveFormError  string
	MemberPGPKeysJSON   template.JS
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

func New(svc ServerService, sessions SessionManager, serviceName string, metricsHandler http.Handler, metricsSecret string, rateLimitMax int, rateLimitWindow time.Duration) (http.Handler, func()) {
	hub := NewChatHub()
	go hub.Run()
	hubAPI := Hub(hub)

	mux := http.NewServeMux()

	// Health & pages
	mux.HandleFunc("GET /healthz", handleHealth(svc))
	if metricsSecret != "" {
		metricsHandler = metricsWithSecret(metricsHandler, metricsSecret)
	}
	mux.Handle("GET /metrics", metricsHandler)
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("GET /content", handleContent(svc, sessions))
	mux.HandleFunc("GET /content/invites", handleContentInvites(svc, sessions))
	mux.HandleFunc("GET /content/users", handleContentUsers(svc, sessions))
	mux.HandleFunc("GET /content/ws", handleContentWebSocket(hub, svc, sessions))

	var authLimiter *rateLimiter
	if rateLimitMax > 0 && rateLimitWindow > 0 {
		authLimiter = newRateLimiter(rateLimitMax, rateLimitWindow)
	}
	limited := func(h http.HandlerFunc) http.HandlerFunc {
		if authLimiter == nil {
			return h
		}
		return http.HandlerFunc(RateLimit(authLimiter)(h).ServeHTTP)
	}

	// Users
	mux.HandleFunc("POST /users", limited(handleCreateUser(svc, sessions, hubAPI)))
	mux.HandleFunc("DELETE /users/{id}", limited(handleDeleteUser(svc, sessions, hubAPI)))
	mux.HandleFunc("POST /login", limited(handleLogin(svc, sessions)))
	mux.HandleFunc("POST /logout", handleLogout(svc, sessions))
	mux.HandleFunc("POST /password", limited(handleChangePassword(svc, sessions)))

	// Rooms
	mux.HandleFunc("POST /rooms", handleCreateRoom(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}", handleRoomDetail(svc, sessions))
	mux.HandleFunc("DELETE /rooms/{id}", handleDeleteRoom(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/sidebar", handleRoomSidebar(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/dynamic", handleRoomDynamic(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/members-list", handleRoomMembersList(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/messages", handleListMessages(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/messages/search", handleSearchMessages(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))
	mux.HandleFunc("POST /rooms/{id}/join", handleJoinRoom(svc, sessions, hubAPI))
	mux.HandleFunc("POST /rooms/{id}/leave", handleLeaveRoom(svc, sessions, hubAPI))
	mux.HandleFunc("POST /rooms/{id}/members-can-invite", handleSetMembersCanInvite(svc, sessions))
	mux.HandleFunc("POST /rooms/{id}/pgp-required", handleSetPGPRequired(svc, sessions))
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

	// Direct Messages
	mux.HandleFunc("POST /dms", handleCreateOrGetDM(svc, sessions))
	mux.HandleFunc("GET /dms", handleListDMs(svc, sessions))

	// Apply middleware stack (outermost first)
	handler := Chain(mux,
		RecoverPanic,
		SecurityHeaders(sessions.Secure()),
		MaxRequestBody,
		TracingMiddleware(serviceName),
		MetricsMiddleware,
		CSRF(sessions.Secret(), sessions.Secure()),
		WithRequestLogger(nil),
	)

	return handler, hub.Stop
}

func metricsWithSecret(next http.Handler, secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.URL.Query().Get("secret")
		if provided == "" {
			provided = r.Header.Get("X-Metrics-Secret")
		}
		if provided != secret {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
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
	render(w, "index.html", indexData{CSRFToken: CSRFTokenFromContext(r.Context())})
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

func handleContentUsers(svc ContentHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, _ := resolveSessionUser(r.Context(), svc, sessions, w, r)
		renderContentUsers(w, r.Context(), svc, currentID)
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

func renderContentUsers(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID store.UserID) {
	view, err := svc.GetContentView(ctx, currentID)
	if err != nil {
		slog.Error("get content view", "err", err)
		http.Error(w, "failed to load users", http.StatusInternalServerError)
		return
	}
	render(w, "content_users.html", contentData{
		CurrentUserID: currentID,
		Users:         view.Users,
	})
}

func renderContentWithRoomFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID store.UserID, attempted, formErr string) {
	renderContentData(w, ctx, svc, contentData{
		CurrentUserID:     currentID,
		RoomFormAttempted: attempted,
		RoomFormError:     formErr,
	})
}

func renderContentWithUserFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, attempted, formErr string) {
	renderContentData(w, ctx, svc, contentData{
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
	verifiedKeys := make(map[store.UserID]string)
	for _, m := range view.Members {
		if m.PGPPublicKey != "" && m.PGPVerifiedAt != nil {
			verifiedKeys[m.ID] = m.PGPPublicKey
		}
	}
	keysJSON, err := json.Marshal(verifiedKeys)
	if err != nil {
		slog.Error("marshal member pgp keys", "err", err)
		http.Error(w, "failed to load room", http.StatusInternalServerError)
		return
	}

	memberIDs := make(map[store.UserID]struct{}, len(view.Members))
	for _, m := range view.Members {
		memberIDs[m.ID] = struct{}{}
	}
	pendingIDs := make(map[store.UserID]struct{}, len(view.PendingInvites))
	for _, inv := range view.PendingInvites {
		pendingIDs[inv.InviteeID] = struct{}{}
	}
	invitableUsers := make([]store.User, 0, len(view.AllUsers))
	for _, u := range view.AllUsers {
		if _, isMem := memberIDs[u.ID]; isMem {
			continue
		}
		if _, isPend := pendingIDs[u.ID]; isPend {
			continue
		}
		invitableUsers = append(invitableUsers, u)
	}

	w.Header().Set("HX-Push-Url", fmt.Sprintf("/rooms/%d", roomID))
	render(w, opts.template, roomDetailData{
		CurrentUserID:       currentID,
		CurrentUsername:     view.CurrentUsername,
		Room:                view.Room,
		IsCreator:           view.IsCreator,
		IsMember:            view.IsMember,
		IsDMRoom:            view.IsDMRoom,
		CanInvite:           view.CanInvite,
		Members:             view.Members,
		PendingInvites:      view.PendingInvites,
		DMPartnerName:       view.DMPartnerName,
		InvitableUsers:      invitableUsers,
		InviteFormAttempted: opts.inviteAttempted,
		InviteFormError:     opts.inviteErr,
		PGPKeyFormAttempted: opts.pgpKeyAttempted,
		PGPKeyFormError:     opts.pgpKeyErr,
		PGPVerifyAttempted:  opts.pgpVerifyAttempted,
		PGPVerifyFormError:  opts.pgpVerifyErr,
		PGPRemoveFormError:  opts.pgpRemoveErr,
		MemberPGPKeysJSON:   template.JS(keysJSON), //nolint:gosec // JSON-encoded map of public keys, safe for script context
	})
}

func renderRoomDetail(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{template: "room_detail.html"})
}

func renderRoomDynamic(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{template: "room_dynamic.html"})
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

func renderRoomSidebarWithPGPKeyError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID, attempted, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:        "room_sidebar.html",
		pgpKeyAttempted: attempted,
		pgpKeyErr:       formErr,
	})
}

func renderRoomSidebarWithPGPVerifyError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID, attempted, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:           "room_sidebar.html",
		pgpVerifyAttempted: attempted,
		pgpVerifyErr:       formErr,
	})
}

func renderRoomSidebarWithPGPRemoveError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID store.UserID, roomID store.RoomID, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:     "room_sidebar.html",
		pgpRemoveErr: formErr,
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
	data.DMRooms = view.DMRooms
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
	_, _ = buf.WriteTo(w) //nolint:errcheck
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
