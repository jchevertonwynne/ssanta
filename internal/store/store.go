package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxRoomNameLength = 64

var (
	ErrRoomNameTaken = errors.New("room name already taken")
	ErrRoomNameEmpty = errors.New("room name cannot be empty")
	ErrRoomNameTooLong = errors.New("room name too long")
	ErrRoomNotFound  = errors.New("room not found")

	ErrUsernameTaken = errors.New("username already taken")
	ErrUsernameInvalid = errors.New("username must be 3-32 letters or digits")

	ErrUserNotFound = errors.New("user not found")

	ErrInviteeNotFound          = errors.New("user to invite not found")
	ErrAlreadyMember            = errors.New("user is already a member")
	ErrAlreadyInvited           = errors.New("user is already invited")
	ErrCannotInviteSelf         = errors.New("cannot invite yourself")
	ErrInviteNotFound           = errors.New("invite not found")
	ErrNotAllowedToInvite       = errors.New("not allowed to invite to this room")
	ErrNotAllowedToCancelInvite = errors.New("not allowed to cancel this invite")
	ErrNotRoomMember            = errors.New("not a member of this room")
	ErrNotRoomCreator           = errors.New("not the room creator")
	ErrCannotRemoveCreator      = errors.New("cannot remove the room creator")

	ErrPGPKeyInvalid        = errors.New("invalid pgp public key")
	ErrPGPChallengeMissing  = errors.New("pgp challenge missing")
	ErrPGPChallengeExpired  = errors.New("pgp challenge expired")
	ErrPGPChallengeIncorrect = errors.New("pgp challenge incorrect")
)

type Store struct {
	Users   *UserStore
	Rooms   *RoomStore
	Invites *InviteStore
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{
		Users:   &UserStore{pool: pool},
		Rooms:   &RoomStore{pool: pool},
		Invites: &InviteStore{pool: pool},
	}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.Users.pool.Ping(ctx)
}

type User struct {
	ID        int64
	Username  string
	CreatedAt time.Time
}

type RoomMember struct {
	ID        int64
	Username  string
	CreatedAt time.Time

	PGPPublicKey           string
	PGPFingerprint         string
	PGPVerifiedAt          *time.Time
	PGPChallengeCiphertext string
	PGPChallengeExpiresAt  *time.Time
}

type Room struct {
	ID          int64
	DisplayName string
	CreatedAt   time.Time
}

type RoomDetail struct {
	Room
	CreatorID        int64
	CreatorUsername  string
	MembersCanInvite bool
}

type InviteForUser struct {
	InviteID    int64
	RoomID      int64
	RoomName    string
	InviterID   int64
	InviterName string
	CreatedAt   time.Time
}

type InviteForRoom struct {
	InviteID    int64
	InviterID   int64
	InviterName string
	InviteeID   int64
	InviteeName string
	CreatedAt   time.Time
}
