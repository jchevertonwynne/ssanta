package service

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/jchevertonwynne/ssanta/internal/store"
	storemocks "github.com/jchevertonwynne/ssanta/internal/store/mocks"
)

var errDBDown = errors.New("db down")

type testService struct {
	svc     *Service
	users   *storemocks.MockUserStore
	rooms   *storemocks.MockRoomStore
	invites *storemocks.MockInviteStore
	chat    *storemocks.MockMessageStore
}

func newTestService(t *testing.T) testService {
	t.Helper()
	ctrl := gomock.NewController(t)
	users := storemocks.NewMockUserStore(ctrl)
	rooms := storemocks.NewMockRoomStore(ctrl)
	invites := storemocks.NewMockInviteStore(ctrl)
	chat := storemocks.NewMockMessageStore(ctrl)

	st := &store.Store{
		Users:   users,
		Rooms:   rooms,
		Invites: invites,
		Chat:    chat,
	}
	return testService{svc: New(st), users: users, rooms: rooms, invites: invites, chat: chat}
}

func TestCreateUser_Success(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().CreateUser(gomock.Any(), "alice", gomock.Any()).Return(store.UserID(1), nil)

	id, err := ts.svc.CreateUser(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 1 {
		t.Fatalf("expected id 1, got %d", id)
	}
}

func TestCreateUser_InvalidUsername(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	_, err := ts.svc.CreateUser(context.Background(), "ab", "password123")
	if !errors.Is(err, store.ErrUsernameInvalid) {
		t.Fatalf("expected ErrUsernameInvalid, got %v", err)
	}
}

func TestCreateUser_ShortPassword(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	_, err := ts.svc.CreateUser(context.Background(), "alice", "short")
	if !errors.Is(err, store.ErrPasswordTooShort) {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}
}

func TestLoginUser_Success(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	hash, _ := hashPassword("password123", DefaultArgon2Params())
	ts.users.EXPECT().GetUserWithPassword(gomock.Any(), "alice").Return(store.UserWithPassword{
		User:         store.User{ID: 1, Username: "alice"},
		PasswordHash: hash,
	}, nil)

	id, err := ts.svc.LoginUser(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 1 {
		t.Fatalf("expected id 1, got %d", id)
	}
}

func TestLoginUser_InvalidCredentials(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	hash, _ := hashPassword("password123", DefaultArgon2Params())
	ts.users.EXPECT().GetUserWithPassword(gomock.Any(), "alice").Return(store.UserWithPassword{
		User:         store.User{ID: 1, Username: "alice"},
		PasswordHash: hash,
	}, nil)

	_, err := ts.svc.LoginUser(context.Background(), "alice", "wrongpassword")
	if !errors.Is(err, store.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginUser_UserNotFound(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().GetUserWithPassword(gomock.Any(), "ghost").Return(store.UserWithPassword{}, store.ErrUserNotFound)

	_, err := ts.svc.LoginUser(context.Background(), "ghost", "password123")
	if !errors.Is(err, store.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for missing user, got %v", err)
	}
}

func TestChangePassword_Success(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	hash, _ := hashPassword("oldpassword", DefaultArgon2Params())
	ts.users.EXPECT().GetUserWithPasswordByID(gomock.Any(), store.UserID(1)).Return(store.UserWithPassword{
		User:         store.User{ID: 1, Username: "alice"},
		PasswordHash: hash,
	}, nil)
	ts.users.EXPECT().UpdatePasswordHash(gomock.Any(), store.UserID(1), gomock.Any()).Return(nil)
	ts.users.EXPECT().BumpSessionVersion(gomock.Any(), store.UserID(1)).Return(nil)

	err := ts.svc.ChangePassword(context.Background(), 1, "oldpassword", "newpassword123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	hash, _ := hashPassword("oldpassword", DefaultArgon2Params())
	ts.users.EXPECT().GetUserWithPasswordByID(gomock.Any(), store.UserID(1)).Return(store.UserWithPassword{
		User:         store.User{ID: 1, Username: "alice"},
		PasswordHash: hash,
	}, nil)

	err := ts.svc.ChangePassword(context.Background(), 1, "wrongpassword", "newpassword123")
	if !errors.Is(err, store.ErrCurrentPasswordIncorrect) {
		t.Fatalf("expected ErrCurrentPasswordIncorrect, got %v", err)
	}
}

func TestChangePassword_ShortNewPassword(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	hash, _ := hashPassword("oldpassword", DefaultArgon2Params())
	ts.users.EXPECT().GetUserWithPasswordByID(gomock.Any(), store.UserID(1)).Return(store.UserWithPassword{
		User:         store.User{ID: 1, Username: "alice"},
		PasswordHash: hash,
	}, nil)

	err := ts.svc.ChangePassword(context.Background(), 1, "oldpassword", "short")
	if !errors.Is(err, store.ErrPasswordTooShort) {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}
}

func TestCreateRoom_Success(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.rooms.EXPECT().CreateRoom(gomock.Any(), "my room", store.UserID(1), false).Return(store.RoomID(10), nil)

	id, err := ts.svc.CreateRoom(context.Background(), "my room", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 10 {
		t.Fatalf("expected id 10, got %d", id)
	}
}

func TestCreateRoom_EmptyName(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	_, err := ts.svc.CreateRoom(context.Background(), "", 1)
	if !errors.Is(err, store.ErrRoomNameEmpty) {
		t.Fatalf("expected ErrRoomNameEmpty, got %v", err)
	}
}

func TestGetOrCreateDMRoom_Success(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().GetUserByID(gomock.Any(), store.UserID(1)).Return(store.User{ID: 1, Username: "alice"}, nil)
	ts.users.EXPECT().GetUserByID(gomock.Any(), store.UserID(2)).Return(store.User{ID: 2, Username: "bob"}, nil)
	ts.rooms.EXPECT().GetOrCreateDMRoom(gomock.Any(), "dm:alice:bob", store.UserID(1)).Return(store.RoomID(10), nil)
	ts.rooms.EXPECT().JoinRoom(gomock.Any(), store.RoomID(10), store.UserID(1)).Return(nil)
	ts.rooms.EXPECT().JoinRoom(gomock.Any(), store.RoomID(10), store.UserID(2)).Return(nil)

	id, err := ts.svc.GetOrCreateDMRoom(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 10 {
		t.Fatalf("expected id 10, got %d", id)
	}
}

func TestGetRoomDetailView_NotFound(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.rooms.EXPECT().GetRoomDetail(gomock.Any(), store.RoomID(10)).Return(store.RoomDetail{}, store.ErrRoomNotFound)
	// Other concurrent calls may fire before errgroup cancels the context.
	ts.users.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).Return(store.User{}, nil).AnyTimes()
	ts.rooms.EXPECT().ListRoomMembersWithPGP(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ts.invites.EXPECT().ListInvitesForRoom(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ts.users.EXPECT().ListUsers(gomock.Any()).Return(nil, nil).AnyTimes()

	_, err := ts.svc.GetRoomDetailView(context.Background(), 10, 1)
	if !errors.Is(err, store.ErrRoomNotFound) {
		t.Fatalf("expected ErrRoomNotFound, got %v", err)
	}
}

func TestGetRoomDetailView_NotMemberAndNotPublic(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().GetUserByID(gomock.Any(), store.UserID(1)).Return(store.User{ID: 1, Username: "alice"}, nil)
	ts.rooms.EXPECT().GetRoomDetail(gomock.Any(), store.RoomID(10)).Return(store.RoomDetail{
		Room: store.Room{ID: 10, DisplayName: "secret", IsPublic: false},
	}, nil)
	ts.rooms.EXPECT().ListRoomMembersWithPGP(gomock.Any(), store.RoomID(10)).Return([]store.RoomMember{}, nil)
	ts.invites.EXPECT().ListInvitesForRoom(gomock.Any(), store.RoomID(10)).Return(nil, nil)
	ts.users.EXPECT().ListUsers(gomock.Any()).Return(nil, nil)

	_, err := ts.svc.GetRoomDetailView(context.Background(), 10, 1)
	if !errors.Is(err, store.ErrNotRoomMember) {
		t.Fatalf("expected ErrNotRoomMember, got %v", err)
	}
}

func TestSetRoomPGPKey_NonMember(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.rooms.EXPECT().IsRoomMember(gomock.Any(), store.RoomID(10), store.UserID(1)).Return(false, nil)

	err := ts.svc.SetRoomPGPKey(context.Background(), 10, 1, "some-key")
	if !errors.Is(err, store.ErrNotRoomMember) {
		t.Fatalf("expected ErrNotRoomMember, got %v", err)
	}
}

func TestVerifyRoomPGPKey_EmptyChallenge(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	err := ts.svc.VerifyRoomPGPKey(context.Background(), 10, 1, "")
	if !errors.Is(err, store.ErrPGPChallengeIncorrect) {
		t.Fatalf("expected ErrPGPChallengeIncorrect, got %v", err)
	}
}

func TestAdminDeleteUser_SelfDelete(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().IsUserAdmin(gomock.Any(), store.UserID(1)).Return(true, nil)

	err := ts.svc.AdminDeleteUser(context.Background(), 1, 1)
	if !errors.Is(err, store.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestSetUserAdmin_SelfDemote(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().IsUserAdmin(gomock.Any(), store.UserID(1)).Return(true, nil)

	err := ts.svc.SetUserAdmin(context.Background(), 1, 1, false)
	if !errors.Is(err, store.ErrCannotSelfDemote) {
		t.Fatalf("expected ErrCannotSelfDemote, got %v", err)
	}
}

func TestGetContentView_AnonymousUser(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().ListUsers(gomock.Any()).Return([]store.User{{ID: 1, Username: "alice"}}, nil)

	view, err := ts.svc.GetContentView(context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(view.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(view.Users))
	}
}

func TestDeleteRoom_AssertNotDM(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.rooms.EXPECT().GetRoomDetail(gomock.Any(), store.RoomID(10)).Return(store.RoomDetail{
		Room: store.Room{ID: 10, DisplayName: "dm:alice:bob", IsDM: true},
	}, nil)

	err := ts.svc.DeleteRoom(context.Background(), 10, 1)
	if !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("expected ErrOperationNotAllowedOnDM, got %v", err)
	}
}

func TestRemoveMember_AssertNotDM(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.rooms.EXPECT().GetRoomDetail(gomock.Any(), store.RoomID(10)).Return(store.RoomDetail{
		Room: store.Room{ID: 10, DisplayName: "dm:alice:bob", IsDM: true},
	}, nil)

	err := ts.svc.RemoveMember(context.Background(), 10, 2, 1)
	if !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("expected ErrOperationNotAllowedOnDM, got %v", err)
	}
}

func TestCreateInvite_AssertNotDM(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.rooms.EXPECT().GetRoomDetail(gomock.Any(), store.RoomID(10)).Return(store.RoomDetail{
		Room: store.Room{ID: 10, DisplayName: "dm:alice:bob", IsDM: true},
	}, nil)

	err := ts.svc.CreateInvite(context.Background(), 10, 1, "bob")
	if !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("expected ErrOperationNotAllowedOnDM, got %v", err)
	}
}

func TestSetRoomMembersCanInvite_AssertNotDM(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.rooms.EXPECT().GetRoomDetail(gomock.Any(), store.RoomID(10)).Return(store.RoomDetail{
		Room: store.Room{ID: 10, DisplayName: "dm:alice:bob", IsDM: true},
	}, nil)

	err := ts.svc.SetRoomMembersCanInvite(context.Background(), 10, 1, true)
	if !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("expected ErrOperationNotAllowedOnDM, got %v", err)
	}
}

func TestSetRoomPGPRequired_AssertNotDM(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.rooms.EXPECT().GetRoomDetail(gomock.Any(), store.RoomID(10)).Return(store.RoomDetail{
		Room: store.Room{ID: 10, DisplayName: "dm:alice:bob", IsDM: true},
	}, nil)

	err := ts.svc.SetRoomPGPRequired(context.Background(), 10, 1, true)
	if !errors.Is(err, store.ErrOperationNotAllowedOnDM) {
		t.Fatalf("expected ErrOperationNotAllowedOnDM, got %v", err)
	}
}

func TestGetAdminView_NonAdmin(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().IsUserAdmin(gomock.Any(), store.UserID(1)).Return(false, nil)

	_, err := ts.svc.GetAdminView(context.Background(), 1)
	if !errors.Is(err, store.ErrNotAdmin) {
		t.Fatalf("expected ErrNotAdmin, got %v", err)
	}
}

func TestGetAdminView_Success(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().IsUserAdmin(gomock.Any(), store.UserID(1)).Return(true, nil)
	ts.users.EXPECT().GetUserByID(gomock.Any(), store.UserID(1)).Return(store.User{ID: 1, Username: "admin"}, nil)
	ts.users.EXPECT().ListAllUsers(gomock.Any()).Return([]store.AdminUser{}, nil)
	ts.rooms.EXPECT().ListAllRooms(gomock.Any()).Return([]store.RoomDetail{}, nil)

	view, err := ts.svc.GetAdminView(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.CurrentUsername != "admin" {
		t.Fatalf("expected username admin, got %q", view.CurrentUsername)
	}
}

func TestRequireAdmin_StoreError(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().IsUserAdmin(gomock.Any(), store.UserID(1)).Return(false, errDBDown)

	err := ts.svc.requireAdmin(context.Background(), 1)
	if err == nil || err.Error() != "db down" {
		t.Fatalf("expected db error, got %v", err)
	}
}

func TestUserExists_DelegatesToStore(t *testing.T) {
	t.Parallel()
	ts := newTestService(t)

	ts.users.EXPECT().UserExists(gomock.Any(), store.UserID(1)).Return(true, nil)

	exists, err := ts.svc.UserExists(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("expected user to exist")
	}
}
