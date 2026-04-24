// Package service contains application-level business logic.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jchevertonwynne/ssanta/internal/model"
	"github.com/jchevertonwynne/ssanta/internal/pgp"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

const (
	defaultInviteMaxAge        = 24 * time.Hour
	defaultRoomPGPChallengeTTL = 10 * time.Minute
	minimumPasswordLength      = 8
	dmRoomNameParts            = 3
)

// Service coordinates store access and application rules.
type Service struct {
	store               *store.Store
	inviteMaxAge        time.Duration
	roomPGPChallengeTTL time.Duration
	argon2              Argon2Params
}

// New constructs a service backed by the provided store.
func New(store *store.Store) *Service {
	return &Service{
		store:               store,
		inviteMaxAge:        defaultInviteMaxAge,
		roomPGPChallengeTTL: defaultRoomPGPChallengeTTL,
		argon2:              DefaultArgon2Params(),
	}
}

// SetInviteMaxAge updates the maximum age allowed for new invites.
func (s *Service) SetInviteMaxAge(d time.Duration) {
	if d > 0 {
		s.inviteMaxAge = d
	}
}

// Ping checks the database connection health.
func (s *Service) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

// DMRoomInfo contains information about a direct message room.
type DMRoomInfo struct {
	RoomID      model.RoomID `json:"room_id"`
	PartnerID   model.UserID `json:"partner_id"`
	PartnerName string       `json:"partner_name"`
	DisplayName string       `json:"display_name"`
	CreatedAt   time.Time    `json:"created_at"`
}

// ContentView contains all data needed to render the main content page.
type ContentView struct {
	CurrentUsername string
	Users           []model.User
	CreatedRooms    []model.Room
	MemberRooms     []model.Room
	DMRooms         []DMRoomInfo
	Invites         []model.InviteForUser
}

// RoomDetailView contains all data needed to render a room detail page.
type RoomDetailView struct {
	CurrentUsername string
	Room            model.RoomDetail
	IsCreator       bool
	IsMember        bool
	IsDMRoom        bool
	CanInvite       bool
	Members         []model.RoomMember
	PendingInvites  []model.InviteForRoom
	DMPartnerName   string       // non-empty only when IsDMRoom is true
	AllUsers        []model.User // all users in the system, for invite dropdown
}

