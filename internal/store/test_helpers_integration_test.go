package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 10*time.Second)
}

func createUser(t *testing.T, pool *pgxpool.Pool, username string) UserID {
	t.Helper()
	st := New(pool)
	ctx, cancel := testCtx(t)
	defer cancel()
	id, err := st.Users.CreateUser(ctx, username, "testhash")
	if err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return id
}

func createRoom(t *testing.T, pool *pgxpool.Pool, name string, creatorID UserID) RoomID {
	t.Helper()
	st := New(pool)
	ctx, cancel := testCtx(t)
	defer cancel()
	roomID, err := st.Rooms.CreateRoom(ctx, name, creatorID, false)
	if err != nil {
		t.Fatalf("create room %q: %v", name, err)
	}
	return roomID
}

func createInvite(t *testing.T, pool *pgxpool.Pool, roomID RoomID, inviterID UserID, inviteeUsername string) InviteID {
	t.Helper()
	st := New(pool)
	ctx, cancel := testCtx(t)
	defer cancel()
	if err := st.Invites.CreateInvite(ctx, roomID, inviterID, inviteeUsername, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("create invite to %q: %v", inviteeUsername, err)
	}
	invites, err := st.Invites.ListInvitesForRoom(ctx, roomID)
	if err != nil {
		t.Fatalf("list invites for room: %v", err)
	}
	if len(invites) == 0 {
		t.Fatalf("expected invite to exist")
	}
	return invites[0].InviteID
}
