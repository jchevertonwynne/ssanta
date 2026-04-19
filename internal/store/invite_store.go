package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type InviteStore struct {
	pool *pgxpool.Pool
}

func (s *InviteStore) CreateInvite(ctx context.Context, roomID, inviterID int64, inviteeUsername string) error {
	inviteeName := strings.TrimSpace(inviteeUsername)
	if inviteeName == "" {
		return ErrInviteeNotFound
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var creatorID int64
	var membersCanInvite bool
	err = tx.QueryRow(ctx,
		`SELECT creator_id, members_can_invite FROM rooms WHERE id = $1`,
		roomID,
	).Scan(&creatorID, &membersCanInvite)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRoomNotFound
	}
	if err != nil {
		return err
	}

	if inviterID != creatorID {
		if !membersCanInvite {
			return ErrNotAllowedToInvite
		}
		var isMember bool
		err = tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM room_users WHERE room_id = $1 AND user_id = $2)`,
			roomID, inviterID,
		).Scan(&isMember)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrNotAllowedToInvite
		}
	}

	var inviteeID int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM users WHERE username = $1`,
		inviteeName,
	).Scan(&inviteeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInviteeNotFound
	}
	if err != nil {
		return err
	}

	if inviteeID == inviterID {
		return ErrCannotInviteSelf
	}

	if inviteeID == creatorID {
		return ErrAlreadyMember
	}

	var alreadyMember bool
	err = tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM room_users WHERE room_id = $1 AND user_id = $2)`,
		roomID, inviteeID,
	).Scan(&alreadyMember)
	if err != nil {
		return err
	}
	if alreadyMember {
		return ErrAlreadyMember
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO room_invites (room_id, inviter_id, invitee_id) VALUES ($1, $2, $3)`,
		roomID, inviterID, inviteeID,
	)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrAlreadyInvited
	}
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *InviteStore) ListInvitesForUser(ctx context.Context, userID int64) ([]InviteForUser, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT i.id, r.id, r.display_name, u.id, u.username, i.created_at
		 FROM room_invites i
		 JOIN rooms r ON r.id = i.room_id
		 JOIN users u ON u.id = i.inviter_id
		 WHERE i.invitee_id = $1
		 ORDER BY i.created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invites []InviteForUser
	for rows.Next() {
		var inv InviteForUser
		if err := rows.Scan(&inv.InviteID, &inv.RoomID, &inv.RoomName, &inv.InviterID, &inv.InviterName, &inv.CreatedAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

func (s *InviteStore) ListInvitesForRoom(ctx context.Context, roomID int64) ([]InviteForRoom, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT i.id, inviter.id, inviter.username, invitee.id, invitee.username, i.created_at
		 FROM room_invites i
		 JOIN users inviter ON inviter.id = i.inviter_id
		 JOIN users invitee ON invitee.id = i.invitee_id
		 WHERE i.room_id = $1
		 ORDER BY i.created_at DESC`,
		roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invites []InviteForRoom
	for rows.Next() {
		var inv InviteForRoom
		if err := rows.Scan(&inv.InviteID, &inv.InviterID, &inv.InviterName, &inv.InviteeID, &inv.InviteeName, &inv.CreatedAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

func (s *InviteStore) AcceptInvite(ctx context.Context, inviteID, userID int64) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var roomID, inviteeID int64
	err = tx.QueryRow(ctx,
		`SELECT room_id, invitee_id FROM room_invites WHERE id = $1 FOR UPDATE`,
		inviteID,
	).Scan(&roomID, &inviteeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInviteNotFound
	}
	if err != nil {
		return err
	}
	if inviteeID != userID {
		return ErrInviteNotFound
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO room_users (room_id, user_id) VALUES ($1, $2)
		 ON CONFLICT (room_id, user_id) DO NOTHING`,
		roomID, userID,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `DELETE FROM room_invites WHERE id = $1`, inviteID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *InviteStore) DeclineInvite(ctx context.Context, inviteID, userID int64) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM room_invites WHERE id = $1 AND invitee_id = $2`,
		inviteID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInviteNotFound
	}
	return nil
}

func (s *InviteStore) CancelInvite(ctx context.Context, inviteID, actingUserID int64) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var inviterID, creatorID int64
	err = tx.QueryRow(ctx,
		`SELECT i.inviter_id, r.creator_id
		 FROM room_invites i
		 JOIN rooms r ON r.id = i.room_id
		 WHERE i.id = $1
		 FOR UPDATE OF i`,
		inviteID,
	).Scan(&inviterID, &creatorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInviteNotFound
	}
	if err != nil {
		return err
	}

	if actingUserID != inviterID && actingUserID != creatorID {
		return ErrNotAllowedToCancelInvite
	}

	_, err = tx.Exec(ctx, `DELETE FROM room_invites WHERE id = $1`, inviteID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *InviteStore) RoomIDForInvite(ctx context.Context, inviteID int64) (int64, error) {
	var roomID int64
	err := s.pool.QueryRow(ctx,
		`SELECT room_id FROM room_invites WHERE id = $1`,
		inviteID,
	).Scan(&roomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInviteNotFound
	}
	return roomID, err
}

func (s *InviteStore) InviteeIDForInvite(ctx context.Context, inviteID int64) (int64, error) {
	var inviteeID int64
	err := s.pool.QueryRow(ctx,
		`SELECT invitee_id FROM room_invites WHERE id = $1`,
		inviteID,
	).Scan(&inviteeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInviteNotFound
	}
	return inviteeID, err
}
