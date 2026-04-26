package service

import (
	"errors"
	"testing"

	"github.com/jchevertonwynne/ssanta/internal/model"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

func TestCreateUser_Validation(t *testing.T) {
	t.Parallel()
	svc := New(nil)

	tests := []struct {
		name     string
		username string
		password string
		wantErr  error
	}{
		{"empty username", "", "password123", store.ErrUsernameInvalid},
		{"too short username", "ab", "password123", store.ErrUsernameInvalid},
		{"invalid chars", "user-name", "password123", store.ErrUsernameInvalid},
		{"short password", "validuser", "1234567", store.ErrPasswordTooShort},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.CreateUser(t.Context(), tt.username, tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCreateRoom_Validation(t *testing.T) {
	t.Parallel()
	svc := New(nil)

	tests := []struct {
		name     string
		dispName string
		wantErr  error
	}{
		{"empty name", "", store.ErrRoomNameEmpty},
		{"too long", string(make([]byte, store.MaxRoomNameLength+1)), store.ErrRoomNameTooLong},
		{"dm prefix", "dm:alice:bob", store.ErrRoomNameReservedPrefix},
		{"dm prefix mixed case", "DM:room", store.ErrRoomNameReservedPrefix},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.CreateRoom(t.Context(), tt.dispName, model.UserID(1))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestGetOrCreateDMRoom_SameUser(t *testing.T) {
	t.Parallel()
	svc := New(nil)
	_, err := svc.GetOrCreateDMRoom(t.Context(), model.UserID(1), model.UserID(1))
	if !errors.Is(err, store.ErrCannotInviteSelf) {
		t.Fatalf("expected ErrCannotInviteSelf, got %v", err)
	}
}