// GetContentView loads all data needed for the main content page.
func (s *Service) GetContentView(ctx context.Context, userID model.UserID) (*ContentView, error) {
	view := &ContentView{}

	if userID == 0 {
		users, err := s.store.Users.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		view.Users = users
		return view, nil
	}

	var users []model.User
	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		var err error
		users, err = s.store.Users.ListUsers(gCtx)
		return err
	})

	g.Go(func() error {
		user, err := s.store.Users.GetUserByID(gCtx, userID)
		if err != nil {
			return err
		}
		view.CurrentUsername = user.Username
		return nil
	})

	g.Go(func() error {
		var err error
		view.CreatedRooms, err = s.store.Rooms.ListRoomsByCreator(gCtx, userID)
		return err
	})

	g.Go(func() error {
		var err error
		view.MemberRooms, err = s.store.Rooms.ListRoomsByMember(gCtx, userID)
		return err
	})

	g.Go(func() error {
		var err error
		view.Invites, err = s.store.Invites.ListInvitesForUser(gCtx, userID)
		return err
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	view.Users = users

	// getDMRoomsForUser depends on the users list, so run after the errgroup
	dmRooms, err := s.getDMRoomsForUser(ctx, userID, users)
	if err != nil {
		return nil, err
	}
	view.DMRooms = dmRooms

	return view, nil
}

// GetRoomDetailView loads all data needed for a room detail page.
// Fetches user info, room detail, members, and invites concurrently.
// Auth guard checks creator or membership from the fetched members list.
//
//nolint:cyclop,funlen
func (s *Service) GetRoomDetailView(ctx context.Context, roomID model.RoomID, userID model.UserID) (*RoomDetailView, error) {
	var (
		user     model.User
		room     model.RoomDetail
		members  []model.RoomMember
		invites  []model.InviteForRoom
		allUsers []model.User
	)

	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		user, err = s.store.Users.GetUserByID(gCtx, userID)
		return err
	})
	g.Go(func() error {
		var err error
		room, err = s.store.Rooms.GetRoomDetail(gCtx, roomID)
		return err
	})
	g.Go(func() error {
		var err error
		members, err = s.store.Rooms.ListRoomMembersWithPGP(gCtx, roomID)
		return err
	})
	g.Go(func() error {
		var err error
		invites, err = s.store.Invites.ListInvitesForRoom(gCtx, roomID)
		return err
	})
	g.Go(func() error {
		var err error
		allUsers, err = s.store.Users.ListUsers(gCtx)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Check membership from the fetched members list
	isCreator := room.CreatorID == userID
	isMember := false
	for _, m := range members {
		if m.ID == userID {
			isMember = true

			break
		}
	}
	if !isCreator && !isMember {
		return nil, store.ErrNotRoomMember
	}

	isDMRoom := room.IsDM || strings.HasPrefix(room.DisplayName, "dm:")

	var dmPartnerName string
	if isDMRoom {
		for _, m := range members {
			if m.ID != userID {
				dmPartnerName = m.Username

				break
			}
		}
	}

	return &RoomDetailView{
		CurrentUsername: user.Username,
		Room:            room,
		IsCreator:       isCreator,
		IsMember:        isMember,
		IsDMRoom:        isDMRoom,
		CanInvite:       !isDMRoom && (isCreator || (isMember && room.MembersCanInvite)),
		Members:         members,
		PendingInvites:  invites,
		DMPartnerName:   dmPartnerName,
		AllUsers:        allUsers,
	}, nil
}

// ListRoomMembersWithPGP loads room members along with any PGP metadata.
func (s *Service) ListRoomMembersWithPGP(ctx context.Context, roomID model.RoomID) ([]model.RoomMember, error) {
	return s.store.Rooms.ListRoomMembersWithPGP(ctx, roomID)
}

// SetRoomPGPKey stores and challenges a room member's public key.
func (s *Service) SetRoomPGPKey(
	ctx context.Context,
	roomID model.RoomID,
	userID model.UserID,
	armoredPublicKey string,
) error {
	isMember, err := s.store.Rooms.IsRoomMember(ctx, roomID, userID)
	if err != nil {
		return err
	}
	if !isMember {
		return store.ErrNotRoomMember
	}

	now := time.Now()
	normalized, fingerprint, err := pgp.NormalizePublicKey(armoredPublicKey, now)
	if err != nil {
		return err
	}

	challenge, err := pgp.NewChallengeString(0)
	if err != nil {
		return err
	}
	ciphertext, err := pgp.EncryptToPublicKey(normalized, []byte(challenge))
	if err != nil {
		return err
	}

	expiresAt := now.Add(s.roomPGPChallengeTTL)
	hash := pgp.HashChallenge(challenge)

	return s.store.Rooms.UpsertRoomUserPGPKeyWithChallenge(
		ctx,
		roomID,
		userID,
		normalized,
		fingerprint,
		ciphertext,
		hash,
		expiresAt,
	)
}

// VerifyRoomPGPKey verifies a decrypted challenge for a room member.
func (s *Service) VerifyRoomPGPKey(
	ctx context.Context,
	roomID model.RoomID,
	userID model.UserID,
	decryptedChallenge string,
) error {
	plaintext := strings.TrimSpace(decryptedChallenge)
	if plaintext == "" {
		return store.ErrPGPChallengeIncorrect
	}
	return s.store.Rooms.VerifyRoomUserPGPChallenge(ctx, roomID, userID, plaintext, time.Now())
}

// RemoveRoomUserPGPKey clears a member's PGP key.
func (s *Service) RemoveRoomUserPGPKey(ctx context.Context, roomID model.RoomID, targetUserID, actingUserID model.UserID) error {
	if targetUserID != actingUserID {
		isCreator, err := s.store.Rooms.IsRoomCreator(ctx, roomID, actingUserID)
		if err != nil {
			return err
		}

		if !isCreator {
			return store.ErrNotRoomCreator
		}
	}

	return s.store.Rooms.ClearRoomUserPGPKey(ctx, roomID, targetUserID)
}

// User operations

// UserExists reports whether a user exists.
func (s *Service) UserExists(ctx context.Context, id model.UserID) (bool, error) {
	return s.store.Users.UserExists(ctx, id)
}

// GetUserSessionVersion returns the user's current session_version. Session
// cookies must carry a matching value to be considered valid.
func (s *Service) GetUserSessionVersion(ctx context.Context, id model.UserID) (int, error) {
	return s.store.Users.GetUserSessionVersion(ctx, id)
}

// CreateUser creates a new user account.
func (s *Service) CreateUser(ctx context.Context, username, password string) (model.UserID, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.CreateUser")
	defer span.End()

	name := strings.TrimSpace(username)
	span.SetAttributes(attribute.String("username", name))

	if !usernameRE.MatchString(name) {
		return 0, store.ErrUsernameInvalid
	}
	if len(password) < minimumPasswordLength {
		return 0, store.ErrPasswordTooShort
	}
	hash, err := hashPassword(password, s.argon2)
	if err != nil {
		slog.ErrorContext(ctx, "hash password", "err", err)
		return 0, err
	}
	id, err := s.store.Users.CreateUser(ctx, name, hash)
	if err != nil {
		return 0, err
	}
	span.SetAttributes(attribute.Int64("user_id", id.Int64()))
	return id, nil
}

// LoginUser authenticates a user by username and password.
func (s *Service) LoginUser(ctx context.Context, username, password string) (model.UserID, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.LoginUser")
	defer span.End()
	span.SetAttributes(attribute.String("username", username))

	user, err := s.store.Users.GetUserWithPassword(ctx, username)
	if errors.Is(err, store.ErrUserNotFound) {
		// Constant-cost dummy verify so missing-user path takes ~the same time
		// as wrong-password path. Removes a username enumeration oracle.
		_, _ = verifyPassword(password, dummyHashSentinel)
		return 0, store.ErrInvalidCredentials
	}
	if err != nil {
		return 0, fmt.Errorf("lookup user: %w", err)
	}
	ok, err := verifyPassword(password, user.PasswordHash)
	if err != nil {
		return 0, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return 0, store.ErrInvalidCredentials
	}
	span.SetAttributes(attribute.Int64("user_id", user.ID.Int64()))
	return user.ID, nil
}

// DeleteUser removes a user account.
func (s *Service) DeleteUser(ctx context.Context, id model.UserID) error {
	return s.store.Users.DeleteUser(ctx, id)
}

// VerifyPassword checks a user's password without changing it.
func (s *Service) VerifyPassword(ctx context.Context, userID model.UserID, password string) error {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.VerifyPassword")
	defer span.End()
	span.SetAttributes(attribute.Int64("user_id", userID.Int64()))

	user, err := s.store.Users.GetUserWithPasswordByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("lookup user: %w", err)
	}
	ok, err := verifyPassword(password, user.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return store.ErrCurrentPasswordIncorrect
	}
	return nil
}

