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
	GetContentView(ctx context.Context, userID int64) (*service.ContentView, error)
}

type RoomDetailViewService interface {
	GetRoomDetailView(ctx context.Context, roomID, userID int64) (*service.RoomDetailView, error)
}

type UserExistsService interface {
	UserExists(ctx context.Context, id int64) (bool, error)
}

type CreateUserService interface {
	CreateUser(ctx context.Context, username string) (int64, error)
}

type DeleteUserService interface {
	DeleteUser(ctx context.Context, id int64) error
}

type UsernameService interface {
	GetUsername(ctx context.Context, userID int64) (string, error)
}

type UserLookupService interface {
	GetUserByUsername(ctx context.Context, username string) (store.User, error)
}

type CreateRoomService interface {
	CreateRoom(ctx context.Context, displayName string, creatorID int64) error
}

type DeleteRoomService interface {
	DeleteRoom(ctx context.Context, roomID, creatorID int64) error
}

type JoinRoomService interface {
	JoinRoom(ctx context.Context, roomID, userID int64) error
}

type LeaveRoomService interface {
	LeaveRoom(ctx context.Context, roomID, userID int64) error
}

type RemoveMemberService interface {
	RemoveMember(ctx context.Context, roomID, memberID, creatorID int64) error
}

type IsRoomCreatorService interface {
	IsRoomCreator(ctx context.Context, roomID, userID int64) (bool, error)
}

type IsRoomMemberService interface {
	IsRoomMember(ctx context.Context, roomID, userID int64) (bool, error)
}

type SetRoomMembersCanInviteService interface {
	SetRoomMembersCanInvite(ctx context.Context, roomID, creatorID int64, value bool) error
}

type RoomPGPService interface {
	SetRoomPGPKey(ctx context.Context, roomID, userID int64, armoredPublicKey string) error
	VerifyRoomPGPKey(ctx context.Context, roomID, userID int64, decryptedChallenge string) error
}

type InviteOpsService interface {
	CreateInvite(ctx context.Context, roomID, inviterID int64, inviteeUsername string) error
	AcceptInvite(ctx context.Context, inviteID, userID int64) error
	DeclineInvite(ctx context.Context, inviteID, userID int64) error
	CancelInvite(ctx context.Context, inviteID, actingUserID int64) error
	RoomIDForInvite(ctx context.Context, inviteID int64) (int64, error)
	InviteeIDForInvite(ctx context.Context, inviteID int64) (int64, error)
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
	DeleteUserService
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
	IsRoomCreatorService
	SetRoomMembersCanInviteService
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
}

type SessionManager interface {
	Set(w http.ResponseWriter, userID int64)
	Clear(w http.ResponseWriter)
	UserID(r *http.Request) (int64, bool)
}

type Hub interface {
	BroadcastSystemMessage(roomID int64, message string)
	NotifyRoomUpdate(roomID int64)
	NotifyUser(userID int64, msgType, message string)
	DisconnectUser(roomID, userID int64)
}
