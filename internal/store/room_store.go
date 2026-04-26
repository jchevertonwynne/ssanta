package store

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RoomStore struct {
	pool *pgxpool.Pool
}

// GetOrCreateDMRoom atomically finds or creates a DM room with the given
// display name. Using INSERT ... ON CONFLICT DO NOTHING + fallback SELECT
// avoids the check-then-create race between two participants starting a DM
// at the same time.
func (s *RoomStore) GetOrCreateDMRoom(ctx context.Context, displayName string, creatorID UserID) (RoomID, error) {
	var roomID RoomID
	err := s.pool.QueryRow(ctx,
		`INSERT INTO rooms (display_name, creator_id, is_dm)
		 VALUES ($1, $2, TRUE)
		 ON CONFLICT (display_name) DO NOTHING
		 RETURNING id`,
		displayName, creatorID,
	).Scan(&roomID)
	if err == nil {
		return roomID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	// Row already existed; resolve it.
	err = s.pool.QueryRow(ctx,
		`SELECT id FROM rooms WHERE display_name = $1`,
		displayName,
	).Scan(&roomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRoomNotFound
	}
	return roomID, err
}

func (s *RoomStore) CreateRoom(ctx context.Context, displayName string, creatorID UserID, isDM bool) (RoomID, error) {
	var roomID RoomID
	err := s.pool.QueryRow(ctx,
		`INSERT INTO rooms (display_name, creator_id, is_dm) VALUES ($1, $2, $3) RETURNING id`,
		displayName, creatorID, isDM,
	).Scan(&roomID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, ErrRoomNameTaken
	}
	if err == nil {
		slog.InfoContext(ctx, "room created in db", "room_name", displayName, "creator_id", creatorID, "room_id", roomID)
	}
	return roomID, err
}

func (s *RoomStore) DeleteRoom(ctx context.Context, roomID RoomID, creatorID UserID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM rooms WHERE id = $1 AND creator_id = $2`,
		roomID, creatorID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRoomNotFound
	}
	slog.InfoContext(ctx, "room deleted from db", "room_id", roomID, "creator_id", creatorID)
	return nil
}

func (s *RoomStore) ListRoomsByCreator(ctx context.Context, userID UserID) ([]Room, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, display_name, created_at, pgp_required, is_dm, is_public FROM rooms WHERE creator_id = $1 AND is_dm = FALSE ORDER BY display_name ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	return scanRooms(rows)
}

func (s *RoomStore) ListRoomsByMember(ctx context.Context, userID UserID) ([]Room, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT r.id, r.display_name, r.created_at, r.pgp_required, r.is_dm, r.is_public
		 FROM rooms r
		 JOIN room_users ru ON ru.room_id = r.id
		 WHERE ru.user_id = $1 AND r.is_dm = FALSE
		 ORDER BY r.display_name ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	return scanRooms(rows)
}

func (s *RoomStore) ListDMRoomsByMember(ctx context.Context, userID UserID) ([]Room, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT r.id, r.display_name, r.created_at, r.pgp_required, r.is_dm, r.is_public
		 FROM rooms r
		 JOIN room_users ru ON ru.room_id = r.id
		 WHERE ru.user_id = $1 AND r.is_dm = TRUE`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	return scanRooms(rows)
}

func (s *RoomStore) GetRoomDetail(ctx context.Context, roomID RoomID) (RoomDetail, error) {
	var d RoomDetail
	err := s.pool.QueryRow(ctx,
		`SELECT r.id, r.display_name, r.created_at, r.creator_id, r.members_can_invite, r.pgp_required, r.is_dm, r.is_public, u.username
		 FROM rooms r
		 JOIN users u ON u.id = r.creator_id
		 WHERE r.id = $1`,
		roomID,
	).Scan(&d.ID, &d.DisplayName, &d.CreatedAt, &d.CreatorID, &d.MembersCanInvite, &d.PGPRequired, &d.IsDM, &d.IsPublic, &d.CreatorUsername)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoomDetail{}, ErrRoomNotFound
	}
	return d, err
}

