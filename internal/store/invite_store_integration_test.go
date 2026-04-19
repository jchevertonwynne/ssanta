package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestInviteStore_AcceptInvite_Concurrent(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	inviteeID, err := st.Users.CreateUser(ctx, "invitee")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}

	if err := st.Rooms.CreateRoom(ctx, "room", creatorID); err != nil {
		t.Fatalf("create room: %v", err)
	}
	rooms, err := st.Rooms.ListRoomsByCreator(ctx, creatorID)
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	roomID := rooms[0].ID

	if err := st.Invites.CreateInvite(ctx, roomID, creatorID, "invitee"); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	invites, err := st.Invites.ListInvitesForRoom(ctx, roomID)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("expected 1 invite, got %d", len(invites))
	}
	inviteID := invites[0].InviteID

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			<-start
			errCh <- st.Invites.AcceptInvite(ctx, inviteID, inviteeID)
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)

	var success, notFound int
	for err := range errCh {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrInviteNotFound):
			notFound++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if success != 1 || notFound != 1 {
		t.Fatalf("expected 1 success and 1 ErrInviteNotFound, got success=%d notFound=%d", success, notFound)
	}

	isMember, err := st.Rooms.IsRoomMember(ctx, roomID, inviteeID)
	if err != nil {
		t.Fatalf("check membership: %v", err)
	}
	if !isMember {
		t.Fatalf("expected invitee to become a member")
	}

	if _, err := st.Invites.RoomIDForInvite(ctx, inviteID); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("expected invite to be deleted (ErrInviteNotFound), got %v", err)
	}
}
