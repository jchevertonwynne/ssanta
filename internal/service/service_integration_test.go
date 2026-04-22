package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

func TestService_GetRoomDetailView_PermissionAndCanInvite(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := store.New(pool)
	svc := New(st)

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
	nonMemberID, err := st.Users.CreateUser(ctx, "nonmember", "testhash")
	if err != nil {
		t.Fatalf("create nonmember: %v", err)
	}

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	// Creator can view even if not a member, and can always invite.
	view, err := svc.GetRoomDetailView(ctx, roomID, creatorID)
	if err != nil {
		t.Fatalf("get room detail view (creator): %v", err)
	}
	if !view.IsCreator {
		t.Fatalf("expected IsCreator=true")
	}
	if view.CanInvite != true {
		t.Fatalf("expected CanInvite=true for creator")
	}

	// Join as member; members cannot invite by default.
	if err := st.Rooms.JoinRoom(ctx, roomID, memberID); err != nil {
		t.Fatalf("join room: %v", err)
	}
	view, err = svc.GetRoomDetailView(ctx, roomID, memberID)
	if err != nil {
		t.Fatalf("get room detail view (member): %v", err)
	}
	if !view.IsMember {
		t.Fatalf("expected IsMember=true")
	}
	if view.CanInvite {
		t.Fatalf("expected CanInvite=false when members_can_invite=false")
	}

	// Toggle members_can_invite; member should now be able to invite.
	if err := st.Rooms.SetRoomMembersCanInvite(ctx, roomID, creatorID, true); err != nil {
		t.Fatalf("set members can invite: %v", err)
	}
	view, err = svc.GetRoomDetailView(ctx, roomID, memberID)
	if err != nil {
		t.Fatalf("get room detail view (member, toggled): %v", err)
	}
	if !view.CanInvite {
		t.Fatalf("expected CanInvite=true when members_can_invite=true")
	}

	// Non-member should be rejected.
	if _, err := svc.GetRoomDetailView(ctx, roomID, nonMemberID); !errors.Is(err, store.ErrNotRoomMember) {
		t.Fatalf("expected ErrNotRoomMember, got %v", err)
	}
}

func TestService_GetContentView_LoggedOutVsLoggedIn(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := store.New(pool)
	svc := New(st)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	id, err := st.Users.CreateUser(ctx, "alice", "testhash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = st.Rooms.CreateRoom(ctx, "room", id, false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	// Logged out: users list populated, but per-user fields should be empty.
	view, err := svc.GetContentView(ctx, 0)
	if err != nil {
		t.Fatalf("get content view (logged out): %v", err)
	}
	if len(view.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(view.Users))
	}
	if view.CurrentUsername != "" {
		t.Fatalf("expected empty current username")
	}
	if len(view.CreatedRooms) != 0 || len(view.MemberRooms) != 0 || len(view.Invites) != 0 {
		t.Fatalf("expected no per-user lists when logged out")
	}

	// Logged in: current username and created rooms populated.
	view, err = svc.GetContentView(ctx, id)
	if err != nil {
		t.Fatalf("get content view (logged in): %v", err)
	}
	if view.CurrentUsername != "alice" {
		t.Fatalf("expected current username alice, got %q", view.CurrentUsername)
	}
	if len(view.CreatedRooms) != 1 {
		t.Fatalf("expected 1 created room, got %d", len(view.CreatedRooms))
	}
}