func (s *RoomStore) IsRoomMember(ctx context.Context, roomID RoomID, userID UserID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM room_users WHERE room_id = $1 AND user_id = $2)`,
		roomID, userID,
	).Scan(&exists)
	return exists, err
}

func (s *RoomStore) IsRoomPGPRequired(ctx context.Context, roomID RoomID) (bool, error) {
	var required bool
	err := s.pool.QueryRow(ctx,
		`SELECT pgp_required FROM rooms WHERE id = $1`,
		roomID,
	).Scan(&required)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrRoomNotFound
	}
	return required, err
}

func (s *RoomStore) IsRoomPublic(ctx context.Context, roomID RoomID) (bool, error) {
	var public bool
	err := s.pool.QueryRow(ctx,
		`SELECT is_public FROM rooms WHERE id = $1`,
		roomID,
	).Scan(&public)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrRoomNotFound
	}
	return public, err
}

func (s *RoomStore) ListPublicRooms(ctx context.Context, userID UserID) ([]Room, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, display_name, created_at, pgp_required, is_dm, is_public
		 FROM rooms
		 WHERE is_public = TRUE
		   AND is_dm = FALSE
		   AND creator_id != $1
		   AND NOT EXISTS (
		       SELECT 1 FROM room_users WHERE room_id = rooms.id AND user_id = $1
		   )
		 ORDER BY display_name ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	return scanRooms(rows)
}

func (s *RoomStore) IsRoomCreator(ctx context.Context, roomID RoomID, userID UserID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM rooms WHERE id = $1 AND creator_id = $2)`,
		roomID, userID,
	).Scan(&exists)
	return exists, err
}

func (s *RoomStore) GetRoomAccess(ctx context.Context, roomID RoomID, userID UserID) (bool, bool, error) {
	var isCreator, isMember bool
	err := s.pool.QueryRow(ctx,
		`SELECT
			EXISTS(SELECT 1 FROM rooms WHERE id = $1 AND creator_id = $2),
			EXISTS(SELECT 1 FROM room_users WHERE room_id = $1 AND user_id = $2)`,
		roomID, userID,
	).Scan(&isCreator, &isMember)
	return isCreator, isMember, err
}

func (s *RoomStore) JoinRoom(ctx context.Context, roomID RoomID, userID UserID) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO room_users (room_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		roomID, userID,
	)
	return err
}

func (s *RoomStore) ListRoomMembersWithPGP(ctx context.Context, roomID RoomID) ([]RoomMember, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.id,
		        u.username,
		        u.created_at,
		        ru.pgp_public_key,
		        ru.pgp_fingerprint,
		        ru.pgp_verified_at,
		        ru.pgp_challenge_ciphertext,
		        ru.pgp_challenge_expires_at
		 FROM users u
		 JOIN room_users ru ON ru.user_id = u.id
		 WHERE ru.room_id = $1
		 ORDER BY u.username ASC`,
		roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RoomMember
	for rows.Next() {
		var m RoomMember
		var pubKey pgtype.Text
		var fingerprint pgtype.Text
		var verifiedAt *time.Time
		var challengeCipher pgtype.Text
		var challengeExp *time.Time
		if err := rows.Scan(&m.ID, &m.Username, &m.CreatedAt, &pubKey, &fingerprint, &verifiedAt, &challengeCipher, &challengeExp); err != nil {
			return nil, err
		}
		if pubKey.Valid {
			m.PGPPublicKey = pubKey.String
		}
		if fingerprint.Valid {
			m.PGPFingerprint = fingerprint.String
		}
		m.PGPVerifiedAt = verifiedAt
		if challengeCipher.Valid {
			m.PGPChallengeCiphertext = challengeCipher.String
		}
		m.PGPChallengeExpiresAt = challengeExp
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *RoomStore) SetRoomMembersCanInvite(ctx context.Context, roomID RoomID, creatorID UserID, value bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE rooms SET members_can_invite = $3 WHERE id = $1 AND creator_id = $2`,
		roomID, creatorID, value,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRoomCreator
	}
	return nil
}

func (s *RoomStore) SetRoomPGPRequired(ctx context.Context, roomID RoomID, creatorID UserID, value bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE rooms SET pgp_required = $3 WHERE id = $1 AND creator_id = $2`,
		roomID, creatorID, value,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRoomCreator
	}
	return nil
}

func (s *RoomStore) SetRoomPublic(ctx context.Context, roomID RoomID, creatorID UserID, value bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE rooms SET is_public = $3 WHERE id = $1 AND creator_id = $2`,
		roomID, creatorID, value,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRoomCreator
	}
	return nil
}

