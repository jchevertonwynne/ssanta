package service

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"
)

// Cleanup deletes or clears expired rows.
// - Invites: expires_at < now
// - Room PGP challenges: pgp_challenge_expires_at < now
// - Queued messages: created_at < now - messageQueueMaxAge.
func (s *Service) Cleanup(
	ctx context.Context,
	now time.Time,
	messageQueueMaxAge time.Duration,
) (int64, int64, int64, error) {
	g, gCtx := errgroup.WithContext(ctx)

	var deletedInvites, clearedChallenges, deletedQueuedMessages int64

	g.Go(func() error {
		var err error
		deletedInvites, err = s.store.Invites.DeleteExpiredInvites(gCtx, now)

		return err
	})

	g.Go(func() error {
		var err error
		clearedChallenges, err = s.store.Rooms.ClearExpiredRoomPGPChallenges(gCtx, now)

		return err
	})

	g.Go(func() error {
		var err error
		deletedQueuedMessages, err = s.store.Messages.DeleteOldMessages(gCtx, now.Add(-messageQueueMaxAge))

		return err
	})

	if err := g.Wait(); err != nil {
		return 0, 0, 0, err
	}

	return deletedInvites, clearedChallenges, deletedQueuedMessages, nil
}
