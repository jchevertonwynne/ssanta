package store

import (
	"context"
	"testing"
	"time"
)

func TestRoomStore_LeaveRoom_DeletesInvitesForNonCreator(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	memberID, err := st.Users.CreateUser(ctx, "member")
	if err != nil {
		t.Fatalf("create member: %v", err)
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

	if err := st.Rooms.JoinRoom(ctx, roomID, memberID); err != nil {
		t.Fatalf("join room: %v", err)
	}

	if err := st.Rooms.SetRoomMembersCanInvite(ctx, roomID, creatorID, true); err != nil {
		t.Fatalf("enable members_can_invite: %v", err)
	}

	if err := st.Invites.CreateInvite(ctx, roomID, memberID, "invitee"); err != nil {
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

	creatorID, err := st.Users.CreateUser(ctx, "creator")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	inviteeID, err := st.Users.CreateUser(ctx, "invitee")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}
	_ = inviteeID

	if err := st.Rooms.CreateRoom(ctx, "room", creatorID); err != nil {
		t.Fatalf("create room: %v", err)
	}
	rooms, err := st.Rooms.ListRoomsByCreator(ctx, creatorID)
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	roomID := rooms[0].ID

	// Creator must first join to be a member, then create an invite.
	if err := st.Rooms.JoinRoom(ctx, roomID, creatorID); err != nil {
		t.Fatalf("join room: %v", err)
	}
	if err := st.Invites.CreateInvite(ctx, roomID, creatorID, "invitee"); err != nil {
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

	creatorID, err := st.Users.CreateUser(ctx, "creator")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	memberID, err := st.Users.CreateUser(ctx, "member")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if err := st.Rooms.CreateRoom(ctx, "room", creatorID); err != nil {
		t.Fatalf("create room: %v", err)
	}
	rooms, err := st.Rooms.ListRoomsByCreator(ctx, creatorID)
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	roomID := rooms[0].ID

	if err := st.Rooms.JoinRoom(ctx, roomID, memberID); err != nil {
		t.Fatalf("join room: %v", err)
	}
	if err := st.Rooms.JoinRoom(ctx, roomID, memberID); err != nil {
		t.Fatalf("join room (second time): %v", err)
	}

	members, err := st.Rooms.ListRoomMembers(ctx, roomID)
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

	creatorID, err := st.Users.CreateUser(ctx, "creator")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	otherID, err := st.Users.CreateUser(ctx, "other")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	if err := st.Rooms.CreateRoom(ctx, "room", creatorID); err != nil {
		t.Fatalf("create room: %v", err)
	}
	rooms, err := st.Rooms.ListRoomsByCreator(ctx, creatorID)
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	roomID := rooms[0].ID

	if err := st.Rooms.SetRoomMembersCanInvite(ctx, roomID, otherID, true); err != ErrNotRoomCreator {
		t.Fatalf("expected ErrNotRoomCreator, got %v", err)
	}
}
