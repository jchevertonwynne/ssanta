package service

import (
	"testing"

	"github.com/jchevertonwynne/ssanta/internal/model"
)

// password helpers

func TestHashPassword_ProducesVerifiableHash(t *testing.T) {
	t.Parallel()
	hash, err := hashPassword("hunter2", DefaultArgon2Params())
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	ok, err := verifyPassword("hunter2", hash)
	if err != nil {
		t.Fatalf("verifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("correct password should verify true")
	}
}

func TestVerifyPassword_WrongPassword_ReturnsFalse(t *testing.T) {
	t.Parallel()
	hash, err := hashPassword("correct", DefaultArgon2Params())
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	ok, err := verifyPassword("wrong", hash)
	if err != nil {
		t.Fatalf("verifyPassword: %v", err)
	}
	if ok {
		t.Fatal("wrong password should not verify")
	}
}

func TestVerifyPassword_InvalidFormat_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := verifyPassword("password", "not-a-hash")
	if err == nil {
		t.Fatal("expected error for invalid hash format")
	}
}

func TestDummyHashSentinel_IsVerifiable(t *testing.T) {
	t.Parallel()
	// Ensure the sentinel hash is well-formed — verifyPassword must not return an error
	// (it may return false for any given input, which is expected).
	_, err := verifyPassword("anything", dummyHashSentinel)
	if err != nil {
		t.Fatalf("dummyHashSentinel is not verifiable: %v", err)
	}
}

// resolveDMPartner

func makeUserMap(users ...model.User) map[string]model.User {
	m := make(map[string]model.User, len(users))
	for _, u := range users {
		m[u.Username] = u
	}
	return m
}

func TestResolveDMPartner_AliceIsUser1_BobIsPartner(t *testing.T) {
	t.Parallel()
	alice := model.User{ID: 1, Username: "alice"}
	bob := model.User{ID: 2, Username: "bob"}
	userByName := makeUserMap(alice, bob)

	name, id, ok := resolveDMPartner("dm:alice:bob", alice.ID, userByName)
	if !ok {
		t.Fatal("expected ok")
	}
	if name != "bob" || id != bob.ID {
		t.Fatalf("expected bob/%d, got %s/%d", bob.ID, name, id)
	}
}

func TestResolveDMPartner_BobIsUser1_AliceIsPartner(t *testing.T) {
	t.Parallel()
	alice := model.User{ID: 1, Username: "alice"}
	bob := model.User{ID: 2, Username: "bob"}
	userByName := makeUserMap(alice, bob)

	name, id, ok := resolveDMPartner("dm:alice:bob", bob.ID, userByName)
	if !ok {
		t.Fatal("expected ok")
	}
	if name != "alice" || id != alice.ID {
		t.Fatalf("expected alice/%d, got %s/%d", alice.ID, name, id)
	}
}

func TestResolveDMPartner_MalformedName_ReturnsFalse(t *testing.T) {
	t.Parallel()
	userByName := makeUserMap(model.User{ID: 1, Username: "alice"})
	_, _, ok := resolveDMPartner("not-a-dm", 1, userByName)
	if ok {
		t.Fatal("expected not ok for malformed name")
	}
}

func TestResolveDMPartner_BothUsersUnknown_ReturnsFalse(t *testing.T) {
	t.Parallel()
	// Neither user in the room name exists in the map (e.g. both were deleted).
	userByName := makeUserMap()

	_, _, ok := resolveDMPartner("dm:ghost1:ghost2", model.UserID(1), userByName)
	if ok {
		t.Fatal("expected not ok when both users are absent from the map")
	}
}

func TestResolveDMPartner_EmptyRooms_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	userByName := makeUserMap()
	_, _, ok := resolveDMPartner("dm:alice:bob", 1, userByName)
	if ok {
		t.Fatal("expected not ok for empty user map")
	}
}
