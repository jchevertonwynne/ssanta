package store

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jchevertonwynne/ssanta/internal/db"
)

type messageStore struct {
	pool *pgxpool.Pool
	// ilikeEscaper escapes LIKE/ILIKE pattern metacharacters so caller-supplied
	// text is matched literally. Order matters: \ is rewritten first so the
	// subsequent replacements don't double-escape.
	ilikeEscaper *strings.Replacer
}

func (s *messageStore) CreateMessage(ctx context.Context, roomID RoomID, userID UserID, username, message string, whisper bool, targetUserID *UserID, preEncrypted bool) (MessageID, error) {
	ctx = db.WithQueryName(ctx, "create_message")
	var id MessageID
	err := s.pool.QueryRow(ctx,
		`INSERT INTO messages (room_id, user_id, username, message, whisper, target_user_id, pre_encrypted)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		roomID, userID, username, message, whisper, targetUserID, preEncrypted,
	).Scan(&id)
	return id, err
}

func (s *messageStore) ListMessages(ctx context.Context, roomID RoomID, userID UserID, beforeID MessageID, limit int) ([]Message, error) {
	ctx = db.WithQueryName(ctx, "list_messages")
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var rows pgx.Rows
	var err error
	if beforeID > 0 {
		rows, err = s.pool.Query(ctx,
			`SELECT id, room_id, user_id, username, message, created_at, whisper, target_user_id, pre_encrypted, edited_at, deleted_at
			 FROM messages
			 WHERE room_id = $1
			   AND deleted_at IS NULL
			   AND (whisper = FALSE OR user_id = $2 OR target_user_id = $2)
			   AND id < $3
			 ORDER BY id DESC LIMIT $4`,
			roomID, userID, beforeID, limit,
		)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT id, room_id, user_id, username, message, created_at, whisper, target_user_id, pre_encrypted, edited_at, deleted_at
			 FROM messages
			 WHERE room_id = $1
			   AND deleted_at IS NULL
			   AND (whisper = FALSE OR user_id = $2 OR target_user_id = $2)
			 ORDER BY id DESC LIMIT $3`,
			roomID, userID, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var targetUserID *UserID
		err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Message, &m.CreatedAt, &m.Whisper, &targetUserID, &m.PreEncrypted, &m.EditedAt, &m.DeletedAt)
		if err != nil {
			return nil, err
		}
		m.TargetUserID = targetUserID
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (s *messageStore) ListMessagesAfterID(ctx context.Context, roomID RoomID, userID UserID, afterID MessageID, limit int) ([]Message, error) {
	ctx = db.WithQueryName(ctx, "list_messages_after_id")
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, room_id, user_id, username, message, created_at, whisper, target_user_id, pre_encrypted, edited_at, deleted_at
		 FROM messages
		 WHERE room_id = $1
		   AND deleted_at IS NULL
		   AND (whisper = FALSE OR user_id = $2 OR target_user_id = $2)
		   AND id > $3
		 ORDER BY id ASC LIMIT $4`,
		roomID, userID, afterID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var targetUserID *UserID
		err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Message, &m.CreatedAt, &m.Whisper, &targetUserID, &m.PreEncrypted, &m.EditedAt, &m.DeletedAt)
		if err != nil {
			return nil, err
		}
		m.TargetUserID = targetUserID
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (s *messageStore) SearchMessages(ctx context.Context, roomID RoomID, userID UserID, query string, limit int) ([]Message, error) {
	ctx = db.WithQueryName(ctx, "search_messages")
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	pattern := "%" + s.ilikeEscaper.Replace(query) + "%"

	rows, err := s.pool.Query(ctx,
		`SELECT id, room_id, user_id, username, message, created_at, whisper, target_user_id, pre_encrypted, edited_at, deleted_at
		 FROM messages
		 WHERE room_id = $1
		   AND deleted_at IS NULL
		   AND (whisper = FALSE OR user_id = $2 OR target_user_id = $2)
		   AND message ILIKE $3 ESCAPE '\'
		 ORDER BY id DESC LIMIT $4`,
		roomID, userID, pattern, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var targetUserID *UserID
		err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Message, &m.CreatedAt, &m.Whisper, &targetUserID, &m.PreEncrypted, &m.EditedAt, &m.DeletedAt)
		if err != nil {
			return nil, err
		}
		m.TargetUserID = targetUserID
		messages = append(messages, m)
	}
	return messages, rows.Err()
}
