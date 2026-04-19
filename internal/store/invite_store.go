package store

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type InviteStore struct {
	db dbtx
}

func (s *InviteStore) CreateInvite(ctx context.Context, roomID RoomID, inviterID UserID, inviteeUsername string, expiresAt time.Time) error {
	inviteeName := strings.TrimSpace(inviteeUsername)
	if inviteeName == "" {
		return ErrInviteeNotFound
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var creatorID UserID
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

	var inviteeID UserID
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
		`INSERT INTO room_invites (room_id, inviter_id, invitee_id, expires_at) VALUES ($1, $2, $3, $4)`,
		roomID, inviterID, inviteeID, expiresAt,
	)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrAlreadyInvited
	}
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	slog.InfoContext(ctx, "invite created in db", "room_id", roomID, "inviter_id", inviterID, "invitee_id", inviteeID)
	return nil
}

func (s *InviteStore) ListInvitesForUser(ctx context.Context, userID UserID) ([]InviteForUser, error) {
	rows, err := s.db.Query(ctx,
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

func (s *InviteStore) ListInvitesForRoom(ctx context.Context, roomID RoomID) ([]InviteForRoom, error) {
	rows, err := s.db.Query(ctx,
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

func (s *InviteStore) AcceptInvite(ctx context.Context, inviteID InviteID, userID UserID) (RoomID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var roomID RoomID
	var inviteeID UserID
	err = tx.QueryRow(ctx,
		`SELECT room_id, invitee_id FROM room_invites WHERE id = $1 FOR UPDATE`,
		inviteID,
	).Scan(&roomID, &inviteeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInviteNotFound
	}
	if err != nil {
		return 0, err
	}
	if inviteeID != userID {
		return 0, ErrInviteNotFound
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO room_users (room_id, user_id) VALUES ($1, $2)
		 ON CONFLICT (room_id, user_id) DO NOTHING`,
		roomID, userID,
	)
	if err != nil {
		return 0, err
	}

	_, err = tx.Exec(ctx, `DELETE FROM room_invites WHERE id = $1`, inviteID)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	slog.InfoContext(ctx, "invite accepted in db", "invite_id", inviteID, "room_id", roomID, "user_id", userID)
	return roomID, nil
}

func (s *InviteStore) DeclineInvite(ctx context.Context, inviteID InviteID, userID UserID) error {
	tag, err := s.db.Exec(ctx,
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

func (s *InviteStore) CancelInvite(ctx context.Context, inviteID InviteID, actingUserID UserID) (RoomID, UserID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)

	var inviterID, creatorID, inviteeID UserID
	var roomID RoomID
	err = tx.QueryRow(ctx,
		`SELECT i.inviter_id, r.creator_id, i.room_id, i.invitee_id
		 FROM room_invites i
		 JOIN rooms r ON r.id = i.room_id
		 WHERE i.id = $1
		 FOR UPDATE OF i`,
		inviteID,
	).Scan(&inviterID, &creatorID, &roomID, &inviteeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrInviteNotFound
	}
	if err != nil {
		return 0, 0, err
	}

	if actingUserID != inviterID && actingUserID != creatorID {
		return 0, 0, ErrNotAllowedToCancelInvite
	}

	_, err = tx.Exec(ctx, `DELETE FROM room_invites WHERE id = $1`, inviteID)
	if err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return roomID, inviteeID, nil
}

func (s *InviteStore) RoomIDForInvite(ctx context.Context, inviteID InviteID) (RoomID, error) {
	var roomID RoomID
	err := s.db.QueryRow(ctx,
		`SELECT room_id FROM room_invites WHERE id = $1`,
		inviteID,
	).Scan(&roomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInviteNotFound
	}
	return roomID, err
}
