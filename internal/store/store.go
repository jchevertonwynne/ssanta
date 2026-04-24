package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxRoomNameLength = 64

var (
	errPingOnTxStore   = errors.New("store: Ping called on tx-scoped store")
	errWithTxOnTxStore = errors.New("store: WithTx called on tx-scoped store")

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

	ErrPGPKeyInvalid         = errors.New("invalid pgp public key")
	ErrPGPChallengeMissing   = errors.New("pgp challenge missing")
	ErrPGPChallengeExpired   = errors.New("pgp challenge expired")
	ErrPGPChallengeIncorrect = errors.New("pgp challenge incorrect")

	ErrOperationNotAllowedOnDM = errors.New("operation not allowed on DM room")
)

// dbtx is the subset of pgxpool.Pool / pgx.Tx that the stores call. It lets a
// Store be backed by either the connection pool or an in-flight transaction
// without code duplication.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Store struct {
	Users   *UserStore
	Rooms   *RoomStore
	Invites *InviteStore
	Chat    *MessageStore

	pool *pgxpool.Pool // nil for tx-scoped stores; used for Ping/WithTx only
}

func New(pool *pgxpool.Pool) *Store {
	return storeFromDB(pool, pool)
}

func storeFromDB(db dbtx, pool *pgxpool.Pool) *Store {
	return &Store{
		Users:   &UserStore{db: db},
		Rooms:   &RoomStore{db: db},
		Invites: &InviteStore{db: db},
		Chat:    &MessageStore{db: db, ilikeEscaper: strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)},
		pool:    pool,
	}
}

func (s *Store) Ping(ctx context.Context) error {
	if s.pool == nil {
		return errPingOnTxStore
	}
	return s.pool.Ping(ctx)
}

// WithTx runs fn inside a transaction. If fn returns an error the transaction
// is rolled back, otherwise it is committed. The Store passed to fn is scoped
// to the transaction — every store call inside it goes through the same tx.
func (s *Store) WithTx(ctx context.Context, fn func(*Store) error) error {
	if s.pool == nil {
		return errWithTxOnTxStore
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := fn(storeFromDB(tx, nil)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UserID is a typed database identifier for a user.
type UserID int64

// Int64 returns the underlying int64 value.
func (id UserID) Int64() int64 { return int64(id) }

// RoomID is a typed database identifier for a room.
type RoomID int64

// Int64 returns the underlying int64 value.
func (id RoomID) Int64() int64 { return int64(id) }

// InviteID is a typed database identifier for an invite.
type InviteID int64

// Int64 returns the underlying int64 value.
func (id InviteID) Int64() int64 { return int64(id) }

// MessageID is a typed database identifier for a message.
type MessageID int64

// Int64 returns the underlying int64 value.
func (id MessageID) Int64() int64 { return int64(id) }

type User struct {
	ID             UserID
	Username       string
	CreatedAt      time.Time
	PasswordHash   string
	SessionVersion int
}

type RoomMember struct {
	ID        UserID
	Username  string
	CreatedAt time.Time

	PGPPublicKey           string
	PGPFingerprint         string
	PGPVerifiedAt          *time.Time
	PGPChallengeCiphertext string
	PGPChallengeExpiresAt  *time.Time
}

type Room struct {
	ID          RoomID
	DisplayName string
	CreatedAt   time.Time
	PGPRequired bool
	IsDM        bool
}

type RoomDetail struct {
	Room

	CreatorID        UserID
	CreatorUsername  string
	MembersCanInvite bool
	PGPRequired      bool
}

type InviteForUser struct {
	InviteID    InviteID
	RoomID      RoomID
	RoomName    string
	InviterID   UserID
	InviterName string
	CreatedAt   time.Time
}

type InviteForRoom struct {
	InviteID    InviteID
	InviterID   UserID
	InviterName string
	InviteeID   UserID
	InviteeName string
	CreatedAt   time.Time
}
