package store

import (
	"context"
	"time"
)

// DeleteExpiredInvites deletes invites whose expires_at is before now.
func (s *inviteStore) DeleteExpiredInvites(ctx context.Context, now time.Time) (int64, error) {
	// Set a short lock timeout to avoid blocking on contended tables
	// Use LIMIT to process in batches and avoid holding locks too long
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
		return 0, err
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM room_invites 
		 WHERE id IN (
		     SELECT id FROM room_invites 
		     WHERE expires_at < $1 
		     LIMIT 1000
		 )`,
		now,
	)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ClearExpiredRoomPGPChallenges clears challenge fields for room members whose
// challenge expiry is in the past.
func (s *roomStore) ClearExpiredRoomPGPChallenges(ctx context.Context, now time.Time) (int64, error) {
	// Set a short lock timeout to avoid blocking on contended tables
	// Use LIMIT to process in batches and avoid holding locks too long
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
		return 0, err
	}

	tag, err := tx.Exec(ctx,
		`UPDATE room_users
		 SET pgp_challenge_ciphertext = NULL,
		     pgp_challenge_hash = NULL,
		     pgp_challenge_expires_at = NULL
		 WHERE (room_id, user_id) IN (
		     SELECT room_id, user_id FROM room_users
		     WHERE pgp_challenge_expires_at IS NOT NULL
		       AND pgp_challenge_expires_at < $1
		     LIMIT 1000
		 )`,
		now,
	)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
