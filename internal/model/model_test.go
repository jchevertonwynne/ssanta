package model

import (
	"strconv"
	"testing"
)

func TestUserID_String(t *testing.T) {
	t.Parallel()
	id := UserID(42)
	if got := id.String(); got != "user_id:42" {
		t.Fatalf("expected user_id:42, got %q", got)
	}
}

func TestUserID_Int64(t *testing.T) {
	t.Parallel()
	id := UserID(42)
	if got := id.Int64(); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestParseUserID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    UserID
		wantErr bool
	}{
		{"valid", "user_id:42", 42, false},
		{"zero is invalid", "user_id:0", 0, true},
		{"negative is invalid", "user_id:-1", 0, true},
		{"valid max", "user_id:" + strconv.FormatInt(int64(^uint64(0)>>1), 10), UserID(^uint64(0) >> 1), false},
		{"missing prefix", "42", 42, false},
		{"no prefix zero", "0", 0, true},
		{"no prefix negative", "-1", 0, true},
		{"empty", "", 0, true},
		{"invalid number", "user_id:abc", 0, true},
		{"prefix only", "user_id:", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseUserID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseUserID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseUserID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestRoomID_String(t *testing.T) {
	t.Parallel()
	id := RoomID(7)
	if got := id.String(); got != "room_id:7" {
		t.Fatalf("expected room_id:7, got %q", got)
	}
}

func TestRoomID_Int64(t *testing.T) {
	t.Parallel()
	id := RoomID(7)
	if got := id.Int64(); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
}

//nolint:dupl
func TestParseRoomID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    RoomID
		wantErr bool
	}{
		{"valid", "room_id:7", 7, false},
		{"zero is invalid", "room_id:0", 0, true},
		{"negative is invalid", "room_id:-1", 0, true},
		{"missing prefix", "7", 7, false},
		{"no prefix zero", "0", 0, true},
		{"no prefix negative", "-1", 0, true},
		{"empty", "", 0, true},
		{"invalid", "room_id:xyz", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRoomID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseRoomID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseRoomID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestInviteID_String(t *testing.T) {
	t.Parallel()
	id := InviteID(99)
	if got := id.String(); got != "invite_id:99" {
		t.Fatalf("expected invite_id:99, got %q", got)
	}
}

func TestInviteID_Int64(t *testing.T) {
	t.Parallel()
	id := InviteID(99)
	if got := id.Int64(); got != 99 {
		t.Fatalf("expected 99, got %d", got)
	}
}

//nolint:dupl
func TestParseInviteID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    InviteID
		wantErr bool
	}{
		{"valid", "invite_id:99", 99, false},
		{"zero is invalid", "invite_id:0", 0, true},
		{"negative is invalid", "invite_id:-1", 0, true},
		{"missing prefix", "99", 99, false},
		{"no prefix zero", "0", 0, true},
		{"no prefix negative", "-1", 0, true},
		{"empty", "", 0, true},
		{"invalid", "invite_id:abc", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseInviteID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseInviteID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseInviteID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestMessageID_String(t *testing.T) {
	t.Parallel()
	id := MessageID(1001)
	if got := id.String(); got != "message_id:1001" {
		t.Fatalf("expected message_id:1001, got %q", got)
	}
}

func TestMessageID_Int64(t *testing.T) {
	t.Parallel()
	id := MessageID(1001)
	if got := id.Int64(); got != 1001 {
		t.Fatalf("expected 1001, got %d", got)
	}
}

//nolint:dupl
func TestParseMessageID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    MessageID
		wantErr bool
	}{
		{"valid", "message_id:1001", 1001, false},
		{"zero is invalid", "message_id:0", 0, true},
		{"negative is invalid", "message_id:-1", 0, true},
		{"missing prefix", "1001", 1001, false},
		{"no prefix zero", "0", 0, true},
		{"no prefix negative", "-1", 0, true},
		{"empty", "", 0, true},
		{"invalid", "message_id:abc", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseMessageID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseMessageID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseMessageID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