func TestService_GetOrCreateDMRoom_AutoJoinsAndRejoins(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := store.New(pool)
	svc := New(st)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	userA, err := st.Users.CreateUser(ctx, "alice", "testhash")
	if err != nil {
		t.Fatalf("create userA: %v", err)
	}
	userB, err := st.Users.CreateUser(ctx, "bob", "testhash")
	if err != nil {
		t.Fatalf("create userB: %v", err)
	}

	roomID, err := svc.GetOrCreateDMRoom(ctx, userA, userB)
	if err != nil {
		t.Fatalf("get or create dm room: %v", err)
	}

	// Initiator should be joined automatically.
	isMemberA, err := st.Rooms.IsRoomMember(ctx, roomID, userA)
	if err != nil {
		t.Fatalf("is room member (A): %v", err)
	}
	if !isMemberA {
		t.Fatalf("expected userA to be a member after DM creation")
	}
	// Partner should also be a member.
	isMemberB, err := st.Rooms.IsRoomMember(ctx, roomID, userB)
	if err != nil {
		t.Fatalf("is room member (B): %v", err)
	}
	if !isMemberB {
		t.Fatalf("expected userB to be a member after DM creation")
	}

	// If A leaves, a subsequent DM creation should re-join A (and not create a new room).
	if err := st.Rooms.LeaveRoom(ctx, roomID, userA); err != nil {
		t.Fatalf("leave room (A): %v", err)
	}

	roomID2, err := svc.GetOrCreateDMRoom(ctx, userA, userB)
	if err != nil {
		t.Fatalf("get or create dm room (second call): %v", err)
	}
	if roomID2 != roomID {
		t.Fatalf("expected same DM room id, got %d then %d", roomID.Int64(), roomID2.Int64())
	}

	isMemberA, err = st.Rooms.IsRoomMember(ctx, roomID2, userA)
	if err != nil {
		t.Fatalf("is room member (A, rejoined): %v", err)
	}
	if !isMemberA {
		t.Fatalf("expected userA to be re-joined after DM re-creation")
	}
}

func TestService_DMOperationsBlocked(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := store.New(pool)
	svc := New(st)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	userA, err := st.Users.CreateUser(ctx, "userablock", "testhash")
	if err != nil {
		t.Fatalf("create userA: %v", err)
	}
	userB, err := st.Users.CreateUser(ctx, "userbblock", "testhash")
	if err != nil {
		t.Fatalf("create userB: %v", err)
	}

	dmID, err := svc.GetOrCreateDMRoom(ctx, userA, userB)
	if err != nil {
		t.Fatalf("get or create DM: %v", err)
	}

	if err := svc.SetRoomMembersCanInvite(ctx, dmID, userA, true); !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("SetRoomMembersCanInvite on DM: expected ErrOperationNotAllowedOnDM, got %v", err)
	}
	if err := svc.SetRoomPGPRequired(ctx, dmID, userA, true); !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("SetRoomPGPRequired on DM: expected ErrOperationNotAllowedOnDM, got %v", err)
	}
	if err := svc.DeleteRoom(ctx, dmID, userA); !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("DeleteRoom on DM: expected ErrOperationNotAllowedOnDM, got %v", err)
	}
	if err := svc.CreateInvite(ctx, dmID, userA, "userbblock"); !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("CreateInvite on DM: expected ErrOperationNotAllowedOnDM, got %v", err)
	}
	if err := svc.RemoveMember(ctx, dmID, userB, userA); !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("RemoveMember on DM: expected ErrOperationNotAllowedOnDM, got %v", err)
	}
}

func TestService_GetContentView_ExcludesDMsFromRoomLists(t *testing.T) {
	t.Parallel()
	pool := requireIntegration(t)
	st := store.New(pool)
	svc := New(st)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	userA, err := st.Users.CreateUser(ctx, "useraqq", "testhash")
	if err != nil {
		t.Fatalf("create userA: %v", err)
	}
	userB, err := st.Users.CreateUser(ctx, "userbqq", "testhash")
	if err != nil {
		t.Fatalf("create userB: %v", err)
	}

	regularID, err := svc.CreateRoom(ctx, "my-regular-room", userA)
	if err != nil {
		t.Fatalf("create regular room: %v", err)
	}

	_, err = svc.GetOrCreateDMRoom(ctx, userA, userB)
	if err != nil {
		t.Fatalf("create dm: %v", err)
	}

	view, err := svc.GetContentView(ctx, userA)
	if err != nil {
		t.Fatalf("get content view: %v", err)
	}

	for _, r := range view.CreatedRooms {
		if r.IsDM {
			t.Fatalf("CreatedRooms contains DM room %d", r.ID)
		}
	}
	for _, r := range view.MemberRooms {
		if r.IsDM {
			t.Fatalf("MemberRooms contains DM room %d", r.ID)
		}
	}
	var foundRegular bool
	for _, r := range view.CreatedRooms {
		if r.ID == regularID {
			foundRegular = true
		}
	}
	if !foundRegular {
		t.Fatalf("expected regular room in CreatedRooms")
	}
	if len(view.DMRooms) != 1 {
		t.Fatalf("expected 1 DM room, got %d", len(view.DMRooms))
	}
}
