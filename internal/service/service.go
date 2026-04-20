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

	"github.com/jchevertonwynne/ssanta/internal/pgp"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9]{3,32}$`)

type Service struct {
	store               *store.Store
	inviteMaxAge        time.Duration
	roomPGPChallengeTTL time.Duration
	argon2              Argon2Params
}

func New(store *store.Store) *Service {
	return &Service{
		store:               store,
		inviteMaxAge:        24 * time.Hour,
		roomPGPChallengeTTL: 10 * time.Minute,
		argon2:              DefaultArgon2Params(),
	}
}

func (s *Service) SetInviteMaxAge(d time.Duration) {
	if d > 0 {
		s.inviteMaxAge = d
	}
}

func (s *Service) SetRoomPGPChallengeTTL(d time.Duration) {
	if d > 0 {
		s.roomPGPChallengeTTL = d
	}
}

func (s *Service) SetArgon2Params(p Argon2Params) {
	if p.Memory > 0 && p.Iterations > 0 && p.Parallelism > 0 {
		s.argon2 = p
	}
}

// Ping checks the database connection health
func (s *Service) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

// DMRoomInfo contains info about a direct message room
type DMRoomInfo struct {
	RoomID      store.RoomID
	PartnerID   store.UserID
	PartnerName string
	DisplayName string
	CreatedAt   time.Time
}

// ContentView contains all data needed to render the main content page
type ContentView struct {
	CurrentUsername string
	Users           []store.User
	CreatedRooms    []store.Room
	MemberRooms     []store.Room
	DMRooms         []DMRoomInfo
	Invites         []store.InviteForUser
}

// RoomDetailView contains all data needed to render a room detail page
type RoomDetailView struct {
	CurrentUsername string
	Room            store.RoomDetail
	IsCreator       bool
	IsMember        bool
	CanInvite       bool
	Members         []store.RoomMember
	PendingInvites  []store.InviteForRoom
	DMPartnerName   string // non-empty only when Room.IsDM is true
}

// GetContentView loads all data needed for the main content page
func (s *Service) GetContentView(ctx context.Context, userID store.UserID) (*ContentView, error) {
	view := &ContentView{}

	users, err := s.store.Users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	view.Users = users

	if userID != 0 {
		user, err := s.store.Users.GetUserByID(ctx, userID)
		if err != nil {
			return nil, err
		}
		view.CurrentUsername = user.Username

		view.CreatedRooms, err = s.store.Rooms.ListRoomsByCreator(ctx, userID)
		if err != nil {
			return nil, err
		}

		view.MemberRooms, err = s.store.Rooms.ListRoomsByMember(ctx, userID)
		if err != nil {
			return nil, err
		}

		view.Invites, err = s.store.Invites.ListInvitesForUser(ctx, userID)
		if err != nil {
			return nil, err
		}

		// Load DM rooms
		dmRooms, err := s.getDMRoomsForUser(ctx, userID, users)
		if err != nil {
			return nil, err
		}
		view.DMRooms = dmRooms
	}

	return view, nil
}

// GetRoomDetailView loads all data needed for a room detail page.
// Phase 1 fetches user info, room detail, and membership concurrently.
// Phase 2 fetches members and invites concurrently, but only after the auth guard passes.
func (s *Service) GetRoomDetailView(ctx context.Context, roomID store.RoomID, userID store.UserID) (*RoomDetailView, error) {
	var (
		user     store.User
		room     store.RoomDetail
		isMember bool
	)

	g1, gCtx1 := errgroup.WithContext(ctx)
	g1.Go(func() error {
		var err error
		user, err = s.store.Users.GetUserByID(gCtx1, userID)
		return err
	})
	g1.Go(func() error {
		var err error
		room, err = s.store.Rooms.GetRoomDetail(gCtx1, roomID)
		return err
	})
	g1.Go(func() error {
		var err error
		isMember, err = s.store.Rooms.IsRoomMember(gCtx1, roomID, userID)
		return err
	})
	if err := g1.Wait(); err != nil {
		return nil, err
	}

	isCreator := room.CreatorID == userID
	if !isCreator && !isMember {
		return nil, store.ErrNotRoomMember
	}

	var members []store.RoomMember
	var invites []store.InviteForRoom
	g2, gCtx2 := errgroup.WithContext(ctx)
	g2.Go(func() error {
		var err error
		members, err = s.store.Rooms.ListRoomMembersWithPGP(gCtx2, roomID)
		return err
	})
	g2.Go(func() error {
		var err error
		invites, err = s.store.Invites.ListInvitesForRoom(gCtx2, roomID)
		return err
	})
	if err := g2.Wait(); err != nil {
		return nil, err
	}

	var dmPartnerName string
	if room.IsDM {
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
		CanInvite:       isCreator || (isMember && room.MembersCanInvite),
		Members:         members,
		PendingInvites:  invites,
		DMPartnerName:   dmPartnerName,
	}, nil
}

func (s *Service) ListRoomMembersWithPGP(ctx context.Context, roomID store.RoomID) ([]store.RoomMember, error) {
	return s.store.Rooms.ListRoomMembersWithPGP(ctx, roomID)
}

func (s *Service) SetRoomPGPKey(ctx context.Context, roomID store.RoomID, userID store.UserID, armoredPublicKey string) error {
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

	return s.store.Rooms.UpsertRoomUserPGPKeyWithChallenge(ctx, roomID, userID, normalized, fingerprint, ciphertext, hash, expiresAt)
}

func (s *Service) VerifyRoomPGPKey(ctx context.Context, roomID store.RoomID, userID store.UserID, decryptedChallenge string) error {
	plaintext := strings.TrimSpace(decryptedChallenge)
	if plaintext == "" {
		return store.ErrPGPChallengeIncorrect
	}
	return s.store.Rooms.VerifyRoomUserPGPChallenge(ctx, roomID, userID, plaintext, time.Now())
}

func (s *Service) RemoveRoomUserPGPKey(ctx context.Context, roomID store.RoomID, targetUserID, actingUserID store.UserID) error {
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

func (s *Service) UserExists(ctx context.Context, id store.UserID) (bool, error) {
	return s.store.Users.UserExists(ctx, id)
}

func (s *Service) CreateUser(ctx context.Context, username, password string) (store.UserID, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.CreateUser")
	defer span.End()

	name := strings.TrimSpace(username)
	span.SetAttributes(attribute.String("username", name))

	if !usernameRE.MatchString(name) {
		return 0, store.ErrUsernameInvalid
	}
	if len(password) < 8 {
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

func (s *Service) LoginUser(ctx context.Context, username, password string) (store.UserID, error) {
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

func (s *Service) DeleteUser(ctx context.Context, id store.UserID) error {
	return s.store.Users.DeleteUser(ctx, id)
}

func (s *Service) ChangePassword(ctx context.Context, userID store.UserID, currentPassword, newPassword string) error {
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
	if len(newPassword) < 8 {
		return store.ErrPasswordTooShort
	}
	hash, err := hashPassword(newPassword, s.argon2)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.store.Users.UpdatePasswordHash(ctx, userID, hash)
}

// Room operations

func (s *Service) CreateRoom(ctx context.Context, displayName string, creatorID store.UserID) (store.RoomID, error) {
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

func (s *Service) assertNotDM(ctx context.Context, roomID store.RoomID) error {
	detail, err := s.store.Rooms.GetRoomDetail(ctx, roomID)
	if err != nil {
		return err
	}
	if detail.IsDM {
		return store.ErrOperationNotAllowedOnDM
	}
	return nil
}

func (s *Service) DeleteRoom(ctx context.Context, roomID store.RoomID, creatorID store.UserID) error {
	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	return s.store.Rooms.DeleteRoom(ctx, roomID, creatorID)
}

func (s *Service) LeaveRoom(ctx context.Context, roomID store.RoomID, userID store.UserID) error {
	return s.store.Rooms.LeaveRoom(ctx, roomID, userID)
}

func (s *Service) JoinRoom(ctx context.Context, roomID store.RoomID, userID store.UserID) error {
	return s.store.Rooms.JoinRoom(ctx, roomID, userID)
}

func (s *Service) IsRoomCreator(ctx context.Context, roomID store.RoomID, userID store.UserID) (bool, error) {
	return s.store.Rooms.IsRoomCreator(ctx, roomID, userID)
}

func (s *Service) GetRoomAccess(ctx context.Context, roomID store.RoomID, userID store.UserID) (isCreator bool, isMember bool, err error) {
	return s.store.Rooms.GetRoomAccess(ctx, roomID, userID)
}

func (s *Service) SetRoomMembersCanInvite(ctx context.Context, roomID store.RoomID, creatorID store.UserID, value bool) error {
	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	return s.store.Rooms.SetRoomMembersCanInvite(ctx, roomID, creatorID, value)
}

func (s *Service) SetRoomPGPRequired(ctx context.Context, roomID store.RoomID, creatorID store.UserID, value bool) error {
	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	return s.store.Rooms.SetRoomPGPRequired(ctx, roomID, creatorID, value)
}

func (s *Service) RemoveMember(ctx context.Context, roomID store.RoomID, memberID, creatorID store.UserID) error {
	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	return s.store.Rooms.RemoveMember(ctx, roomID, memberID, creatorID)
}

// Invite operations

func (s *Service) CreateInvite(ctx context.Context, roomID store.RoomID, inviterID store.UserID, inviteeUsername string) error {
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

func (s *Service) AcceptInvite(ctx context.Context, inviteID store.InviteID, userID store.UserID) (store.RoomID, error) {
	ctx, span := otel.Tracer("ssanta").Start(ctx, "Service.AcceptInvite")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("invite_id", inviteID.Int64()),
		attribute.Int64("user_id", userID.Int64()),
	)

	roomID, err := s.store.Invites.AcceptInvite(ctx, inviteID, userID)
	if err == nil {
		span.SetAttributes(attribute.Int64("room_id", roomID.Int64()))
		slog.InfoContext(ctx, "invite accepted", "invite_id", inviteID, "user_id", userID, "room_id", roomID)
	}
	return roomID, err
}

func (s *Service) DeclineInvite(ctx context.Context, inviteID store.InviteID, userID store.UserID) error {
	return s.store.Invites.DeclineInvite(ctx, inviteID, userID)
}

func (s *Service) CancelInvite(ctx context.Context, inviteID store.InviteID, actingUserID store.UserID) (store.RoomID, store.UserID, error) {
	return s.store.Invites.CancelInvite(ctx, inviteID, actingUserID)
}

func (s *Service) RoomIDForInvite(ctx context.Context, inviteID store.InviteID) (store.RoomID, error) {
	return s.store.Invites.RoomIDForInvite(ctx, inviteID)
}

// Helper operations

func (s *Service) IsRoomMember(ctx context.Context, roomID store.RoomID, userID store.UserID) (bool, error) {
	return s.store.Rooms.IsRoomMember(ctx, roomID, userID)
}

func (s *Service) GetUsername(ctx context.Context, userID store.UserID) (string, error) {
	user, err := s.store.Users.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return user.Username, nil
}

func (s *Service) GetUserByUsername(ctx context.Context, username string) (store.User, error) {
	return s.store.Users.GetUserByUsername(ctx, username)
}

// Room Discovery

func (s *Service) ListPublicRooms(ctx context.Context, limit int, offset int) ([]store.Room, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.Rooms.ListPublicRooms(ctx, limit, offset)
}

func (s *Service) SearchPublicRooms(ctx context.Context, query string, limit int) ([]store.Room, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []store.Room{}, nil
	}
	if len(query) > 100 {
		query = query[:100]
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.Rooms.SearchPublicRooms(ctx, query, limit)
}

func (s *Service) SetRoomPublic(ctx context.Context, roomID store.RoomID, creatorID store.UserID, isPublic bool) error {
	if err := s.assertNotDM(ctx, roomID); err != nil {
		return err
	}
	return s.store.Rooms.SetRoomPublic(ctx, roomID, creatorID, isPublic)
}

// Direct Messages

// GetOrCreateDMRoom gets or creates a DM room between two users
func (s *Service) GetOrCreateDMRoom(ctx context.Context, user1ID, user2ID store.UserID) (store.RoomID, error) {
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

	// DM rooms are identified by the reserved displayName prefix. We treat them as
	// special rooms and ensure both users are members, even if one previously left.
	room, err := s.store.Rooms.GetRoomByDisplayName(ctx, displayName)
	if err != nil && !errors.Is(err, store.ErrRoomNotFound) {
		return 0, err
	}

	roomID := room.ID
	if errors.Is(err, store.ErrRoomNotFound) {
		// Create new DM room (user1 is creator)
		roomID, err = s.store.Rooms.CreateRoom(ctx, displayName, user1ID, true)
		if err != nil {
			return 0, err
		}
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

// GetDMPartnerUsername gets the partner's username from a DM room
func (s *Service) GetDMPartnerUsername(ctx context.Context, roomID store.RoomID, currentUserID store.UserID) (string, error) {
	members, err := s.store.Rooms.ListRoomMembersWithPGP(ctx, roomID)
	if err != nil {
		return "", err
	}

	for _, member := range members {
		if member.ID != currentUserID {
			return member.Username, nil
		}
	}

	return "", errors.New("partner not found in DM room")
}

// getDMRoomsForUser returns a list of DM rooms for a user with partner info
func (s *Service) getDMRoomsForUser(ctx context.Context, userID store.UserID, users []store.User) ([]DMRoomInfo, error) {
	memberRooms, err := s.store.Rooms.ListDMRoomsByMember(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Build a map of username -> user for quick lookup
	userByName := make(map[string]store.User)
	for _, u := range users {
		userByName[u.Username] = u
	}

	var dmRooms []DMRoomInfo

	for _, room := range memberRooms {
		// Extract partner username from DM name format "dm:user1:user2"
		parts := strings.Split(room.DisplayName, ":")
		if len(parts) != 3 {
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
		var partnerID store.UserID
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
