package store

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jchevertonwynne/ssanta/internal/db"
)

type inviteStore struct {
	pool *pgxpool.Pool
}

//nolint:cyclop,funlen
func (s *inviteStore) CreateInvite(ctx context.Context, roomID RoomID, inviterID UserID, inviteeUsername string, expiresAt time.Time) error {
	ctx = db.WithQueryName(ctx, "create_invite")
	inviteeName := strings.TrimSpace(inviteeUsername)
	if inviteeName == "" {
		return ErrInviteeNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Single query to gather all validation data
	var creatorID UserID
	var membersCanInvite bool
	var inviterIsMember bool
	var inviteeID *UserID // nullable if user not found
	var inviteeIsMember bool
	err = tx.QueryRow(ctx,
		`SELECT
			r.creator_id,
			r.members_can_invite,
			EXISTS(SELECT 1 FROM room_users WHERE room_id = $1 AND user_id = $2) AS inviter_is_member,
			u.id,
			EXISTS(SELECT 1 FROM room_users ru2 WHERE ru2.room_id = $1 AND ru2.user_id = u.id) AS invitee_is_member
		 FROM rooms r
		 LEFT JOIN users u ON u.username = $3
		 WHERE r.id = $1`,
		roomID, inviterID, inviteeName,
	).Scan(&creatorID, &membersCanInvite, &inviterIsMember, &inviteeID, &inviteeIsMember)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRoomNotFound
	}
	if err != nil {
		return err
	}

	// Validate inviter permissions
	if inviterID != creatorID {
		if !membersCanInvite || !inviterIsMember {
			return ErrNotAllowedToInvite
		}
	}

	// Validate invitee
	if inviteeID == nil {
		return ErrInviteeNotFound
	}
	if *inviteeID == inviterID {
		return ErrCannotInviteSelf
	}
	if *inviteeID == creatorID || inviteeIsMember {
		return ErrAlreadyMember
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO room_invites (room_id, inviter_id, invitee_id, expires_at) VALUES ($1, $2, $3, $4)`,
		roomID, inviterID, *inviteeID, expiresAt,
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

	slog.InfoContext(ctx, "invite created in db", "room_id", roomID, "inviter_id", inviterID, "invitee_id", *inviteeID)
	return nil
}

func (s *inviteStore) ListInvitesForUser(ctx context.Context, userID UserID) ([]InviteForUser, error) {
	ctx = db.WithQueryName(ctx, "list_invites_for_user")
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

func (s *inviteStore) ListInvitesForRoom(ctx context.Context, roomID RoomID) ([]InviteForRoom, error) {
	ctx = db.WithQueryName(ctx, "list_invites_for_room")
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

func (s *inviteStore) AcceptInvite(ctx context.Context, inviteID InviteID, userID UserID) (RoomID, error) {
	ctx = db.WithQueryName(ctx, "accept_invite")
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var roomID RoomID
	var inviteeID UserID
	var expiresAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT room_id, invitee_id, expires_at FROM room_invites WHERE id = $1 FOR UPDATE`,
		inviteID,
	).Scan(&roomID, &inviteeID, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInviteNotFound
	}
	if err != nil {
		return 0, err
	}
	if inviteeID != userID {
		return 0, ErrInviteNotFound
	}
	if time.Now().After(expiresAt) {
		return 0, ErrInviteExpired
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

func (s *inviteStore) DeclineInvite(ctx context.Context, inviteID InviteID, userID UserID) error {
	ctx = db.WithQueryName(ctx, "decline_invite")
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

func (s *inviteStore) CancelInvite(ctx context.Context, inviteID InviteID, actingUserID UserID) (RoomID, UserID, error) {
	ctx = db.WithQueryName(ctx, "cancel_invite")
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

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

func (s *inviteStore) RoomIDForInvite(ctx context.Context, inviteID InviteID) (RoomID, error) {
	ctx = db.WithQueryName(ctx, "room_id_for_invite")
	var roomID RoomID
	err := s.pool.QueryRow(ctx,
		`SELECT room_id FROM room_invites WHERE id = $1`,
		inviteID,
	).Scan(&roomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInviteNotFound
	}
	return roomID, err
}
