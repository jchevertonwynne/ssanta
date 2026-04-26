//go:generate go run -mod=mod go.uber.org/mock/mockgen -typed -source=interfaces.go -destination=mocks/mock_interfaces.go -package=storemocks

package store

import (
	"context"
	"time"
)

// UserStore defines user-related persistence operations.
//
//nolint:interfacebloat
type UserStore interface {
	BumpSessionVersion(ctx context.Context, id UserID) error
	CreateUser(ctx context.Context, username, passwordHash string) (UserID, error)
	DeleteUser(ctx context.Context, id UserID) error
	GetUserByID(ctx context.Context, id UserID) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	GetUserSessionVersion(ctx context.Context, id UserID) (int, error)
	GetUserWithPassword(ctx context.Context, username string) (UserWithPassword, error)
	GetUserWithPasswordByID(ctx context.Context, id UserID) (UserWithPassword, error)
	GrantAdmin(ctx context.Context, targetID, grantedBy UserID) error
	IsUserAdmin(ctx context.Context, id UserID) (bool, error)
	ListAllUsers(ctx context.Context) ([]AdminUser, error)
	ListUsers(ctx context.Context) ([]User, error)
	RevokeAdmin(ctx context.Context, targetID UserID) error
	UpdatePasswordHash(ctx context.Context, id UserID, passwordHash string) error
	UserExists(ctx context.Context, id UserID) (bool, error)
}

// RoomStore defines room-related persistence operations.
//
//nolint:interfacebloat
type RoomStore interface {
	AdminDeleteRoom(ctx context.Context, roomID RoomID) error
	ClearExpiredRoomPGPChallenges(ctx context.Context, now time.Time) (int64, error)
	ClearRoomUserPGPKey(ctx context.Context, roomID RoomID, userID UserID) error
	CreateRoom(ctx context.Context, displayName string, creatorID UserID, isDM bool) (RoomID, error)
	DeleteRoom(ctx context.Context, roomID RoomID, creatorID UserID) error
	GetOrCreateDMRoom(ctx context.Context, displayName string, creatorID UserID) (RoomID, error)
	GetRoomAccess(ctx context.Context, roomID RoomID, userID UserID) (bool, bool, error)
	GetRoomDetail(ctx context.Context, roomID RoomID) (RoomDetail, error)
	IsRoomCreator(ctx context.Context, roomID RoomID, userID UserID) (bool, error)
	IsRoomMember(ctx context.Context, roomID RoomID, userID UserID) (bool, error)
	IsRoomPGPRequired(ctx context.Context, roomID RoomID) (bool, error)
	IsRoomPublic(ctx context.Context, roomID RoomID) (bool, error)
	JoinRoom(ctx context.Context, roomID RoomID, userID UserID) error
	LeaveRoom(ctx context.Context, roomID RoomID, userID UserID) error
	ListAllRooms(ctx context.Context) ([]RoomDetail, error)
	ListDMRoomsByMember(ctx context.Context, userID UserID) ([]Room, error)
	ListPublicRooms(ctx context.Context, userID UserID) ([]Room, error)
	ListRoomMembersWithPGP(ctx context.Context, roomID RoomID) ([]RoomMember, error)
	ListRoomsByCreator(ctx context.Context, userID UserID) ([]Room, error)
	ListRoomsByMember(ctx context.Context, userID UserID) ([]Room, error)
	RemoveMember(ctx context.Context, roomID RoomID, memberID, creatorID UserID) error
	SetRoomMembersCanInvite(ctx context.Context, roomID RoomID, creatorID UserID, value bool) error
	SetRoomPGPRequired(ctx context.Context, roomID RoomID, creatorID UserID, value bool) error
	SetRoomPublic(ctx context.Context, roomID RoomID, creatorID UserID, value bool) error
	UpsertRoomUserPGPKeyWithChallenge(ctx context.Context, roomID RoomID, userID UserID, publicKey, fingerprint, challengeCiphertext string, challengeHash []byte, challengeExpiresAt time.Time) error
	VerifyRoomUserPGPChallenge(ctx context.Context, roomID RoomID, userID UserID, providedPlaintext string, now time.Time) error
}

// InviteStore defines invite-related persistence operations.
type InviteStore interface {
	AcceptInvite(ctx context.Context, inviteID InviteID, userID UserID) (RoomID, error)
	DeleteExpiredInvites(ctx context.Context, now time.Time) (int64, error)
	CancelInvite(ctx context.Context, inviteID InviteID, actingUserID UserID) (RoomID, UserID, error)
	CreateInvite(ctx context.Context, roomID RoomID, inviterID UserID, inviteeUsername string, expiresAt time.Time) error
	DeclineInvite(ctx context.Context, inviteID InviteID, userID UserID) error
	ListInvitesForRoom(ctx context.Context, roomID RoomID) ([]InviteForRoom, error)
	ListInvitesForUser(ctx context.Context, userID UserID) ([]InviteForUser, error)
	RoomIDForInvite(ctx context.Context, inviteID InviteID) (RoomID, error)
}

// MessageStore defines message-related persistence operations.
type MessageStore interface {
	CreateMessage(ctx context.Context, roomID RoomID, userID UserID, username, message string, whisper bool, targetUserID *UserID, preEncrypted bool) (MessageID, error)
	ListMessages(ctx context.Context, roomID RoomID, userID UserID, beforeID MessageID, limit int) ([]Message, error)
	ListMessagesAfterID(ctx context.Context, roomID RoomID, userID UserID, afterID MessageID, limit int) ([]Message, error)
	SearchMessages(ctx context.Context, roomID RoomID, userID UserID, query string, limit int) ([]Message, error)
}
