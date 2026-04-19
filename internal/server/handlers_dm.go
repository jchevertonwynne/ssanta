package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

func handleCreateOrGetDM(svc DMHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		partnerIDStr := r.FormValue("partner_id")
		if partnerIDStr == "" {
			http.Error(w, "partner_id required", http.StatusBadRequest)
			return
		}

		partnerID64, err := strconv.ParseInt(partnerIDStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid partner_id", http.StatusBadRequest)
			return
		}
		partnerID := store.UserID(partnerID64)

		// Validate partner exists
		partnerExists, err := svc.UserExists(r.Context(), partnerID)
		if err != nil {
			loggerFromContext(r.Context()).Error("check user exists", "err", err)
			http.Error(w, "failed to check partner", http.StatusInternalServerError)
			return
		}
		if !partnerExists {
			http.Error(w, "partner user not found", http.StatusNotFound)
			return
		}

		// Get or create DM room
		roomID, err := svc.GetOrCreateDMRoom(r.Context(), currentID, partnerID)
		if errors.Is(err, store.ErrCannotInviteSelf) {
			http.Error(w, "cannot message yourself", http.StatusConflict)
			return
		}
		if err != nil {
			loggerFromContext(r.Context()).Error("get or create dm room", "err", err)
			http.Error(w, "failed to create DM", http.StatusInternalServerError)
			return
		}

		// Redirect to the room
		http.Redirect(w, r, fmt.Sprintf("/rooms/%d", roomID), http.StatusSeeOther)
	}
}

func handleListDMs(svc DMHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}

		// Get content view which now includes DM rooms
		view, err := svc.GetContentView(r.Context(), currentID)
		if err != nil {
			loggerFromContext(r.Context()).Error("get content view", "err", err)
			http.Error(w, "failed to load DMs", http.StatusInternalServerError)
			return
		}

		// For format=json, return JSON
		format := r.URL.Query().Get("format")
		if format == "json" {
			w.Header().Set("Content-Type", "application/json")
			encoder := json.NewEncoder(w)
			_ = encoder.Encode(view.DMRooms) //nolint:errcheck
			return
		}

		// Default: render as HTML fragment
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var buf bytes.Buffer
		for _, dm := range view.DMRooms {
			fmt.Fprintf(&buf, `<li><a href="/rooms/%d">%s</a></li>`, dm.RoomID, escapeHTML(dm.PartnerName))
		}
		_, _ = buf.WriteTo(w) //nolint:errcheck
	}
}

// Helper to escape HTML special chars
func escapeHTML(s string) string {
	if s == "" {
		return ""
	}
	var buf bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			buf.WriteString("&amp;")
		case '<':
			buf.WriteString("&lt;")
		case '>':
			buf.WriteString("&gt;")
		case '"':
			buf.WriteString("&quot;")
		case '\'':
			buf.WriteString("&#39;")
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String()
}