// ChangePassword updates a user's password after verifying the current one.
func (s *Service) ChangePassword(ctx context.Context, userID model.UserID, currentPassword, newPassword string) error {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.ChangePassword")
	defer span.End()
	span.SetAttributes(attribute.Int64("user_id", userID.Int64()))

	user, err := s.store.Users.GetUserWithPasswordByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("lookup user: %w", err)
	}
	ok, err := verifyPassword(currentPassword, user.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return store.ErrCurrentPasswordIncorrect
	}
	if len(newPassword) < minimumPasswordLength {
		return store.ErrPasswordTooShort
	}
	hash, err := hashPassword(newPassword, s.argon2)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.store.Users.UpdatePasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	// Invalidate all other existing session cookies so a leaked cookie stops
	// working immediately after a password change.
	return s.store.Users.BumpSessionVersion(ctx, userID)
}

// Room operations

// CreateRoom creates a new room for the requesting user.
func (s *Service) CreateRoom(ctx context.Context, displayName string, creatorID model.UserID) (model.RoomID, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.CreateRoom")
	defer span.End()

	name := strings.TrimSpace(displayName)
	span.SetAttributes(
		attribute.String("room_name", name),
		attribute.Int64("creator_id", creatorID.Int64()),
	)

	if name == "" {
		return 0, store.ErrRoomNameEmpty
	}
	if len(name) > store.MaxRoomNameLength {
		return 0, store.ErrRoomNameTooLong
	}
	if strings.HasPrefix(strings.ToLower(name), "dm:") {
		return 0, store.ErrRoomNameReservedPrefix
	}
	roomID, err := s.store.Rooms.CreateRoom(ctx, name, creatorID, false)
	if err == nil {
		slog.InfoContext(ctx, "room created", "room_name", name, "creator_id", creatorID, "room_id", roomID.Int64())
		span.SetAttributes(attribute.Int64("room_id", roomID.Int64()))
	}
	return roomID, err
}

