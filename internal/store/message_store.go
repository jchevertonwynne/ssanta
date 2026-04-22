package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type MessageStore struct {
	db dbtx
}

type Message struct {
	ID           MessageID
	RoomID       RoomID
	UserID       UserID
	Username     string
	Message      string
	CreatedAt    time.Time
	Whisper      bool
	TargetUserID *UserID
	PreEncrypted bool
	EditedAt     *time.Time
	DeletedAt    *time.Time
}

func (s *MessageStore) CreateMessage(ctx context.Context, roomID RoomID, userID UserID, username, message string, whisper bool, targetUserID *UserID, preEncrypted bool) (MessageID, error) {
	var id MessageID
	err := s.db.QueryRow(ctx,
		`INSERT INTO messages (room_id, user_id, username, message, whisper, target_user_id, pre_encrypted)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		roomID, userID, username, message, whisper, targetUserID, preEncrypted,
	).Scan(&id)
	return id, err
}

func (s *MessageStore) ListMessages(ctx context.Context, roomID RoomID, userID UserID, beforeID MessageID, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var rows pgx.Rows
	var err error
	if beforeID > 0 {
		rows, err = s.db.Query(ctx,
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
		rows, err = s.db.Query(ctx,
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

func (s *MessageStore) SearchMessages(ctx context.Context, roomID RoomID, userID UserID, query string, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	pattern := "%" + query + "%"

	rows, err := s.db.Query(ctx,
		`SELECT id, room_id, user_id, username, message, created_at, whisper, target_user_id, pre_encrypted, edited_at, deleted_at
		 FROM messages
		 WHERE room_id = $1
		   AND deleted_at IS NULL
		   AND (whisper = FALSE OR user_id = $2 OR target_user_id = $2)
		   AND message ILIKE $3
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
