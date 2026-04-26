package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jchevertonwynne/ssanta/internal/model"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

//nolint:cyclop,funlen
func handleCreateOrGetDM(svc DMHandlersService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := loggerFromContext(r.Context())
		contentType := r.Header.Get("Content-Type")
		logger.Info("dm create_or_get start", "content_type", contentType, "content_length", r.ContentLength)

		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			logger.Warn("dm create_or_get unauthorized")
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}

		if strings.HasPrefix(contentType, "multipart/form-data") {
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				logger.Warn("dm parse multipart form", "err", err, "content_type", contentType)
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
		} else {
			r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
			if err := r.ParseForm(); err != nil {
				logger.Warn("dm parse form", "err", err, "content_type", contentType)
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
		}

		partnerIDStr := r.FormValue("partner_id")
		if partnerIDStr == "" {
			logger.Warn("dm missing partner_id", "content_type", contentType, "form_keys", formKeys(r.Form))
			http.Error(w, "partner_id required", http.StatusBadRequest)
			return
		}

		partnerID, err := model.ParseUserID(partnerIDStr)
		if err != nil {
			logger.Warn("dm invalid partner_id format", "partner_id", partnerIDStr)
			http.Error(w, "invalid partner_id format", http.StatusBadRequest)
			return
		}

		// Validate partner exists
		partnerExists, err := svc.UserExists(r.Context(), partnerID)
		if err != nil {
			logger.Error("dm check partner exists", "err", err, "partner_id", partnerID)
			http.Error(w, "failed to check partner", http.StatusInternalServerError)
			return
		}
		if !partnerExists {
			logger.Info("dm partner not found", "partner_id", partnerID)
			http.Error(w, "partner user not found", http.StatusNotFound)
			return
		}

		// Get or create DM room
		roomID, err := svc.GetOrCreateDMRoom(r.Context(), currentID, partnerID)
		if errors.Is(err, store.ErrCannotInviteSelf) {
			logger.Info("dm cannot message self", "user_id", currentID)
			http.Error(w, "cannot message yourself", http.StatusConflict)
			return
		}
		if err != nil {
			logger.Error("dm get or create room", "err", err, "partner_id", partnerID)
			http.Error(w, "failed to create DM", http.StatusInternalServerError)
			return
		}

		// Redirect to the room
		redirectURL := fmt.Sprintf("/rooms/%d", roomID)
		logger.Info("dm create_or_get success", "partner_id", partnerID, "room_id", roomID, "redirect", redirectURL)
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
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
			_ = encoder.Encode(view.DMRooms)
			return
		}

		// Default: render as HTML fragment
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var buf bytes.Buffer
		for _, dm := range view.DMRooms {
			fmt.Fprintf(&buf, `<li><a href="/rooms/%d">%s</a></li>`, dm.RoomID, escapeHTML(dm.PartnerName))
		}
		_, _ = buf.WriteTo(w)
	}
}

// Helper to escape HTML special chars.
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

func formKeys(form map[string][]string) []string {
	if len(form) == 0 {
		return nil
	}
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
