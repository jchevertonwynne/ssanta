package store

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type UserStore struct {
	db dbtx
}

func (s *UserStore) UserExists(ctx context.Context, id UserID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, id).Scan(&exists)
	return exists, err
}

func (s *UserStore) GetUserByID(ctx context.Context, id UserID) (User, error) {
	var u User
	err := s.db.QueryRow(ctx,
		`SELECT id, username, created_at FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

func (s *UserStore) GetUserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	err := s.db.QueryRow(ctx,
		`SELECT id, username, created_at FROM users WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

func (s *UserStore) GetUserWithPassword(ctx context.Context, username string) (User, error) {
	var u User
	err := s.db.QueryRow(ctx,
		`SELECT id, username, created_at, password_hash FROM users WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.Username, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

func (s *UserStore) CreateUser(ctx context.Context, username, passwordHash string) (UserID, error) {
	var id UserID
	err := s.db.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id`,
		username, passwordHash,
	).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, ErrUsernameTaken
	}
	if err == nil {
		slog.InfoContext(ctx, "user created in db", "user_id", id, "username", username)
	}
	return id, err
}

func (s *UserStore) DeleteUser(ctx context.Context, id UserID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	slog.InfoContext(ctx, "user deleted from db", "user_id", id)
	return nil
}

func (s *UserStore) GetUserWithPasswordByID(ctx context.Context, id UserID) (User, error) {
	var u User
	err := s.db.QueryRow(ctx,
		`SELECT id, username, created_at, password_hash FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.Username, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

func (s *UserStore) UpdatePasswordHash(ctx context.Context, id UserID, passwordHash string) error {
	tag, err := s.db.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, passwordHash, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	slog.InfoContext(ctx, "password updated in db", "user_id", id)
	return nil
}

// GetUserSessionVersion returns the current session_version for a user.
// ErrUserNotFound is returned when the row is missing. Sessions whose cookie
// carries a mismatched version should be treated as invalid.
func (s *UserStore) GetUserSessionVersion(ctx context.Context, id UserID) (int, error) {
	var v int
	err := s.db.QueryRow(ctx, `SELECT session_version FROM users WHERE id = $1`, id).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUserNotFound
	}
	return v, err
}

// BumpSessionVersion increments the user's session_version so all previously
// issued session cookies stop validating. Called by password change flows.
func (s *UserStore) BumpSessionVersion(ctx context.Context, id UserID) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE users SET session_version = session_version + 1 WHERE id = $1`,
		id,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *UserStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, `SELECT id, username, created_at FROM users ORDER BY username ASC`)
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
