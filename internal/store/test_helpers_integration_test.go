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

func createUser(t *testing.T, pool *pgxpool.Pool, username string) int64 {
	t.Helper()
	st := New(pool)
	ctx, cancel := testCtx(t)
	defer cancel()
	id, err := st.Users.CreateUser(ctx, username)
	if err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return id
}

func createRoom(t *testing.T, pool *pgxpool.Pool, name string, creatorID int64) int64 {
	t.Helper()
	st := New(pool)
	ctx, cancel := testCtx(t)
	defer cancel()
	if err := st.Rooms.CreateRoom(ctx, name, creatorID); err != nil {
		t.Fatalf("create room %q: %v", name, err)
	}
	rooms, err := st.Rooms.ListRoomsByCreator(ctx, creatorID)
	if err != nil {
		t.Fatalf("list rooms by creator: %v", err)
	}
	if len(rooms) == 0 {
		t.Fatalf("expected created room to appear in list")
	}
	return rooms[0].ID
}

func createInvite(t *testing.T, pool *pgxpool.Pool, roomID, inviterID int64, inviteeUsername string) int64 {
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
