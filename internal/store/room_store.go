package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RoomStore struct {
	pool *pgxpool.Pool
}

func (s *RoomStore) CreateRoom(ctx context.Context, displayName string, creatorID int64) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO rooms (display_name, creator_id) VALUES ($1, $2)`,
		displayName, creatorID,
	)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrRoomNameTaken
	}
	return err
}

func (s *RoomStore) DeleteRoom(ctx context.Context, roomID, creatorID int64) error {
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
	return nil
}

func (s *RoomStore) ListRoomsByCreator(ctx context.Context, userID int64) ([]Room, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, display_name, created_at FROM rooms WHERE creator_id = $1 ORDER BY id DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	return scanRooms(rows)
}

func (s *RoomStore) ListRoomsByMember(ctx context.Context, userID int64) ([]Room, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT r.id, r.display_name, r.created_at
		 FROM rooms r
		 JOIN room_users ru ON ru.room_id = r.id
		 WHERE ru.user_id = $1
		 ORDER BY r.id DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	return scanRooms(rows)
}

func (s *RoomStore) GetRoomDetail(ctx context.Context, roomID int64) (RoomDetail, error) {
	var d RoomDetail
	err := s.pool.QueryRow(ctx,
		`SELECT r.id, r.display_name, r.created_at, r.creator_id, r.members_can_invite, u.username
		 FROM rooms r
		 JOIN users u ON u.id = r.creator_id
		 WHERE r.id = $1`,
		roomID,
	).Scan(&d.ID, &d.DisplayName, &d.CreatedAt, &d.CreatorID, &d.MembersCanInvite, &d.CreatorUsername)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoomDetail{}, ErrRoomNotFound
	}
	return d, err
}

func (s *RoomStore) IsRoomMember(ctx context.Context, roomID, userID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM room_users WHERE room_id = $1 AND user_id = $2)`,
		roomID, userID,
	).Scan(&exists)
	return exists, err
}

func (s *RoomStore) IsRoomCreator(ctx context.Context, roomID, userID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM rooms WHERE id = $1 AND creator_id = $2)`,
		roomID, userID,
	).Scan(&exists)
	return exists, err
}

func (s *RoomStore) JoinRoom(ctx context.Context, roomID, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO room_users (room_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		roomID, userID,
	)
	return err
}

func (s *RoomStore) ListRoomMembers(ctx context.Context, roomID int64) ([]User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, u.username, u.created_at
		 FROM users u
		 JOIN room_users ru ON ru.user_id = u.id
		 WHERE ru.room_id = $1
		 ORDER BY ru.joined_at ASC`,
		roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *RoomStore) SetRoomMembersCanInvite(ctx context.Context, roomID, creatorID int64, value bool) error {
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

func (s *RoomStore) LeaveRoom(ctx context.Context, roomID, userID int64) error {
	// Start transaction to ensure atomicity
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Check if room exists and if user is the creator
	var creatorID int64
	err = tx.QueryRow(ctx,
		`SELECT creator_id FROM rooms WHERE id = $1`,
		roomID,
	).Scan(&creatorID)
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

	return tx.Commit(ctx)
}

func (s *RoomStore) RemoveMember(ctx context.Context, roomID, memberID, creatorID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Verify the actor is the creator
	var actualCreatorID int64
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

func scanRooms(rows pgx.Rows) ([]Room, error) {
	defer rows.Close()
	var rooms []Room
	for rows.Next() {
		var r Room
		if err := rows.Scan(&r.ID, &r.DisplayName, &r.CreatedAt); err != nil {
			return nil, err
		}
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}
