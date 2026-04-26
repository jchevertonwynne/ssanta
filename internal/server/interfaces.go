package server

//go:generate go run -mod=mod go.uber.org/mock/mockgen -typed -source=interfaces.go -destination=mocks/mock_interfaces.go -package=servermocks

import (
	"context"
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/model"
	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/ws"
)

// Capability interfaces (small and reusable).

type HealthService interface {
	Ping(ctx context.Context) error
}

type ContentViewService interface {
	GetContentView(ctx context.Context, userID model.UserID) (*service.ContentView, error)
}

type RoomDetailViewService interface {
	GetRoomDetailView(ctx context.Context, roomID model.RoomID, userID model.UserID) (*service.RoomDetailView, error)
}

type RoomMembersWithPGPService interface {
	ListRoomMembersWithPGP(ctx context.Context, roomID model.RoomID) ([]model.RoomMember, error)
}

type UserExistsService interface {
	UserExists(ctx context.Context, id model.UserID) (bool, error)
	GetUserSessionVersion(ctx context.Context, id model.UserID) (int, error)
}

type CreateUserService interface {
	CreateUser(ctx context.Context, username, password string) (model.UserID, error)
}

type LoginUserService interface {
	LoginUser(ctx context.Context, username, password string) (model.UserID, error)
}

type DeleteUserService interface {
	DeleteUser(ctx context.Context, id model.UserID) error
}

type ChangePasswordService interface {
	ChangePassword(ctx context.Context, userID model.UserID, currentPassword, newPassword string) error
}

type UsernameService interface {
	GetUsername(ctx context.Context, userID model.UserID) (string, error)
}

type UserLookupService interface {
	GetUserByUsername(ctx context.Context, username string) (model.User, error)
}

type CreateRoomService interface {
	CreateRoom(ctx context.Context, displayName string, creatorID model.UserID) (model.RoomID, error)
}

type DeleteRoomService interface {
	DeleteRoom(ctx context.Context, roomID model.RoomID, creatorID model.UserID) error
}

type JoinRoomService interface {
	JoinRoom(ctx context.Context, roomID model.RoomID, userID model.UserID) error
}

type LeaveRoomService interface {
	LeaveRoom(ctx context.Context, roomID model.RoomID, userID model.UserID) error
}

type RemoveMemberService interface {
	RemoveMember(ctx context.Context, roomID model.RoomID, memberID, creatorID model.UserID) error
}

type IsRoomCreatorService interface {
	IsRoomCreator(ctx context.Context, roomID model.RoomID, userID model.UserID) (bool, error)
}

type IsRoomMemberService interface {
	IsRoomMember(ctx context.Context, roomID model.RoomID, userID model.UserID) (bool, error)
}

type IsRoomPGPRequiredService interface {
	IsRoomPGPRequired(ctx context.Context, roomID model.RoomID) (bool, error)
}

type RoomAccessService interface {
	GetRoomAccess(ctx context.Context, roomID model.RoomID, userID model.UserID) (isCreator bool, isMember bool, err error)
}

type SetRoomMembersCanInviteService interface {
	SetRoomMembersCanInvite(ctx context.Context, roomID model.RoomID, creatorID model.UserID, value bool) error
}

type SetRoomPGPRequiredService interface {
	SetRoomPGPRequired(ctx context.Context, roomID model.RoomID, creatorID model.UserID, value bool) error
}

type SetRoomPublicService interface {
	SetRoomPublic(ctx context.Context, roomID model.RoomID, creatorID model.UserID, value bool) error
}

type IsRoomPublicService interface {
	IsRoomPublic(ctx context.Context, roomID model.RoomID) (bool, error)
}

type RoomPGPService interface {
	SetRoomPGPKey(ctx context.Context, roomID model.RoomID, userID model.UserID, armoredPublicKey string) error
	VerifyRoomPGPKey(ctx context.Context, roomID model.RoomID, userID model.UserID, decryptedChallenge string) error
	RemoveRoomUserPGPKey(ctx context.Context, roomID model.RoomID, targetUserID, actingUserID model.UserID) error
}

