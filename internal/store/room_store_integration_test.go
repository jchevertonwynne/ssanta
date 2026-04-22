package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRoomStore_LeaveRoom_DeletesInvitesForNonCreator(t *testing.T) {
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
	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
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
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	otherID, err := st.Users.CreateUser(ctx, "other", "testhash")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Rooms.SetRoomMembersCanInvite(ctx, roomID, otherID, true); !errors.Is(err, ErrNotRoomCreator) {
		t.Fatalf("expected ErrNotRoomCreator, got %v", err)
	}
}

func TestRoomStore_SetPGPRequired_Success(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
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
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	otherID, err := st.Users.CreateUser(ctx, "other", "testhash")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := st.Rooms.SetRoomPGPRequired(ctx, roomID, otherID, true); !errors.Is(err, ErrNotRoomCreator) {
		t.Fatalf("expected ErrNotRoomCreator, got %v", err)
	}
}

func TestRoomStore_CreateRoom_IsDM_Field(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}

	regularID, err := st.Rooms.CreateRoom(ctx, "regular-room", creatorID, false)
	if err != nil {
		t.Fatalf("create regular room: %v", err)
	}

	dmID, err := st.Rooms.CreateRoom(ctx, "dm:aaa:bbb", creatorID, true)
	if err != nil {
		t.Fatalf("create dm room: %v", err)
	}

	rd, err := st.Rooms.GetRoomDetail(ctx, regularID)
	if err != nil {
		t.Fatalf("get regular room detail: %v", err)
	}
	if rd.IsDM {
		t.Fatalf("expected regular room IsDM=false, got true")
	}

	rd, err = st.Rooms.GetRoomDetail(ctx, dmID)
	if err != nil {
		t.Fatalf("get dm room detail: %v", err)
	}
	if !rd.IsDM {
		t.Fatalf("expected DM room IsDM=true, got false")
	}
}

func TestRoomStore_LeaveRoom_DMDeletesOnLastLeave(t *testing.T) {
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

	// DM with a non-standard name to prove is_dm column drives deletion, not name prefix
	dmID, err := st.Rooms.CreateRoom(ctx, "not-dm-prefix", creatorID, true)
	if err != nil {
		t.Fatalf("create dm room: %v", err)
	}
	if err := st.Rooms.JoinRoom(ctx, dmID, creatorID); err != nil {
		t.Fatalf("join creator: %v", err)
	}
	if err := st.Rooms.JoinRoom(ctx, dmID, memberID); err != nil {
		t.Fatalf("join member: %v", err)
	}

	// First leave — room survives
	if err := st.Rooms.LeaveRoom(ctx, dmID, memberID); err != nil {
		t.Fatalf("leave member: %v", err)
	}
	if _, err := st.Rooms.GetRoomDetail(ctx, dmID); err != nil {
		t.Fatalf("room should still exist after first leave, got: %v", err)
	}

	// Second leave — room deleted
	if err := st.Rooms.LeaveRoom(ctx, dmID, creatorID); err != nil {
		t.Fatalf("leave creator: %v", err)
	}
	if _, err := st.Rooms.GetRoomDetail(ctx, dmID); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("expected room to be deleted after all members leave, got: %v", err)
	}
}

func TestRoomStore_ListRoomsByMember_ExcludesDMs(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := New(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	creatorID, err := st.Users.CreateUser(ctx, "creator", "testhash")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}

	regularID, err := st.Rooms.CreateRoom(ctx, "regular", creatorID, false)
	if err != nil {
		t.Fatalf("create regular room: %v", err)
	}
	dmID, err := st.Rooms.CreateRoom(ctx, "dm:aaa:bbb", creatorID, true)
	if err != nil {
		t.Fatalf("create dm room: %v", err)
	}

	if err := st.Rooms.JoinRoom(ctx, regularID, creatorID); err != nil {
		t.Fatalf("join regular: %v", err)
	}
	if err := st.Rooms.JoinRoom(ctx, dmID, creatorID); err != nil {
		t.Fatalf("join dm: %v", err)
	}

	rooms, err := st.Rooms.ListRoomsByMember(ctx, creatorID)
	if err != nil {
		t.Fatalf("list rooms by member: %v", err)
	}
	for _, r := range rooms {
		if r.IsDM {
			t.Fatalf("ListRoomsByMember returned DM room %d", r.ID)
		}
	}

	dmRooms, err := st.Rooms.ListDMRoomsByMember(ctx, creatorID)
	if err != nil {
		t.Fatalf("list dm rooms by member: %v", err)
	}
	for _, r := range dmRooms {
		if !r.IsDM {
			t.Fatalf("ListDMRoomsByMember returned non-DM room %d", r.ID)
		}
	}
	if len(dmRooms) != 1 || dmRooms[0].ID != dmID {
		t.Fatalf("expected exactly the dm room, got %v", dmRooms)
	}
}
