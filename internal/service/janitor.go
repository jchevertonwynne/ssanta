package service

import (
	"context"
	"time"
)

// Cleanup deletes/clears expired rows.
// - Invites: expires_at < now
// - Room PGP challenges: pgp_challenge_expires_at < now
func (s *Service) Cleanup(ctx context.Context, now time.Time) (deletedInvites int64, clearedChallenges int64, err error) {
	deletedInvites, err = s.store.Invites.DeleteExpiredInvites(ctx, now)
	if err != nil {
		return 0, 0, err
	}

	clearedChallenges, err = s.store.Rooms.ClearExpiredRoomPGPChallenges(ctx, now)
	if err != nil {
		return 0, 0, err
	}

	return deletedInvites, clearedChallenges, nil
}
