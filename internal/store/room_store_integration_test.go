package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRoomStore_LeaveRoom_DeletesInvitesForNonCreator(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID)
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
		t.Fatalf("create invite: %v", err)
	}
	invites, err := st.Invites.ListInvitesForRoom(ctx, roomID)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("expected 1 invite, got %d", len(invites))
	}
	_ = inviteeID // only used to create the invite by username

	if err := st.Rooms.LeaveRoom(ctx, roomID, memberID); err != nil {
		t.Fatalf("leave room: %v", err)
	}

	invites, err = st.Invites.ListInvitesForRoom(ctx, roomID)
	if err != nil {
		t.Fatalf("list invites (post-leave): %v", err)
	}
	if len(invites) != 0 {
		t.Fatalf("expected invites to be deleted on leave, got %d", len(invites))
	}
}

func TestRoomStore_LeaveRoom_CreatorDoesNotDeleteOwnInvites(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	// Creator must first join to be a member, then create an invite.
	if err := st.Rooms.JoinRoom(ctx, roomID, creatorID); err != nil {
		t.Fatalf("join room: %v", err)
	}
	if err := st.Invites.CreateInvite(ctx, roomID, creatorID, "invitee", time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// Leave as creator should not delete creator-created invites.
	if err := st.Rooms.LeaveRoom(ctx, roomID, creatorID); err != nil {
		t.Fatalf("leave room: %v", err)
	}

	invites, err := st.Invites.ListInvitesForRoom(ctx, roomID)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("expected invite to remain, got %d", len(invites))
	}
}

func TestRoomStore_JoinRoom_Idempotent(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	memberID, err := st.Users.CreateUser(ctx, "member", "testhash")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Rooms.JoinRoom(ctx, roomID, memberID); err != nil {
		t.Fatalf("join room: %v", err)
	}
	if err := st.Rooms.JoinRoom(ctx, roomID, memberID); err != nil {
		t.Fatalf("join room (second time): %v", err)
	}

	members, err := st.Rooms.ListRoomMembersWithPGP(ctx, roomID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	var count int
	for _, m := range members {
		if m.ID == memberID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected member to appear once, got %d", count)
	}
}

func TestRoomStore_SetMembersCanInvite_NonCreatorForbidden(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	otherID, err := st.Users.CreateUser(ctx, "other", "testhash")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Rooms.SetRoomMembersCanInvite(ctx, roomID, otherID, true); !errors.Is(err, ErrNotRoomCreator) {
		t.Fatalf("expected ErrNotRoomCreator, got %v", err)
	}
}

func TestRoomStore_SetPGPRequired_Success(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	// Initially pgp_required should be false
	rd, err := st.Rooms.GetRoomDetail(ctx, roomID)
	if err != nil {
		t.Fatalf("get room detail: %v", err)
	}
	if rd.PGPRequired {
		t.Fatalf("expected PGPRequired to be false initially, got true")
	}

	// Set pgp_required to true
	if err := st.Rooms.SetRoomPGPRequired(ctx, roomID, creatorID, true); err != nil {
		t.Fatalf("set pgp_required: %v", err)
	}

	// Verify it was updated
	rd, err = st.Rooms.GetRoomDetail(ctx, roomID)
	if err != nil {
		t.Fatalf("get room detail: %v", err)
	}
	if !rd.PGPRequired {
		t.Fatalf("expected PGPRequired to be true after update, got false")
	}
}

func TestRoomStore_SetPGPRequired_NonCreatorForbidden(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	otherID, err := st.Users.CreateUser(ctx, "other", "testhash")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Rooms.SetRoomPGPRequired(ctx, roomID, otherID, true); !errors.Is(err, ErrNotRoomCreator) {
		t.Fatalf("expected ErrNotRoomCreator, got %v", err)
	}
}
