package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

func TestService_GetRoomDetailView_PermissionAndCanInvite(t *testing.T) {
	pool := requireIntegration(t)
	st := store.New(pool)
	svc := New(st)

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
	nonMemberID, err := st.Users.CreateUser(ctx, "nonmember", "testhash")
	if err != nil {
		t.Fatalf("create nonmember: %v", err)
	}

	roomID, err := st.Rooms.CreateRoom(ctx, "room", creatorID)
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
	pool := requireIntegration(t)
	st := store.New(pool)
	svc := New(st)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := st.Users.CreateUser(ctx, "alice", "testhash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = st.Rooms.CreateRoom(ctx, "room", id)
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
