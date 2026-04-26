package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/jchevertonwynne/ssanta/internal/model"
	"github.com/jchevertonwynne/ssanta/internal/ratelimit"
	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/store"
	"github.com/jchevertonwynne/ssanta/internal/ws"
)

//go:embed templates/*.html
var templatesFS embed.FS

var templates = template.Must(template.ParseFS(templatesFS, "templates/*.html")) //nolint:gochecknoglobals // parsed templates

type indexData struct {
	BootstrapURL string
	CSRFToken    string
	ScriptNonce  string
}

type contentData struct {
	CurrentUserID       model.UserID
	CurrentUsername     string
	CurrentUserIsAdmin  bool
	Users               []model.User
	CreatedRooms        []model.Room
	MemberRooms         []model.Room
	DMRooms             []service.DMRoomInfo
	Invites             []model.InviteForUser
	RoomFormError       string
	RoomFormAttempted   string
	UserFormError       string
	UserFormAttempted   string
	LoginFormError      string
	LoginFormAttempted  string
	PasswordFormError   string
	PasswordFormSuccess bool
	ScriptNonce         string
}

type roomDetailData struct {
	CurrentUserID       model.UserID
	CurrentUsername     string
	Room                model.RoomDetail
	IsCreator           bool
	IsMember            bool
	IsDMRoom            bool
	CanInvite           bool
	Members             []model.RoomMember
	PendingInvites      []model.InviteForRoom
	DMPartnerName       string
	InvitableUsers      []model.User
	InviteFormError     string
	InviteFormAttempted string
	PGPKeyFormError     string
	PGPKeyFormAttempted string
	PGPVerifyFormError  string
	PGPVerifyAttempted  string
	PGPRemoveFormError  string
	MemberPGPKeysBase64 string
	ScriptNonce         string
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

// RateLimiterConfig holds per-limiter settings. Max <= 0 or Window <= 0 disables the limiter.
type RateLimiterConfig struct {
	Max    int
	Window time.Duration
}

// Options holds optional server wiring parameters with sensible defaults.
type Options struct {
	WSMessageBurst        int
	WSMessageRefillPerSec float64
	TrustProxyHeaders     bool
	RateLimitAuth         RateLimiterConfig
	RateLimitSearch       RateLimiterConfig
	RateLimitRoom         RateLimiterConfig
	RateLimitInvite       RateLimiterConfig
	RateLimitWS           RateLimiterConfig
	RateLimitDM           RateLimiterConfig
}

//nolint:funlen
func New(svc ServerService, sessions SessionManager, serviceName string, metricsHandler http.Handler, metricsSecret string, opts Options) (http.Handler, func()) {
	hub := ws.NewChatHubWithLimits(opts.WSMessageBurst, opts.WSMessageRefillPerSec)
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
	makeLimiter := func(cfg RateLimiterConfig) *ratelimit.RateLimiter {
		if cfg.Max <= 0 || cfg.Window <= 0 {
			return nil
		}
		return ratelimit.New(cfg.Max, cfg.Window, opts.TrustProxyHeaders)
	}
	wrap := func(rl *ratelimit.RateLimiter, h http.HandlerFunc) http.HandlerFunc {
		if rl == nil {
			return h
		}
		return http.HandlerFunc(ratelimit.Middleware(rl)(h).ServeHTTP)
	}
	authRL := makeLimiter(opts.RateLimitAuth)
	// Search does a per-room full scan on ILIKE; keep it in its own bucket so
	// legitimate typing doesn't consume the auth budget.
	searchRL := makeLimiter(opts.RateLimitSearch)
	roomRL := makeLimiter(opts.RateLimitRoom)
	inviteRL := makeLimiter(opts.RateLimitInvite)
	wsRL := makeLimiter(opts.RateLimitWS)
	dmRL := makeLimiter(opts.RateLimitDM)
	limited := func(h http.HandlerFunc) http.HandlerFunc { return wrap(authRL, h) }
	searchLimited := func(h http.HandlerFunc) http.HandlerFunc { return wrap(searchRL, h) }
	roomLimited := func(h http.HandlerFunc) http.HandlerFunc { return wrap(roomRL, h) }
	inviteLimited := func(h http.HandlerFunc) http.HandlerFunc { return wrap(inviteRL, h) }
	wsLimited := func(h http.HandlerFunc) http.HandlerFunc { return wrap(wsRL, h) }
	dmLimited := func(h http.HandlerFunc) http.HandlerFunc { return wrap(dmRL, h) }

	mux.HandleFunc("GET /content/ws", wsLimited(handleContentWebSocket(hub, svc, sessions)))

	// Users
	mux.HandleFunc("POST /users", limited(handleCreateUser(svc, sessions, hubAPI)))
	mux.HandleFunc("DELETE /users/{id}", limited(handleDeleteUser(svc, sessions, hubAPI)))
	mux.HandleFunc("POST /login", limited(handleLogin(svc, sessions)))
	mux.HandleFunc("POST /logout", handleLogout(svc, sessions))
	mux.HandleFunc("POST /password", limited(handleChangePassword(svc, sessions)))

	// Rooms
	mux.HandleFunc("POST /rooms", roomLimited(handleCreateRoom(svc, sessions)))
	mux.HandleFunc("GET /rooms/{id}", handleRoomDetail(svc, sessions))
	mux.HandleFunc("DELETE /rooms/{id}", handleDeleteRoom(svc, sessions, hubAPI))
	mux.HandleFunc("GET /rooms/{id}/sidebar", handleRoomSidebar(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/dynamic", handleRoomDynamic(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/members-list", handleRoomMembersList(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/messages", handleListMessages(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/messages/search", searchLimited(handleSearchMessages(svc, sessions)))
	mux.HandleFunc("GET /rooms/{id}/ws", wsLimited(handleWebSocket(hub, svc, sessions)))
	mux.HandleFunc("POST /rooms/{id}/join", roomLimited(handleJoinRoom(svc, sessions, hubAPI)))
	mux.HandleFunc("POST /rooms/{id}/leave", roomLimited(handleLeaveRoom(svc, sessions, hubAPI)))
	mux.HandleFunc("POST /rooms/{id}/members-can-invite", handleSetMembersCanInvite(svc, sessions))
	mux.HandleFunc("POST /rooms/{id}/pgp-required", handleSetPGPRequired(svc, sessions, hubAPI))
	mux.HandleFunc("POST /rooms/{id}/public", handleSetRoomPublic(svc, sessions))
	mux.HandleFunc("DELETE /rooms/{id}/members/{memberid}", handleRemoveMember(svc, sessions, hubAPI))

	// PGP keys
	mux.HandleFunc("POST /rooms/{id}/pgp-key", handleSetRoomPGPKey(svc, sessions, hubAPI))
	mux.HandleFunc("POST /rooms/{id}/pgp-key/verify", handleVerifyRoomPGPKey(svc, sessions, hubAPI))
	mux.HandleFunc("DELETE /rooms/{id}/pgp-key", handleRemoveRoomPGPKey(svc, sessions, hubAPI))
	mux.HandleFunc("DELETE /rooms/{id}/members/{memberid}/pgp-key", handleRemoveMemberPGPKey(svc, sessions, hubAPI))

	// Invites
	mux.HandleFunc("POST /rooms/{id}/invites", inviteLimited(handleCreateInvite(svc, sessions, hubAPI)))
	mux.HandleFunc("POST /invites/{id}/accept", inviteLimited(handleAcceptInvite(svc, sessions, hubAPI)))
	mux.HandleFunc("POST /invites/{id}/decline", inviteLimited(handleDeclineInvite(svc, sessions)))
	mux.HandleFunc("POST /invites/{id}/cancel", inviteLimited(handleCancelInvite(svc, sessions, hubAPI)))

	// Direct Messages
	mux.HandleFunc("POST /dms", dmLimited(handleCreateOrGetDM(svc, sessions)))
	mux.HandleFunc("GET /dms", handleListDMs(svc, sessions))

	// Admin
	mux.HandleFunc("GET /admin", handleAdminPage(svc, sessions))
	mux.HandleFunc("DELETE /admin/users/{id}", handleAdminDeleteUser(svc, sessions, hubAPI))
	mux.HandleFunc("DELETE /admin/rooms/{id}", handleAdminDeleteRoom(svc, sessions, hubAPI))
	mux.HandleFunc("POST /admin/users/{id}/admin", handleAdminSetUserAdmin(svc, sessions))

	// Apply middleware stack (outermost first)
	handler := Chain(mux,
		RecoverPanic,
		WithScriptNonce,
		SecurityHeaders(sessions.Secure()),
		MaxRequestBody,
		TracingMiddleware(serviceName),
		MetricsMiddleware,
		CSRF(sessions, sessions.Secret(), sessions.Secure()),
		WithRequestLogger(nil),
	)

	closeFn := func() {
		for _, rl := range []*ratelimit.RateLimiter{authRL, searchRL, roomRL, inviteRL, wsRL, dmRL} {
			if rl != nil {
				rl.Close()
			}
		}
		hub.Stop()
	}
	return handler, closeFn
}

func metricsWithSecret(next http.Handler, secret string) http.Handler {
	// Header-only: a query-string fallback would leak the secret into proxy
	// access logs / APM trace attributes.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Metrics-Secret") != secret {
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
	render(w, "index.html", indexData{CSRFToken: CSRFTokenFromContext(r.Context()), ScriptNonce: scriptNonceFromContext(r.Context())})
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
// Cookies are rejected when: the signature is bad, the referenced user no
// longer exists (DB wipe), or the server-side session_version has been bumped
// beyond the one baked into the cookie (password change / force-logout).
func resolveSessionUser(ctx context.Context, svc UserExistsService, sessions SessionManager, w http.ResponseWriter, r *http.Request) (model.UserID, bool) {
	id, cookieVersion, ok := sessions.UserID(r)
	if !ok {
		return 0, false
	}
	serverVersion, err := svc.GetUserSessionVersion(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			sessions.Clear(w)
			return 0, false
		}
		slog.Error("fetch session version", "err", err)
		return 0, false
	}
	if cookieVersion != serverVersion {
		sessions.Clear(w)
		return 0, false
	}
	return id, true
}

func renderContent(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID model.UserID) {
	renderContentData(w, ctx, svc, contentData{CurrentUserID: currentID})
}

func renderContentInvites(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID model.UserID) {
	view, err := svc.GetContentView(ctx, currentID)
	if err != nil {
		slog.Error("get content view", "err", err)
		http.Error(w, "failed to load invites", http.StatusInternalServerError)
		return
	}
	render(w, "content_invites.html", contentData{
		CurrentUserID: currentID,
		Invites:       view.Invites,
		ScriptNonce:   scriptNonceFromContext(ctx),
	})
}

func renderContentUsers(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID model.UserID) {
	view, err := svc.GetContentView(ctx, currentID)
	if err != nil {
		slog.Error("get content view", "err", err)
		http.Error(w, "failed to load users", http.StatusInternalServerError)
		return
	}
	render(w, "content_users.html", contentData{
		CurrentUserID: currentID,
		Users:         view.Users,
		ScriptNonce:   scriptNonceFromContext(ctx),
	})
}

func renderContentWithRoomFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID model.UserID, attempted, formErr string) {
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

func renderContentWithPasswordFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID model.UserID, formErr string) {
	renderContentData(w, ctx, svc, contentData{
		CurrentUserID:     currentID,
		PasswordFormError: formErr,
	})
}

func renderContentWithPasswordSuccess(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID model.UserID) {
	renderContentData(w, ctx, svc, contentData{
		CurrentUserID:       currentID,
		PasswordFormSuccess: true,
	})
}

//nolint:cyclop,funlen
func renderRoom(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID model.UserID, roomID model.RoomID, opts roomRenderOpts) {
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
	verifiedKeys := make(map[model.UserID]string)
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

	memberIDs := make(map[model.UserID]struct{}, len(view.Members))
	for _, m := range view.Members {
		memberIDs[m.ID] = struct{}{}
	}
	pendingIDs := make(map[model.UserID]struct{}, len(view.PendingInvites))
	for _, inv := range view.PendingInvites {
		pendingIDs[inv.InviteeID] = struct{}{}
	}
	invitableUsers := make([]model.User, 0, len(view.AllUsers))
	for _, u := range view.AllUsers {
		if _, isMem := memberIDs[u.ID]; isMem {
			continue
		}
		if _, isPend := pendingIDs[u.ID]; isPend {
			continue
		}
		invitableUsers = append(invitableUsers, u)
	}

	w.Header().Set("Hx-Push-Url", fmt.Sprintf("/rooms/%d", roomID))
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
		MemberPGPKeysBase64: base64.StdEncoding.EncodeToString(keysJSON),
		ScriptNonce:         scriptNonceFromContext(ctx),
	})
}

