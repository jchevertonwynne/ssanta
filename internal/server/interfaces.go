package server

//go:generate go run -mod=mod go.uber.org/mock/mockgen -typed -source=interfaces.go -destination=mocks/mock_interfaces.go -package=servermocks

import (
	"context"
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

// Capability interfaces (small and reusable).

type HealthService interface {
	Ping(ctx context.Context) error
}

type ContentViewService interface {
	GetContentView(ctx context.Context, userID store.UserID) (*service.ContentView, error)
}

type RoomDetailViewService interface {
	GetRoomDetailView(ctx context.Context, roomID store.RoomID, userID store.UserID) (*service.RoomDetailView, error)
}

type RoomMembersWithPGPService interface {
	ListRoomMembersWithPGP(ctx context.Context, roomID store.RoomID) ([]store.RoomMember, error)
}

type UserExistsService interface {
	UserExists(ctx context.Context, id store.UserID) (bool, error)
}

type CreateUserService interface {
	CreateUser(ctx context.Context, username, password string) (store.UserID, error)
}

type LoginUserService interface {
	LoginUser(ctx context.Context, username, password string) (store.UserID, error)
}

type DeleteUserService interface {
	DeleteUser(ctx context.Context, id store.UserID) error
}

type ChangePasswordService interface {
	ChangePassword(ctx context.Context, userID store.UserID, currentPassword, newPassword string) error
}

type UsernameService interface {
	GetUsername(ctx context.Context, userID store.UserID) (string, error)
}

type UserLookupService interface {
	GetUserByUsername(ctx context.Context, username string) (store.User, error)
}

type CreateRoomService interface {
	CreateRoom(ctx context.Context, displayName string, creatorID store.UserID) (store.RoomID, error)
}

type DeleteRoomService interface {
	DeleteRoom(ctx context.Context, roomID store.RoomID, creatorID store.UserID) error
}

type JoinRoomService interface {
	JoinRoom(ctx context.Context, roomID store.RoomID, userID store.UserID) error
}

type LeaveRoomService interface {
	LeaveRoom(ctx context.Context, roomID store.RoomID, userID store.UserID) error
}

type RemoveMemberService interface {
	RemoveMember(ctx context.Context, roomID store.RoomID, memberID, creatorID store.UserID) error
}

type IsRoomCreatorService interface {
	IsRoomCreator(ctx context.Context, roomID store.RoomID, userID store.UserID) (bool, error)
}

type IsRoomMemberService interface {
	IsRoomMember(ctx context.Context, roomID store.RoomID, userID store.UserID) (bool, error)
}

type IsRoomPGPRequiredService interface {
	IsRoomPGPRequired(ctx context.Context, roomID store.RoomID) (bool, error)
}

type RoomAccessService interface {
	GetRoomAccess(ctx context.Context, roomID store.RoomID, userID store.UserID) (isCreator bool, isMember bool, err error)
}

type SetRoomMembersCanInviteService interface {
	SetRoomMembersCanInvite(ctx context.Context, roomID store.RoomID, creatorID store.UserID, value bool) error
}

type SetRoomPGPRequiredService interface {
	SetRoomPGPRequired(ctx context.Context, roomID store.RoomID, creatorID store.UserID, value bool) error
}

type RoomPGPService interface {
	SetRoomPGPKey(ctx context.Context, roomID store.RoomID, userID store.UserID, armoredPublicKey string) error
	VerifyRoomPGPKey(ctx context.Context, roomID store.RoomID, userID store.UserID, decryptedChallenge string) error
	RemoveRoomUserPGPKey(ctx context.Context, roomID store.RoomID, targetUserID, actingUserID store.UserID) error
}

type InviteOpsService interface {
	CreateInvite(ctx context.Context, roomID store.RoomID, inviterID store.UserID, inviteeUsername string) error
	AcceptInvite(ctx context.Context, inviteID store.InviteID, userID store.UserID) (store.RoomID, error)
	DeclineInvite(ctx context.Context, inviteID store.InviteID, userID store.UserID) error
	CancelInvite(ctx context.Context, inviteID store.InviteID, actingUserID store.UserID) (store.RoomID, store.UserID, error)
	RoomIDForInvite(ctx context.Context, inviteID store.InviteID) (store.RoomID, error)
}

// Handler-facing composite interfaces (stable surfaces for mocks).

type ContentHandlersService interface {
	UserExistsService
	ContentViewService
}

type UserHandlersService interface {
	UserExistsService
	ContentViewService
	CreateUserService
	LoginUserService
	DeleteUserService
	ChangePasswordService
}

type RoomHandlersService interface {
	UserExistsService
	ContentViewService
	RoomDetailViewService
	UsernameService
	CreateRoomService
	DeleteRoomService
	JoinRoomService
	LeaveRoomService
	RemoveMemberService
	RoomAccessService
	SetRoomMembersCanInviteService
	SetRoomPGPRequiredService
	RoomPGPService
}

type InviteHandlersService interface {
	UserExistsService
	ContentViewService
	RoomDetailViewService
	UsernameService
	UserLookupService
	InviteOpsService
}

type WebSocketHandlersService interface {
	UserExistsService
	UsernameService
	IsRoomMemberService
	RoomMembersWithPGPService
	IsRoomPGPRequiredService
	RoomAccessService
}

// ServerService is the full surface required to wire all routes.
// Production code passes the concrete *service.Service which satisfies all of these.
type ServerService interface {
	HealthService
	ContentHandlersService
	UserHandlersService
	RoomHandlersService
	InviteHandlersService
	WebSocketHandlersService
	DMHandlersService
}

type SessionManager interface {
	Set(w http.ResponseWriter, userID store.UserID)
	Clear(w http.ResponseWriter)
	UserID(r *http.Request) (store.UserID, bool)
}

type DMHandlersService interface {
	UserExistsService
	ContentViewService
	GetOrCreateDMRoom(ctx context.Context, user1ID, user2ID store.UserID) (store.RoomID, error)
}

type Hub interface {
	BroadcastSystemMessage(roomID store.RoomID, message string)
	NotifyRoomUpdate(roomID store.RoomID)
	NotifyUser(userID store.UserID, msgType, message string)
	NotifyContentUpdate(msgType string)
	DisconnectUser(roomID store.RoomID, userID store.UserID)
}