// DeleteRoom removes a room if the creator is allowed to do so.
func (s *Service) DeleteRoom(ctx context.Context, roomID model.RoomID, creatorID model.UserID) error {
	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	return s.store.Rooms.DeleteRoom(ctx, roomID, creatorID)
}

// LeaveRoom removes a user from a room.
func (s *Service) LeaveRoom(ctx context.Context, roomID model.RoomID, userID model.UserID) error {
	return s.store.Rooms.LeaveRoom(ctx, roomID, userID)
}

// JoinRoom adds a user to a room.
func (s *Service) JoinRoom(ctx context.Context, roomID model.RoomID, userID model.UserID) error {
	return s.store.Rooms.JoinRoom(ctx, roomID, userID)
}

// IsRoomCreator reports whether a user created the room.
func (s *Service) IsRoomCreator(ctx context.Context, roomID model.RoomID, userID model.UserID) (bool, error) {
	return s.store.Rooms.IsRoomCreator(ctx, roomID, userID)
}

// GetRoomAccess reports creator and membership state for a room.
func (s *Service) GetRoomAccess(
	ctx context.Context,
	roomID model.RoomID,
	userID model.UserID,
) (bool, bool, error) {
	return s.store.Rooms.GetRoomAccess(ctx, roomID, userID)
}

// SetRoomMembersCanInvite toggles whether room members can invite others.
func (s *Service) SetRoomMembersCanInvite(
	ctx context.Context,
	roomID model.RoomID,
	creatorID model.UserID,
	value bool,
) error {
	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	return s.store.Rooms.SetRoomMembersCanInvite(ctx, roomID, creatorID, value)
}

// SetRoomPGPRequired toggles whether a room requires PGP.
func (s *Service) SetRoomPGPRequired(
	ctx context.Context,
	roomID model.RoomID,
	actingUserID model.UserID,
	value bool,
) error {
	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	return s.store.Rooms.SetRoomPGPRequired(ctx, roomID, actingUserID, value)
}

// RemoveMember removes another user from a room.
func (s *Service) RemoveMember(ctx context.Context, roomID model.RoomID, memberID, creatorID model.UserID) error {
	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	return s.store.Rooms.RemoveMember(ctx, roomID, memberID, creatorID)
}

// Invite operations

// CreateInvite creates an invite for another user.
func (s *Service) CreateInvite(
	ctx context.Context,
	roomID model.RoomID,
	inviterID model.UserID,
	inviteeUsername string,
) error {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.CreateInvite")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("room_id", roomID.Int64()),
		attribute.Int64("inviter_id", inviterID.Int64()),
		attribute.String("invitee_username", inviteeUsername),
	)

	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	expiresAt := time.Now().Add(s.inviteMaxAge)
	err := s.store.Invites.CreateInvite(ctx, roomID, inviterID, inviteeUsername, expiresAt)
	if err == nil {
		slog.InfoContext(ctx, "invite created", "room_id", roomID, "inviter_id", inviterID, "invitee", inviteeUsername)
	}
	return err
}