//nolint:cyclop,nestif,funlen
func (s *RoomStore) LeaveRoom(ctx context.Context, roomID RoomID, userID UserID) error {
	// Start transaction to ensure atomicity
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Check if room exists and if user is the creator
	var creatorID UserID
	var isDM bool
	err = tx.QueryRow(ctx,
		`SELECT creator_id, is_dm FROM rooms WHERE id = $1`,
		roomID,
	).Scan(&creatorID, &isDM)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		}
		return err
	}

	// Remove user from room_users
	tag, err := tx.Exec(ctx,
		`DELETE FROM room_users WHERE room_id = $1 AND user_id = $2`,
		roomID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRoomMember
	}

	// Only delete invites if the user is NOT the creator
	// Creators can maintain their invites even when not members
	if userID != creatorID {
		_, err = tx.Exec(ctx,
			`DELETE FROM room_invites WHERE room_id = $1 AND inviter_id = $2`,
			roomID, userID,
		)
		if err != nil {
			return err
		}
	}

	// For DM rooms, check if there are any remaining members
	// If no members left, delete the room entirely
	if isDM {
		var memberCount int
		err = tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM room_users WHERE room_id = $1`,
			roomID,
		).Scan(&memberCount)
		if err != nil {
			return err
		}

		if memberCount == 0 {
			// Delete all related data for this DM room
			// Delete room invites
			_, err = tx.Exec(ctx,
				`DELETE FROM room_invites WHERE room_id = $1`,
				roomID,
			)
			if err != nil {
				return err
			}

			// Delete room members (should be empty already, but be explicit)
			_, err = tx.Exec(ctx,
				`DELETE FROM room_users WHERE room_id = $1`,
				roomID,
			)
			if err != nil {
				return err
			}

			// Delete the room itself
			_, err = tx.Exec(ctx,
				`DELETE FROM rooms WHERE id = $1`,
				roomID,
			)
			if err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

func (s *RoomStore) RemoveMember(ctx context.Context, roomID RoomID, memberID, creatorID UserID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Verify the actor is the creator
	var actualCreatorID UserID
	err = tx.QueryRow(ctx,
		`SELECT creator_id FROM rooms WHERE id = $1`,
		roomID,
	).Scan(&actualCreatorID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		}
		return err
	}

	if actualCreatorID != creatorID {
		return ErrNotRoomCreator
	}

	// Cannot remove the creator
	if memberID == actualCreatorID {
		return ErrCannotRemoveCreator
	}

	// Remove user from room_users
	tag, err := tx.Exec(ctx,
		`DELETE FROM room_users WHERE room_id = $1 AND user_id = $2`,
		roomID, memberID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRoomMember
	}

	// Delete invites created by this user
	_, err = tx.Exec(ctx,
		`DELETE FROM room_invites WHERE room_id = $1 AND inviter_id = $2`,
		roomID, memberID,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *RoomStore) ListAllRooms(ctx context.Context) ([]RoomDetail, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT r.id, r.display_name, r.created_at, r.creator_id, r.members_can_invite, r.pgp_required, r.is_dm, r.is_public, u.username
		 FROM rooms r
		 JOIN users u ON u.id = r.creator_id
		 WHERE r.is_dm = FALSE
		 ORDER BY r.display_name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []RoomDetail
	for rows.Next() {
		var d RoomDetail
		if err := rows.Scan(&d.ID, &d.DisplayName, &d.CreatedAt, &d.CreatorID, &d.MembersCanInvite, &d.PGPRequired, &d.IsDM, &d.IsPublic, &d.CreatorUsername); err != nil {
			return nil, err
		}
		rooms = append(rooms, d)
	}
	return rooms, rows.Err()
}

func (s *RoomStore) AdminDeleteRoom(ctx context.Context, roomID RoomID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM rooms WHERE id = $1`, roomID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRoomNotFound
	}
	slog.InfoContext(ctx, "room admin-deleted from db", "room_id", roomID)
	return nil
}

func scanRooms(rows pgx.Rows) ([]Room, error) {
	defer rows.Close()
	var rooms []Room
	for rows.Next() {
		var r Room
		if err := rows.Scan(&r.ID, &r.DisplayName, &r.CreatedAt, &r.PGPRequired, &r.IsDM, &r.IsPublic); err != nil {
			return nil, err
		}
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}
