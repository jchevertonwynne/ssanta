package ws

import (
	"context"
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/store"
)

// Service lists every method the WS layer calls on the service layer.
type Service interface {
	UserExists(ctx context.Context, id store.UserID) (bool, error)
	GetUserSessionVersion(ctx context.Context, id store.UserID) (int, error)
	GetUsername(ctx context.Context, userID store.UserID) (string, error)
	IsRoomMember(ctx context.Context, roomID store.RoomID, userID store.UserID) (bool, error)
	ListRoomMembersWithPGP(ctx context.Context, roomID store.RoomID) ([]store.RoomMember, error)
	IsRoomPGPRequired(ctx context.Context, roomID store.RoomID) (bool, error)
	GetRoomAccess(ctx context.Context, roomID store.RoomID, userID store.UserID) (isCreator bool, isMember bool, err error)
	CreateMessage(ctx context.Context, roomID store.RoomID, userID store.UserID, username, message string, whisper bool, targetUserID *store.UserID, preEncrypted bool) (store.MessageID, error)
	ListMessagesAfterID(ctx context.Context, roomID store.RoomID, userID store.UserID, afterID store.MessageID, limit int) ([]store.Message, error)
}

// SessionReader is the subset of session.Manager the WS handlers need.
type SessionReader interface {
	UserID(r *http.Request) (store.UserID, int, bool)
	Clear(w http.ResponseWriter)
	Secure() bool
}
