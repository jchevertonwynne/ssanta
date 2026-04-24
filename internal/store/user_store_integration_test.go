package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUserStore_CreateGetListDelete(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	id, err := st.Users.CreateUser(ctx, "alice", "testhash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	u, err := st.Users.GetUserByID(ctx, id)
	if err != nil {
		t.Fatalf("get user by id %d: %v", id, err)
	}
	if u.Username != "alice" {
		t.Fatalf("expected username alice, got %q", u.Username)
	}

	u2, err := st.Users.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("get user by username: %v", err)
	}
	if u2.ID != id {
		t.Fatalf("expected same id, got %d", u2.ID)
	}

	users, err := st.Users.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].ID != id {
		t.Fatalf("expected most recent user first")
	}

	if err := st.Users.DeleteUser(ctx, id); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if _, err := st.Users.GetUserByID(ctx, id); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound after delete, got %v", err)
	}
}

func TestUserStore_CreateUser_DuplicateUsername(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := testCtx(t)
	defer cancel()

	if _, err := st.Users.CreateUser(ctx, "alice", "testhash"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := st.Users.CreateUser(ctx, "alice", "testhash"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("expected ErrUsernameTaken, got %v", err)
	}
}

func TestUserStore_UserExists(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := testCtx(t)
	defer cancel()

	exists, err := st.Users.UserExists(ctx, 9999)
	if err != nil {
		t.Fatalf("user exists: %v", err)
	}
	if exists {
		t.Fatalf("expected exists=false")
	}

	id, err := st.Users.CreateUser(ctx, "alice", "testhash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	exists, err = st.Users.UserExists(ctx, id)
	if err != nil {
		t.Fatalf("user exists (created): %v", err)
	}
	if !exists {
		t.Fatalf("expected exists=true")
	}
}

func TestUserStore_DeleteUser_NotFound(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := testCtx(t)
	defer cancel()

	if err := st.Users.DeleteUser(ctx, 12345); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUserStore_GetUser_NotFound(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := testCtx(t)
	defer cancel()

	if _, err := st.Users.GetUserByID(ctx, 12345); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
	if _, err := st.Users.GetUserByUsername(ctx, "missing"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}