// AcceptInvite accepts an invite and returns the room ID.
func (s *Service) AcceptInvite(
	ctx context.Context,
	inviteID model.InviteID,
	userID model.UserID,
) (model.RoomID, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.AcceptInvite")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("invite_id", inviteID.Int64()),
		attribute.Int64("user_id", userID.Int64()),
	)

	roomID, err := s.store.Invites.AcceptInvite(ctx, inviteID, userID)
	if err == nil {
		span.SetAttributes(attribute.Int64("room_id", roomID.Int64()))
		slog.InfoContext(
			ctx,
			"invite accepted",
			"invite_id",
			inviteID,
			"user_id",
			userID,
			"room_id",
			roomID,
		)
	}
	return roomID, err
}

// DeclineInvite declines an invite.
func (s *Service) DeclineInvite(ctx context.Context, inviteID model.InviteID, userID model.UserID) error {
	return s.store.Invites.DeclineInvite(ctx, inviteID, userID)
}

// CancelInvite cancels an invite and returns the affected room and user IDs.
func (s *Service) CancelInvite(
	ctx context.Context,
	inviteID model.InviteID,
	actingUserID model.UserID,
) (model.RoomID, model.UserID, error) {
	return s.store.Invites.CancelInvite(ctx, inviteID, actingUserID)
}

// RoomIDForInvite resolves the room associated with an invite.
func (s *Service) RoomIDForInvite(ctx context.Context, inviteID model.InviteID) (model.RoomID, error) {
	return s.store.Invites.RoomIDForInvite(ctx, inviteID)
}

// Helper operations

// IsRoomMember reports whether a user belongs to a room.
func (s *Service) IsRoomMember(
	ctx context.Context,
	roomID model.RoomID,
	userID model.UserID,
) (bool, error) {
	return s.store.Rooms.IsRoomMember(ctx, roomID, userID)
}

// IsRoomPGPRequired reports whether a room requires PGP.
func (s *Service) IsRoomPGPRequired(ctx context.Context, roomID model.RoomID) (bool, error) {
	return s.store.Rooms.IsRoomPGPRequired(ctx, roomID)
}

// GetUsername resolves a user ID to a username.
func (s *Service) GetUsername(ctx context.Context, userID model.UserID) (string, error) {
	user, err := s.store.Users.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return user.Username, nil
}

// GetUserByUsername looks up a user by username.
func (s *Service) GetUserByUsername(ctx context.Context, username string) (model.User, error) {
	return s.store.Users.GetUserByUsername(ctx, username)
}

// Direct Messages

// GetOrCreateDMRoom gets or creates a DM room between two users.
func (s *Service) GetOrCreateDMRoom(
	ctx context.Context,
	user1ID, user2ID model.UserID,
) (model.RoomID, error) {
	if user1ID == user2ID {
		return 0, store.ErrCannotInviteSelf
	}

	// Get usernames
	user1, err := s.store.Users.GetUserByID(ctx, user1ID)
	if err != nil {
		return 0, err
	}
	user2, err := s.store.Users.GetUserByID(ctx, user2ID)
	if err != nil {
		return 0, err
	}

	// Generate deterministic DM room name (alphabetically sorted)
	name1, name2 := user1.Username, user2.Username
	if name1 > name2 {
		name1, name2 = name2, name1
	}
	displayName := "dm:" + name1 + ":" + name2

	roomID, err := s.store.Rooms.GetOrCreateDMRoom(ctx, displayName, user1ID)
	if err != nil {
		return 0, err
	}

	// Ensure BOTH participants are members.
	// This makes DMs usable immediately and also allows re-joining after leaving.
	if err := s.store.Rooms.JoinRoom(ctx, roomID, user1ID); err != nil {
		return 0, err
	}
	if err := s.store.Rooms.JoinRoom(ctx, roomID, user2ID); err != nil {
		return 0, err
	}

	return roomID, nil
}

