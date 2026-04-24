package ws

import (
	"context"
	"net/http"

	"github.com/jchevertonwynne/ssanta/internal/model"
)

// Service lists every method the WS layer calls on the service layer.
type Service interface {
	UserExists(ctx context.Context, id model.UserID) (bool, error)
	GetUserSessionVersion(ctx context.Context, id model.UserID) (int, error)
	GetUsername(ctx context.Context, userID model.UserID) (string, error)
	IsRoomMember(ctx context.Context, roomID model.RoomID, userID model.UserID) (bool, error)
	ListRoomMembersWithPGP(ctx context.Context, roomID model.RoomID) ([]model.RoomMember, error)
	IsRoomPGPRequired(ctx context.Context, roomID model.RoomID) (bool, error)
	GetRoomAccess(ctx context.Context, roomID model.RoomID, userID model.UserID) (isCreator bool, isMember bool, err error)
	CreateMessage(ctx context.Context, roomID model.RoomID, userID model.UserID, username, message string, whisper bool, targetUserID *model.UserID, preEncrypted bool) (model.MessageID, error)
	ListMessagesAfterID(ctx context.Context, roomID model.RoomID, userID model.UserID, afterID model.MessageID, limit int) ([]model.Message, error)
}

// SessionReader is the subset of session.Manager the WS handlers need.
type SessionReader interface {
	UserID(r *http.Request) (model.UserID, int, bool)
	Clear(w http.ResponseWriter)
	Secure() bool
}
