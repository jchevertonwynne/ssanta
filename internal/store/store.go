package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jchevertonwynne/ssanta/internal/model"
)

const MaxRoomNameLength = 64

var (
	ErrRoomNameTaken          = errors.New("room name already taken")
	ErrRoomNameEmpty          = errors.New("room name cannot be empty")
	ErrRoomNameTooLong        = errors.New("room name too long")
	ErrRoomNameReservedPrefix = errors.New("room name cannot use the dm: prefix")
	ErrRoomNotFound           = errors.New("room not found")

	ErrUsernameTaken            = errors.New("username already taken")
	ErrUsernameInvalid          = errors.New("username must be 3-32 letters or digits")
	ErrPasswordTooShort         = errors.New("password must be at least 8 characters")
	ErrInvalidCredentials       = errors.New("invalid username or password")
	ErrCurrentPasswordIncorrect = errors.New("current password is incorrect")

	ErrUserNotFound = errors.New("user not found")

	ErrInviteeNotFound          = errors.New("user to invite not found")
	ErrAlreadyMember            = errors.New("user is already a member")
	ErrAlreadyInvited           = errors.New("user is already invited")
	ErrCannotInviteSelf         = errors.New("cannot invite yourself")
	ErrInviteNotFound           = errors.New("invite not found")
	ErrInviteExpired            = errors.New("invite expired")
	ErrNotAllowedToInvite       = errors.New("not allowed to invite to this room")
	ErrNotAllowedToCancelInvite = errors.New("not allowed to cancel this invite")
	ErrNotRoomMember            = errors.New("not a member of this room")
	ErrNotRoomCreator           = errors.New("not the room creator")
	ErrCannotRemoveCreator      = errors.New("cannot remove the room creator")

	ErrNotAdmin         = errors.New("not an admin")
	ErrCannotSelfDemote = errors.New("cannot remove your own admin status")

	ErrPGPKeyInvalid = errors.New("invalid pgp public key")
	ErrPGPChallengeMissing   = errors.New("pgp challenge missing")
	ErrPGPChallengeExpired   = errors.New("pgp challenge expired")
	ErrPGPChallengeIncorrect = errors.New("pgp challenge incorrect")

	ErrOperationNotAllowedOnDM = errors.New("operation not allowed on DM room")
)

type Store struct {
	Users   *UserStore
	Rooms   *RoomStore
	Invites *InviteStore
	Chat    *MessageStore

	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{
		Users:   &UserStore{pool},
		Rooms:   &RoomStore{pool},
		Invites: &InviteStore{pool},
		Chat:    &MessageStore{pool, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)},
		pool:    pool,
	}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Type aliases so existing store-internal code continues to work unqualified.
type (
	UserID        = model.UserID
	RoomID        = model.RoomID
	InviteID      = model.InviteID
	MessageID     = model.MessageID
	User          = model.User
	RoomMember    = model.RoomMember
	Room          = model.Room
	RoomDetail    = model.RoomDetail
	InviteForUser = model.InviteForUser
	InviteForRoom = model.InviteForRoom
	Message       = model.Message
)
