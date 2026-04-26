package store

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jchevertonwynne/ssanta/internal/db"
)

type UserStore struct {
	pool *pgxpool.Pool
}

func (s *UserStore) UserExists(ctx context.Context, id UserID) (bool, error) {
	ctx = db.WithQueryName(ctx, "user_exists")
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, id).Scan(&exists)
	return exists, err
}

func (s *UserStore) GetUserByID(ctx context.Context, id UserID) (User, error) {
	ctx = db.WithQueryName(ctx, "get_user_by_id")
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, created_at FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

func (s *UserStore) GetUserByUsername(ctx context.Context, username string) (User, error) {
	ctx = db.WithQueryName(ctx, "get_user_by_username")
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, created_at FROM users WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

func (s *UserStore) GetUserWithPassword(ctx context.Context, username string) (UserWithPassword, error) {
	ctx = db.WithQueryName(ctx, "get_user_with_password")
	var u UserWithPassword
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, created_at, password_hash FROM users WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.Username, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserWithPassword{}, ErrUserNotFound
	}
	return u, err
}

func (s *UserStore) CreateUser(ctx context.Context, username, passwordHash string) (UserID, error) {
	ctx = db.WithQueryName(ctx, "create_user")
	var id UserID
	err := s.pool.QueryRow(ctx,
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
	ctx = db.WithQueryName(ctx, "delete_user")
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	slog.InfoContext(ctx, "user deleted from db", "user_id", id)
	return nil
}

func (s *UserStore) GetUserWithPasswordByID(ctx context.Context, id UserID) (UserWithPassword, error) {
	ctx = db.WithQueryName(ctx, "get_user_with_password_by_id")
	var u UserWithPassword
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, created_at, password_hash FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.Username, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserWithPassword{}, ErrUserNotFound
	}
	return u, err
}

func (s *UserStore) UpdatePasswordHash(ctx context.Context, id UserID, passwordHash string) error {
	ctx = db.WithQueryName(ctx, "update_password_hash")
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, passwordHash, id)
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
	ctx = db.WithQueryName(ctx, "get_user_session_version")
	var v int
	err := s.pool.QueryRow(ctx, `SELECT session_version FROM users WHERE id = $1`, id).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUserNotFound
	}
	return v, err
}

// BumpSessionVersion increments the user's session_version so all previously
// issued session cookies stop validating. Called by password change flows.
func (s *UserStore) BumpSessionVersion(ctx context.Context, id UserID) error {
	ctx = db.WithQueryName(ctx, "bump_session_version")
	tag, err := s.pool.Exec(ctx,
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
	ctx = db.WithQueryName(ctx, "list_users")
	rows, err := s.pool.Query(ctx, `SELECT id, username, created_at FROM users ORDER BY username ASC`)
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

func (s *UserStore) ListAllUsers(ctx context.Context) ([]AdminUser, error) {
	ctx = db.WithQueryName(ctx, "list_all_users")
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, u.username, u.created_at,
		        a.user_id IS NOT NULL,
		        a.admin_since,
		        g.username
		 FROM users u
		 LEFT JOIN admins a ON a.user_id = u.id
		 LEFT JOIN users g ON g.id = a.granted_by
		 ORDER BY u.username ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []AdminUser
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Username, &u.CreatedAt, &u.IsAdmin, &u.AdminSince, &u.AdminGrantedByUsername); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *UserStore) IsUserAdmin(ctx context.Context, id UserID) (bool, error) {
	ctx = db.WithQueryName(ctx, "is_user_admin")
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins WHERE user_id = $1)`, id).Scan(&exists)
	return exists, err
}

func (s *UserStore) GrantAdmin(ctx context.Context, targetID, grantedBy UserID) error {
	ctx = db.WithQueryName(ctx, "grant_admin")
	_, err := s.pool.Exec(ctx,
		`INSERT INTO admins (user_id, granted_by) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		targetID, grantedBy,
	)
	return err
}

func (s *UserStore) RevokeAdmin(ctx context.Context, targetID UserID) error {
	ctx = db.WithQueryName(ctx, "revoke_admin")
	_, err := s.pool.Exec(ctx, `DELETE FROM admins WHERE user_id = $1`, targetID)
	return err
}
