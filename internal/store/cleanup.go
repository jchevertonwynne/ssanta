package store

import (
	"context"
	"time"
)

// DeleteExpiredInvites deletes invites whose expires_at is before now.
func (s *InviteStore) DeleteExpiredInvites(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM room_invites WHERE expires_at < $1`,
		now,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ClearExpiredRoomPGPChallenges clears challenge fields for room members whose
// challenge expiry is in the past.
func (s *RoomStore) ClearExpiredRoomPGPChallenges(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE room_users
		 SET pgp_challenge_ciphertext = NULL,
		     pgp_challenge_hash = NULL,
		     pgp_challenge_expires_at = NULL
		 WHERE pgp_challenge_expires_at IS NOT NULL
		   AND pgp_challenge_expires_at < $1`,
		now,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
