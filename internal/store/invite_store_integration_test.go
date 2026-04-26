package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

//nolint:funlen
func TestInviteStore_AcceptInvite_Concurrent(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	inviteeID, err := st.Users.CreateUser(ctx, "invitee", "testhash")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Invites.CreateInvite(ctx, roomID, creatorID, "invitee", time.Now().Add(24*time.Hour)); err != nil {
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
	for range 2 {
		wg.Go(func() {
			<-start
			_, err := st.Invites.AcceptInvite(ctx, inviteID, inviteeID)
			errCh <- err
		})
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

func TestInviteStore_CreateInvite_PermissionDeniedWhenMembersCannotInvite(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	memberID, err := st.Users.CreateUser(ctx, "member", "testhash")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	inviteeID, err := st.Users.CreateUser(ctx, "invitee", "testhash")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}
	_ = inviteeID

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Rooms.JoinRoom(ctx, roomID, memberID); err != nil {
		t.Fatalf("join room: %v", err)
	}

	if err := st.Invites.CreateInvite(ctx, roomID, memberID, "invitee", time.Now().Add(24*time.Hour)); !errors.Is(err, ErrNotAllowedToInvite) {
		t.Fatalf("expected ErrNotAllowedToInvite, got %v", err)
	}
}

func TestInviteStore_CreateInvite_MemberAllowedWhenMembersCanInviteEnabled(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	memberID, err := st.Users.CreateUser(ctx, "member", "testhash")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	inviteeID, err := st.Users.CreateUser(ctx, "invitee", "testhash")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}
	_ = inviteeID

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Rooms.JoinRoom(ctx, roomID, memberID); err != nil {
		t.Fatalf("join room: %v", err)
	}
	if err := st.Rooms.SetRoomMembersCanInvite(ctx, roomID, creatorID, true); err != nil {
		t.Fatalf("enable members_can_invite: %v", err)
	}

	if err := st.Invites.CreateInvite(ctx, roomID, memberID, "invitee", time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("expected invite creation success, got %v", err)
	}
}

func TestInviteStore_CreateInvite_DuplicateInviteRejected(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	inviteeID, err := st.Users.CreateUser(ctx, "invitee", "testhash")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}
	_ = inviteeID

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Invites.CreateInvite(ctx, roomID, creatorID, "invitee", time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := st.Invites.CreateInvite(ctx, roomID, creatorID, "invitee", time.Now().Add(24*time.Hour)); !errors.Is(err, ErrAlreadyInvited) {
		t.Fatalf("expected ErrAlreadyInvited, got %v", err)
	}
}

func TestInviteStore_AcceptInvite_WrongUserGetsNotFound(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	inviteeID, err := st.Users.CreateUser(ctx, "invitee", "testhash")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}
	_ = inviteeID
	otherID, err := st.Users.CreateUser(ctx, "other", "testhash")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Invites.CreateInvite(ctx, roomID, creatorID, "invitee", time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	invites, err := st.Invites.ListInvitesForRoom(ctx, roomID)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	inviteID := invites[0].InviteID

	if _, err := st.Invites.AcceptInvite(ctx, inviteID, otherID); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("expected ErrInviteNotFound, got %v", err)
	}

	// Invite still exists for the real invitee.
	if _, err := st.Invites.RoomIDForInvite(ctx, inviteID); err != nil {
		t.Fatalf("expected invite to still exist, got %v", err)
	}
}

func TestInviteStore_AcceptInvite_Expired(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	inviteeID, err := st.Users.CreateUser(ctx, "invitee", "testhash")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	// Create an invite that expired 1 second ago
	if err := st.Invites.CreateInvite(ctx, roomID, creatorID, "invitee", time.Now().Add(-1*time.Second)); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	invites, err := st.Invites.ListInvitesForRoom(ctx, roomID)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	inviteID := invites[0].InviteID

	if _, err := st.Invites.AcceptInvite(ctx, inviteID, inviteeID); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("expected ErrInviteExpired, got %v", err)
	}

	// Invite still exists (not auto-deleted)
	if _, err := st.Invites.RoomIDForInvite(ctx, inviteID); err != nil {
		t.Fatalf("expected invite to still exist, got %v", err)
	}
}