func renderRoomDetail(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID model.UserID, roomID model.RoomID) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{template: "room_detail.html"})
}

func renderRoomDynamic(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID model.UserID, roomID model.RoomID) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{template: "room_dynamic.html"})
}

func renderRoomSidebar(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID model.UserID, roomID model.RoomID) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{template: "room_sidebar.html"})
}

func renderRoomSidebarWithInviteError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID model.UserID, roomID model.RoomID, attempted, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:        "room_sidebar.html",
		inviteAttempted: attempted,
		inviteErr:       formErr,
	})
}

func renderRoomSidebarWithPGPKeyError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID model.UserID, roomID model.RoomID, attempted, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:        "room_sidebar.html",
		pgpKeyAttempted: attempted,
		pgpKeyErr:       formErr,
	})
}

func renderRoomSidebarWithPGPVerifyError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID model.UserID, roomID model.RoomID, attempted, formErr string) {
	renderRoom(w, ctx, svc, currentID, roomID, roomRenderOpts{
		template:           "room_sidebar.html",
		pgpVerifyAttempted: attempted,
		pgpVerifyErr:       formErr,
	})
}

func renderRoomSidebarWithPGPRemoveError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID model.UserID, roomID model.RoomID, formErr string) {
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
	data.CurrentUserIsAdmin = view.IsAdmin
	data.Users = view.Users
	data.CreatedRooms = view.CreatedRooms
	data.MemberRooms = view.MemberRooms
	data.DMRooms = view.DMRooms
	data.Invites = view.Invites
	data.ScriptNonce = scriptNonceFromContext(ctx)

	w.Header().Set("Hx-Push-Url", "/")
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
	_, _ = buf.WriteTo(w)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode json", "err", err)
	}
}