// getDMRoomsForUser returns a list of DM rooms for a user with partner info.
//
//nolint:cyclop,nestif,funcorder
func (s *Service) getDMRoomsForUser(ctx context.Context, userID model.UserID, users []model.User) ([]DMRoomInfo, error) {
	memberRooms, err := s.store.Rooms.ListDMRoomsByMember(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Build a map of username -> user for quick lookup
	userByName := make(map[string]model.User)
	for _, u := range users {
		userByName[u.Username] = u
	}

	var dmRooms []DMRoomInfo

	for _, room := range memberRooms {
		// Extract partner username from DM name format "dm:user1:user2"
		parts := strings.Split(room.DisplayName, ":")
		if len(parts) != dmRoomNameParts {
			continue
		}

		partnerName := parts[1]
		if partnerName == "" {
			partnerName = parts[2]
		} else if parts[2] != "" {
			// Pick the one that's not the current user
			if user, ok := userByName[parts[1]]; ok {
				if user.ID != userID {
					partnerName = parts[1]
				} else if user2, ok2 := userByName[parts[2]]; ok2 {
					partnerName = parts[2]
					if user2.ID == userID {
						continue
					}
				}
			}
		}

		// Find the partner in the user list
		var partnerID model.UserID
		if partner, ok := userByName[partnerName]; ok {
			partnerID = partner.ID
		} else {
			continue
		}

		dmRooms = append(dmRooms, DMRoomInfo{
			RoomID:      room.ID,
			PartnerID:   partnerID,
			PartnerName: partnerName,
			DisplayName: room.DisplayName,
			CreatedAt:   room.CreatedAt,
		})
	}

	// Sort by created_at DESC (newest first)
	sort.Slice(dmRooms, func(i, j int) bool {
		return dmRooms[i].CreatedAt.After(dmRooms[j].CreatedAt)
	})

	return dmRooms, nil
}

// Message operations

// CreateMessage stores a new room message.
func (s *Service) CreateMessage(
	ctx context.Context,
	roomID model.RoomID,
	userID model.UserID,
	username, message string,
	whisper bool,
	targetUserID *model.UserID,
	preEncrypted bool,
) (model.MessageID, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.CreateMessage")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("room_id", roomID.Int64()),
		attribute.Int64("user_id", userID.Int64()),
	)
	return s.store.Chat.CreateMessage(ctx, roomID, userID, username, message, whisper, targetUserID, preEncrypted)
}

// ListMessagesAfterID returns messages newer than afterID for a room.
func (s *Service) ListMessagesAfterID(
	ctx context.Context,
	roomID model.RoomID,
	userID model.UserID,
	afterID model.MessageID,
	limit int,
) ([]model.Message, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.ListMessagesAfterID")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("room_id", roomID.Int64()),
		attribute.Int64("user_id", userID.Int64()),
		attribute.Int64("after_id", afterID.Int64()),
	)
	return s.store.Chat.ListMessagesAfterID(ctx, roomID, userID, afterID, limit)
}

// ListMessages returns messages for a room.
func (s *Service) ListMessages(
	ctx context.Context,
	roomID model.RoomID,
	userID model.UserID,
	beforeID model.MessageID,
	limit int,
) ([]model.Message, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.ListMessages")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("room_id", roomID.Int64()),
		attribute.Int64("user_id", userID.Int64()),
	)
	return s.store.Chat.ListMessages(ctx, roomID, userID, beforeID, limit)
}

// SearchMessages searches messages in a room.
func (s *Service) SearchMessages(
	ctx context.Context,
	roomID model.RoomID,
	userID model.UserID,
	query string,
	limit int,
) ([]model.Message, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.SearchMessages")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("room_id", roomID.Int64()),
		attribute.Int64("user_id", userID.Int64()),
		attribute.String("query", query),
	)
	return s.store.Chat.SearchMessages(ctx, roomID, userID, query, limit)
}

func (s *Service) assertNotDM(ctx context.Context, roomID model.RoomID) error {
	detail, err := s.store.Rooms.GetRoomDetail(ctx, roomID)
	if err != nil {
		return err
	}
	if detail.IsDM {
		return store.ErrOperationNotAllowedOnDM
	}
	return nil
}
