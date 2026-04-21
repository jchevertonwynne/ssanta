package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// QueuedMessage is a single row from the message_queue table.
type QueuedMessage struct {
	SenderUsername string
	CreatedAt      time.Time
	Message        string
	PreEncrypted   bool
	Whisper        bool
}

type MessageQueueStore struct {
	db dbtx
}

// Enqueue inserts one row per recipient into the message queue.
func (s *MessageQueueStore) Enqueue(
	ctx context.Context,
	roomID RoomID,
	senderUsername, message string,
	createdAt time.Time,
	preEncrypted, whisper bool,
	recipientIDs []UserID,
) error {
	for _, uid := range recipientIDs {
		if _, err := s.db.Exec(ctx,
			`INSERT INTO message_queue
			     (room_id, recipient_id, sender_username, created_at, message, pre_encrypted, whisper)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			roomID, uid, senderUsername, createdAt, message, preEncrypted, whisper,
		); err != nil {
			return err
		}
	}
	return nil
}

// DeleteOldMessages deletes queued messages whose created_at is before cutoff.
func (s *MessageQueueStore) DeleteOldMessages(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM message_queue WHERE created_at < $1`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// FlushForUser returns and deletes all queued messages for a user in a room,
// ordered by created_at, in a single transaction.
func (s *MessageQueueStore) FlushForUser(
	ctx context.Context,
	roomID RoomID,
	userID UserID,
) ([]QueuedMessage, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx,
		`SELECT sender_username, created_at, message, pre_encrypted, whisper
		 FROM message_queue
		 WHERE room_id = $1 AND recipient_id = $2
		 ORDER BY created_at
		 FOR UPDATE`,
		roomID, userID,
	)
	if err != nil {
		return nil, err
	}
	msgs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (QueuedMessage, error) {
		var m QueuedMessage
		return m, row.Scan(&m.SenderUsername, &m.CreatedAt, &m.Message, &m.PreEncrypted, &m.Whisper)
	})
	if err != nil {
		return nil, err
	}

	if len(msgs) > 0 {
		if _, err := tx.Exec(ctx,
			`DELETE FROM message_queue WHERE room_id = $1 AND recipient_id = $2`,
			roomID, userID,
		); err != nil {
			return nil, err
		}
	}

	return msgs, tx.Commit(ctx)
}
