// Package model defines the core domain types shared across application layers.
package model

import "time"

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
	ID        UserID
	Username  string
	CreatedAt time.Time
}

type UserWithPassword struct {
	User

	PasswordHash string
}

type AdminUser struct {
	User

	IsAdmin                bool
	AdminSince             *time.Time
	AdminGrantedByUsername *string
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

type Message struct {
	ID           MessageID
	RoomID       RoomID
	UserID       UserID
	Username     string
	Message      string
	CreatedAt    time.Time
	Whisper      bool
	TargetUserID *UserID
	PreEncrypted bool
	EditedAt     *time.Time
	DeletedAt    *time.Time
}
