package service

import (
	"context"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jchevertonwynne/ssanta/internal/pgp"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9]{3,32}$`)

type Service struct {
	store        *store.Store
	inviteMaxAge time.Duration
}

func New(store *store.Store) *Service {
	return &Service{store: store, inviteMaxAge: 24 * time.Hour}
}

func (s *Service) SetInviteMaxAge(d time.Duration) {
	if d > 0 {
		s.inviteMaxAge = d
	}
}

// Ping checks the database connection health
func (s *Service) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

// ContentView contains all data needed to render the main content page
type ContentView struct {
	CurrentUsername string
	Users           []store.User
	CreatedRooms    []store.Room
	MemberRooms     []store.Room
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
}

// GetContentView loads all data needed for the main content page
func (s *Service) GetContentView(ctx context.Context, userID int64) (*ContentView, error) {
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
	}

	return view, nil
}

// GetRoomDetailView loads all data needed for a room detail page.
// Phase 1 fetches user info, room detail, and membership concurrently.
// Phase 2 fetches members and invites concurrently, but only after the auth guard passes.
func (s *Service) GetRoomDetailView(ctx context.Context, roomID, userID int64) (*RoomDetailView, error) {
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

	return &RoomDetailView{
		CurrentUsername: user.Username,
		Room:            room,
		IsCreator:       isCreator,
		IsMember:        isMember,
		CanInvite:       isCreator || (isMember && room.MembersCanInvite),
		Members:         members,
		PendingInvites:  invites,
	}, nil
}

func (s *Service) ListRoomMembersWithPGP(ctx context.Context, roomID int64) ([]store.RoomMember, error) {
	return s.store.Rooms.ListRoomMembersWithPGP(ctx, roomID)
}

const roomPGPChallengeTTL = 10 * time.Minute

func (s *Service) SetRoomPGPKey(ctx context.Context, roomID, userID int64, armoredPublicKey string) error {
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

	expiresAt := now.Add(roomPGPChallengeTTL)
	hash := pgp.HashChallenge(challenge)

	return s.store.Rooms.UpsertRoomUserPGPKeyWithChallenge(ctx, roomID, userID, normalized, fingerprint, ciphertext, hash, expiresAt)
}

func (s *Service) VerifyRoomPGPKey(ctx context.Context, roomID, userID int64, decryptedChallenge string) error {
	plaintext := strings.TrimSpace(decryptedChallenge)
	if plaintext == "" {
		return store.ErrPGPChallengeIncorrect
	}
	return s.store.Rooms.VerifyRoomUserPGPChallenge(ctx, roomID, userID, plaintext, time.Now())
}

func (s *Service) RemoveRoomUserPGPKey(ctx context.Context, roomID, targetUserID, actingUserID int64) error {
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

func (s *Service) UserExists(ctx context.Context, id int64) (bool, error) {
	return s.store.Users.UserExists(ctx, id)
}

func (s *Service) CreateUser(ctx context.Context, username, password string) (int64, error) {
	name := strings.TrimSpace(username)
	if !usernameRE.MatchString(name) {
		return 0, store.ErrUsernameInvalid
	}
	if len(password) < 8 {
		return 0, store.ErrPasswordTooShort
	}
	hash, err := hashPassword(password)
	if err != nil {
		return 0, err
	}
	return s.store.Users.CreateUser(ctx, name, hash)
}

func (s *Service) LoginUser(ctx context.Context, username, password string) (int64, error) {
	user, err := s.store.Users.GetUserWithPassword(ctx, username)
	if err != nil {
		return 0, store.ErrInvalidCredentials
	}
	ok, err := verifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		return 0, store.ErrInvalidCredentials
	}
	return user.ID, nil
}

func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	return s.store.Users.DeleteUser(ctx, id)
}

// Room operations

func (s *Service) CreateRoom(ctx context.Context, displayName string, creatorID int64) error {
	name := strings.TrimSpace(displayName)
	if name == "" {
		return store.ErrRoomNameEmpty
	}
	if len(name) > store.MaxRoomNameLength {
		return store.ErrRoomNameTooLong
	}
	return s.store.Rooms.CreateRoom(ctx, name, creatorID)
}

func (s *Service) DeleteRoom(ctx context.Context, roomID, creatorID int64) error {
	return s.store.Rooms.DeleteRoom(ctx, roomID, creatorID)
}

func (s *Service) LeaveRoom(ctx context.Context, roomID, userID int64) error {
	return s.store.Rooms.LeaveRoom(ctx, roomID, userID)
}

func (s *Service) JoinRoom(ctx context.Context, roomID, userID int64) error {
	return s.store.Rooms.JoinRoom(ctx, roomID, userID)
}

func (s *Service) IsRoomCreator(ctx context.Context, roomID, userID int64) (bool, error) {
	return s.store.Rooms.IsRoomCreator(ctx, roomID, userID)
}

func (s *Service) GetRoomAccess(ctx context.Context, roomID, userID int64) (isCreator bool, isMember bool, err error) {
	return s.store.Rooms.GetRoomAccess(ctx, roomID, userID)
}

func (s *Service) SetRoomMembersCanInvite(ctx context.Context, roomID, creatorID int64, value bool) error {
	return s.store.Rooms.SetRoomMembersCanInvite(ctx, roomID, creatorID, value)
}

func (s *Service) RemoveMember(ctx context.Context, roomID, memberID, creatorID int64) error {
	return s.store.Rooms.RemoveMember(ctx, roomID, memberID, creatorID)
}

// Invite operations

func (s *Service) CreateInvite(ctx context.Context, roomID, inviterID int64, inviteeUsername string) error {
	expiresAt := time.Now().Add(s.inviteMaxAge)
	return s.store.Invites.CreateInvite(ctx, roomID, inviterID, inviteeUsername, expiresAt)
}

func (s *Service) AcceptInvite(ctx context.Context, inviteID, userID int64) error {
	return s.store.Invites.AcceptInvite(ctx, inviteID, userID)
}

func (s *Service) DeclineInvite(ctx context.Context, inviteID, userID int64) error {
	return s.store.Invites.DeclineInvite(ctx, inviteID, userID)
}

func (s *Service) CancelInvite(ctx context.Context, inviteID, actingUserID int64) (int64, int64, error) {
	return s.store.Invites.CancelInvite(ctx, inviteID, actingUserID)
}

func (s *Service) RoomIDForInvite(ctx context.Context, inviteID int64) (int64, error) {
	return s.store.Invites.RoomIDForInvite(ctx, inviteID)
}

// Helper operations

func (s *Service) IsRoomMember(ctx context.Context, roomID, userID int64) (bool, error) {
	return s.store.Rooms.IsRoomMember(ctx, roomID, userID)
}

func (s *Service) GetUsername(ctx context.Context, userID int64) (string, error) {
	user, err := s.store.Users.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return user.Username, nil
}

func (s *Service) GetUserByUsername(ctx context.Context, username string) (store.User, error) {
	return s.store.Users.GetUserByUsername(ctx, username)
}
