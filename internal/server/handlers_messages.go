package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jchevertonwynne/ssanta/internal/model"
)

type messageResponse struct {
	ID           model.MessageID `json:"id"`
	Username     string          `json:"username"`
	Message      string          `json:"message"`
	CreatedAt    time.Time       `json:"created_at"`
	Whisper      bool            `json:"whisper"`
	TargetUserID *int64          `json:"target_user_id,omitempty"`
	PreEncrypted bool            `json:"pre_encrypted"`
}

func messagesToResponse(msgs []model.Message) []messageResponse {
	out := make([]messageResponse, len(msgs))
	for i, m := range msgs {
		out[i] = messageResponse{
			ID:           m.ID,
			Username:     m.Username,
			Message:      m.Message,
			CreatedAt:    m.CreatedAt,
			Whisper:      m.Whisper,
			PreEncrypted: m.PreEncrypted,
		}
		if m.TargetUserID != nil {
			id := int64(*m.TargetUserID)
			out[i].TargetUserID = &id
		}
	}
	return out
}

//nolint:cyclop
func handleListMessages(svc MessageListService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}

		isCreator, isMember, err := svc.GetRoomAccess(r.Context(), roomID, currentID)
		if err != nil {
			slog.Error("check room access", "err", err, "room_id", roomID) //nolint:gosec
			http.Error(w, "failed to check room access", http.StatusInternalServerError)
			return
		}
		if !isCreator && !isMember {
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		}

		limitStr := r.URL.Query().Get("limit")
		limit := 50
		if limitStr != "" {
			if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 200 {
				limit = v
			}
		}

		beforeIDStr := r.URL.Query().Get("before_id")
		var beforeID model.MessageID
		if beforeIDStr != "" {
			if v, err := strconv.ParseInt(beforeIDStr, 10, 64); err == nil {
				beforeID = model.MessageID(v)
			}
		}

		msgs, err := svc.ListMessages(r.Context(), roomID, currentID, beforeID, limit)
		if err != nil {
			slog.Error("list messages", "err", err, "room_id", roomID) //nolint:gosec
			http.Error(w, "failed to load messages", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messagesToResponse(msgs))
	}
}

//nolint:cyclop
func handleSearchMessages(svc MessageListService, sessions SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentID, ok := resolveSessionUser(r.Context(), svc, sessions, w, r)
		if !ok {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		roomID, ok := pathRoomID(w, r, "id")
		if !ok {
			return
		}

		isCreator, isMember, err := svc.GetRoomAccess(r.Context(), roomID, currentID)
		if err != nil {
			slog.Error("check room access", "err", err, "room_id", roomID) //nolint:gosec
			http.Error(w, "failed to check room access", http.StatusInternalServerError)
			return
		}
		if !isCreator && !isMember {
			http.Error(w, "not a member of this room", http.StatusForbidden)
			return
		}

		query := r.URL.Query().Get("q")
		// Floor at 2 chars to avoid trivial "%a%" scans; cap at 128 to keep
		// the ILIKE pattern bounded. Matches the client's search input limits.
		if n := len(query); n < 2 || n > 128 {
			http.Error(w, "query must be between 2 and 128 characters", http.StatusBadRequest)
			return
		}

		limitStr := r.URL.Query().Get("limit")
		limit := 50
		if limitStr != "" {
			if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 200 {
				limit = v
			}
		}

		msgs, err := svc.SearchMessages(r.Context(), roomID, currentID, query, limit)
		if err != nil {
			slog.Error("search messages", "err", err, "room_id", roomID) //nolint:gosec
			http.Error(w, "failed to search messages", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messagesToResponse(msgs))
	}
}
