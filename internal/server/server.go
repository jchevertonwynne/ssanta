package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

var templates = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

type contentData struct {
	CurrentUserID     int64
	CurrentUsername   string
	Users             []store.User
	CreatedRooms      []store.Room
	MemberRooms       []store.Room
	Invites           []store.InviteForUser
	RoomFormError     string
	RoomFormAttempted string
	UserFormError     string
	UserFormAttempted string
}

type roomDetailData struct {
	CurrentUserID       int64
	CurrentUsername     string
	Room                store.RoomDetail
	IsCreator           bool
	IsMember            bool
	CanInvite           bool
	Members             []store.User
	PendingInvites      []store.InviteForRoom
	InviteFormError     string
	InviteFormAttempted string
}

func New(svc ServerService, sessions SessionManager) (http.Handler, func()) {
	hub := NewChatHub()
	go hub.Run()
	hubAPI := Hub(hub)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth(svc))
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("GET /content", handleContent(svc, sessions))
	mux.HandleFunc("GET /content/invites", handleContentInvites(svc, sessions))
	mux.HandleFunc("POST /users", handleCreateUser(svc, sessions))
	mux.HandleFunc("DELETE /users/{id}", handleDeleteUser(svc, sessions))
	mux.HandleFunc("POST /login/{id}", handleLogin(svc, sessions))
	mux.HandleFunc("POST /logout", handleLogout(svc, sessions))
	mux.HandleFunc("POST /rooms", handleCreateRoom(svc, sessions))
	mux.HandleFunc("DELETE /rooms/{id}", handleDeleteRoom(svc, sessions))
	mux.HandleFunc("POST /rooms/{id}/join", handleJoinRoom(svc, sessions, hubAPI))
	mux.HandleFunc("POST /rooms/{id}/leave", handleLeaveRoom(svc, sessions, hubAPI))
	mux.HandleFunc("DELETE /rooms/{id}/members/{memberid}", handleRemoveMember(svc, sessions, hubAPI))
	mux.HandleFunc("GET /rooms/{id}", handleRoomDetail(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/sidebar", handleRoomSidebar(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/dynamic", handleRoomDynamic(svc, sessions))
	mux.HandleFunc("GET /rooms/{id}/ws", handleWebSocket(hub, svc, sessions))
	mux.HandleFunc("GET /content/ws", handleContentWebSocket(hub, svc, sessions))
	mux.HandleFunc("POST /rooms/{id}/invites", handleCreateInvite(svc, sessions, hubAPI))
	mux.HandleFunc("POST /rooms/{id}/members-can-invite", handleSetMembersCanInvite(svc, sessions))
	mux.HandleFunc("POST /invites/{id}/accept", handleAcceptInvite(svc, sessions, hubAPI))
	mux.HandleFunc("POST /invites/{id}/decline", handleDeclineInvite(svc, sessions))
	mux.HandleFunc("POST /invites/{id}/cancel", handleCancelInvite(svc, sessions, hubAPI))
	return mux, hub.Stop
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
func resolveSessionUser(ctx context.Context, svc UserExistsService, sessions SessionManager, w http.ResponseWriter, r *http.Request) (int64, bool) {
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

func renderContent(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID int64) {
	renderContentData(w, ctx, svc, contentData{CurrentUserID: currentID})
}

func renderContentInvites(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID int64) {
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

func renderContentWithRoomFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID int64, attempted, formErr string) {
	renderContentData(w, ctx, svc, contentData{
		CurrentUserID:     currentID,
		RoomFormAttempted: attempted,
		RoomFormError:     formErr,
	})
}

func renderContentWithUserFormError(w http.ResponseWriter, ctx context.Context, svc ContentViewService, currentID int64, attempted, formErr string) {
	renderContentData(w, ctx, svc, contentData{
		CurrentUserID:     currentID,
		UserFormAttempted: attempted,
		UserFormError:     formErr,
	})
}

func renderRoomDetail(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID, roomID int64) {
	renderRoomDetailData(w, ctx, svc, roomDetailData{
		CurrentUserID: currentID,
		Room:          store.RoomDetail{Room: store.Room{ID: roomID}},
	})
}

func renderRoomDetailWithInviteError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID, roomID int64, attempted, formErr string) {
	renderRoomDetailData(w, ctx, svc, roomDetailData{
		CurrentUserID:       currentID,
		Room:                store.RoomDetail{Room: store.Room{ID: roomID}},
		InviteFormAttempted: attempted,
		InviteFormError:     formErr,
	})
}

func renderRoomDetailData(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, data roomDetailData) {
	view, err := svc.GetRoomDetailView(ctx, data.Room.ID, data.CurrentUserID)
	if err != nil {
		if errors.Is(err, store.ErrRoomNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, store.ErrNotRoomMember) {
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		}
		slog.Error("get room detail view", "err", err)
		http.Error(w, "failed to load room", http.StatusInternalServerError)
		return
	}

	// Transfer view data to template data
	data.CurrentUsername = view.CurrentUsername
	data.Room = view.Room
	data.IsCreator = view.IsCreator
	data.IsMember = view.IsMember
	data.CanInvite = view.CanInvite
	data.Members = view.Members
	data.PendingInvites = view.PendingInvites

	render(w, "room_detail.html", data)
}

// renderContentData takes a partially-populated contentData (caller supplies
// CurrentUserID and any form error state), fills in the user/room lists plus
// CurrentUsername, and renders the content fragment.
func renderContentData(w http.ResponseWriter, ctx context.Context, svc ContentViewService, data contentData) {
	view, err := svc.GetContentView(ctx, data.CurrentUserID)
	if err != nil {
		slog.Error("get content view", "err", err)
		http.Error(w, "failed to load content", http.StatusInternalServerError)
		return
	}

	// Transfer view data to template data, preserving form error fields
	data.CurrentUsername = view.CurrentUsername
	data.Users = view.Users
	data.CreatedRooms = view.CreatedRooms
	data.MemberRooms = view.MemberRooms
	data.Invites = view.Invites

	render(w, "content.html", data)
}

func renderRoomDetailPage(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID, roomID int64) {
	view, err := svc.GetRoomDetailView(ctx, roomID, currentID)
	if err != nil {
		if errors.Is(err, store.ErrRoomNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, store.ErrNotRoomMember) {
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		}
		slog.Error("get room detail view", "err", err)
		http.Error(w, "failed to load room", http.StatusInternalServerError)
		return
	}

	data := roomDetailData{
		CurrentUserID:   currentID,
		CurrentUsername: view.CurrentUsername,
		Room:            view.Room,
		IsCreator:       view.IsCreator,
		IsMember:        view.IsMember,
		CanInvite:       view.CanInvite,
		Members:         view.Members,
		PendingInvites:  view.PendingInvites,
	}

	render(w, "room_detail_page.html", data)
}

func renderRoomDynamic(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID, roomID int64) {
	view, err := svc.GetRoomDetailView(ctx, roomID, currentID)
	if err != nil {
		if errors.Is(err, store.ErrRoomNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, store.ErrNotRoomMember) {
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		}
		slog.Error("get room detail view", "err", err)
		http.Error(w, "failed to load room", http.StatusInternalServerError)
		return
	}

	data := roomDetailData{
		CurrentUserID:   currentID,
		CurrentUsername: view.CurrentUsername,
		Room:            view.Room,
		IsCreator:       view.IsCreator,
		IsMember:        view.IsMember,
		CanInvite:       view.CanInvite,
		Members:         view.Members,
		PendingInvites:  view.PendingInvites,
	}

	render(w, "room_dynamic.html", data)
}

func renderRoomSidebar(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID, roomID int64) {
	view, err := svc.GetRoomDetailView(ctx, roomID, currentID)
	if err != nil {
		if errors.Is(err, store.ErrRoomNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, store.ErrNotRoomMember) {
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		}
		slog.Error("get room detail view", "err", err)
		http.Error(w, "failed to load room", http.StatusInternalServerError)
		return
	}

	data := roomDetailData{
		CurrentUserID:   currentID,
		CurrentUsername: view.CurrentUsername,
		Room:            view.Room,
		IsCreator:       view.IsCreator,
		IsMember:        view.IsMember,
		CanInvite:       view.CanInvite,
		Members:         view.Members,
		PendingInvites:  view.PendingInvites,
	}

	render(w, "room_sidebar.html", data)
}

func renderRoomSidebarWithInviteError(w http.ResponseWriter, ctx context.Context, svc RoomDetailViewService, currentID, roomID int64, attempted, formErr string) {
	view, err := svc.GetRoomDetailView(ctx, roomID, currentID)
	if err != nil {
		if errors.Is(err, store.ErrRoomNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, store.ErrNotRoomMember) {
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		}
		slog.Error("get room detail view", "err", err)
		http.Error(w, "failed to load room", http.StatusInternalServerError)
		return
	}

	data := roomDetailData{
		CurrentUserID:       currentID,
		CurrentUsername:     view.CurrentUsername,
		Room:                view.Room,
		IsCreator:           view.IsCreator,
		IsMember:            view.IsMember,
		CanInvite:           view.CanInvite,
		Members:             view.Members,
		PendingInvites:      view.PendingInvites,
		InviteFormAttempted: attempted,
		InviteFormError:     formErr,
	}

	render(w, "room_sidebar.html", data)
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