type InviteOpsService interface {
	CreateInvite(ctx context.Context, roomID model.RoomID, inviterID model.UserID, inviteeUsername string) error
	AcceptInvite(ctx context.Context, inviteID model.InviteID, userID model.UserID) (model.RoomID, error)
	DeclineInvite(ctx context.Context, inviteID model.InviteID, userID model.UserID) error
	CancelInvite(ctx context.Context, inviteID model.InviteID, actingUserID model.UserID) (model.RoomID, model.UserID, error)
	RoomIDForInvite(ctx context.Context, inviteID model.InviteID) (model.RoomID, error)
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
	VerifyPassword(ctx context.Context, userID model.UserID, password string) error
}

// RoomHandlersService aggregates all dependencies for room HTTP handlers.
//
//nolint:interfacebloat // composed of small single-method interfaces
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
	RoomMembersWithPGPService
	RoomAccessService
	SetRoomMembersCanInviteService
	SetRoomPGPRequiredService
	SetRoomPublicService
	IsRoomPublicService
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

type MessageHistoryService interface {
	CreateMessage(ctx context.Context, roomID model.RoomID, userID model.UserID, username, message string, whisper bool, targetUserID *model.UserID, preEncrypted bool) (model.MessageID, error)
	ListMessages(ctx context.Context, roomID model.RoomID, userID model.UserID, beforeID model.MessageID, limit int) ([]model.Message, error)
	ListMessagesAfterID(ctx context.Context, roomID model.RoomID, userID model.UserID, afterID model.MessageID, limit int) ([]model.Message, error)
	SearchMessages(ctx context.Context, roomID model.RoomID, userID model.UserID, query string, limit int) ([]model.Message, error)
}

type MessageListService interface {
	UserExistsService
	MessageHistoryService
	RoomAccessService
	IsRoomPublicService
}

type WebSocketHandlersService interface {
	UserExistsService
	UsernameService
	IsRoomMemberService
	RoomMembersWithPGPService
	IsRoomPGPRequiredService
	IsRoomPublicService
	RoomAccessService
	MessageHistoryService
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
	MessageHistoryService
	DMHandlersService
	AdminHandlersService
}

type SessionManager interface {
	Set(w http.ResponseWriter, userID model.UserID, version int)
	Clear(w http.ResponseWriter)
	UserID(r *http.Request) (model.UserID, int, bool)
	Secret() []byte
	Secure() bool
}

type DMHandlersService interface {
	UserExistsService
	ContentViewService
	GetOrCreateDMRoom(ctx context.Context, user1ID, user2ID model.UserID) (model.RoomID, error)
}

type IsAdminService interface {
	IsUserAdmin(ctx context.Context, userID model.UserID) (bool, error)
}

type GetAdminViewService interface {
	GetAdminView(ctx context.Context, adminID model.UserID) (*service.AdminView, error)
}

type AdminDeleteUserService interface {
	AdminDeleteUser(ctx context.Context, adminID, targetID model.UserID) error
}

type AdminDeleteRoomService interface {
	AdminDeleteRoom(ctx context.Context, adminID model.UserID, roomID model.RoomID) error
}

type SetUserAdminService interface {
	SetUserAdmin(ctx context.Context, adminID, targetID model.UserID, grant bool) error
}

type AdminHandlersService interface {
	UserExistsService
	IsAdminService
	GetAdminViewService
	AdminDeleteUserService
	AdminDeleteRoomService
	SetUserAdminService
}

type Hub interface {
	BroadcastSystemMessage(roomID model.RoomID, message string)
	NotifyRoomUpdate(roomID model.RoomID)
	NotifyUser(userID model.UserID, msgType ws.MsgType, message string)
	NotifyContentUpdate(msgType ws.MsgType)
	DisconnectUser(roomID model.RoomID, userID model.UserID)
	DisconnectRoom(roomID model.RoomID)
	BroadcastRoomPresence(roomID model.RoomID)
	KickSpectators(roomID model.RoomID, memberIDs map[model.UserID]struct{})
}
