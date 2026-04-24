package store

import (
	"testing"
	"time"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/stretchr/testify/require"

	"github.com/jchevertonwynne/ssanta/internal/pgp"
)

func TestRoomPGPChallengeLifecycle(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	userID := createUser(t, pool, "alice")
	roomID := createRoom(t, pool, "room1", userID)

	ctx, cancel := testCtx(t)
	defer cancel()
	require.NoError(t, st.Rooms.JoinRoom(ctx, roomID, userID))

	// Generate a key and normalize it.
	armoredPub, privKey := mustGenerateTestKeyPair(t)
	normalized, fingerprint, err := pgp.NormalizePublicKey(armoredPub, time.Now())
	require.NoError(t, err)

	challenge := "test-challenge-plaintext"
	ciphertext, err := pgp.EncryptToPublicKey(normalized, []byte(challenge))
	require.NoError(t, err)

	now := time.Now()
	expiresAt := now.Add(10 * time.Minute)
	hash := pgp.HashChallenge(challenge)

	require.NoError(t, st.Rooms.UpsertRoomUserPGPKeyWithChallenge(ctx, roomID, userID, normalized, fingerprint, ciphertext, hash, expiresAt))

	// Wrong plaintext should fail.
	err = st.Rooms.VerifyRoomUserPGPChallenge(ctx, roomID, userID, "wrong", now)
	require.ErrorIs(t, err, ErrPGPChallengeIncorrect)

	// Expired should fail.
	require.NoError(t, st.Rooms.UpsertRoomUserPGPKeyWithChallenge(ctx, roomID, userID, normalized, fingerprint, ciphertext, hash, now.Add(-time.Minute)))
	err = st.Rooms.VerifyRoomUserPGPChallenge(ctx, roomID, userID, challenge, now)
	require.ErrorIs(t, err, ErrPGPChallengeExpired)

	// Valid should succeed.
	require.NoError(t, st.Rooms.UpsertRoomUserPGPKeyWithChallenge(ctx, roomID, userID, normalized, fingerprint, ciphertext, hash, expiresAt))
	err = st.Rooms.VerifyRoomUserPGPChallenge(ctx, roomID, userID, challenge, now)
	require.NoError(t, err)

	members, err := st.Rooms.ListRoomMembersWithPGP(ctx, roomID)
	require.NoError(t, err)
	var found *RoomMember
	for i := range members {
		if members[i].ID == userID {
			found = &members[i]
			break
		}
	}
	require.NotNil(t, found)
	require.Equal(t, fingerprint, found.PGPFingerprint)
	require.NotNil(t, found.PGPVerifiedAt)
	require.Empty(t, found.PGPChallengeCiphertext)
	require.Nil(t, found.PGPChallengeExpiresAt)

	// Sanity: ciphertext can be decrypted by the generated private key.
	plaintext := mustDecryptArmored(t, privKey, ciphertext)
	require.Equal(t, challenge, plaintext)
}

func TestRoomPGPClearKey(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	userID := createUser(t, pool, "alice")
	roomID := createRoom(t, pool, "room1", userID)

	ctx, cancel := testCtx(t)
	defer cancel()
	require.NoError(t, st.Rooms.JoinRoom(ctx, roomID, userID))

	armoredPub, _ := mustGenerateTestKeyPair(t)
	normalized, fingerprint, err := pgp.NormalizePublicKey(armoredPub, time.Now())
	require.NoError(t, err)

	challenge := "test-challenge-plaintext"
	ciphertext, err := pgp.EncryptToPublicKey(normalized, []byte(challenge))
	require.NoError(t, err)

	now := time.Now()
	expiresAt := now.Add(10 * time.Minute)
	hash := pgp.HashChallenge(challenge)

	require.NoError(t, st.Rooms.UpsertRoomUserPGPKeyWithChallenge(ctx, roomID, userID, normalized, fingerprint, ciphertext, hash, expiresAt))
	require.NoError(t, st.Rooms.ClearRoomUserPGPKey(ctx, roomID, userID))

	members, err := st.Rooms.ListRoomMembersWithPGP(ctx, roomID)
	require.NoError(t, err)
	var found *RoomMember
	for i := range members {
		if members[i].ID == userID {
			found = &members[i]
			break
		}
	}
	require.NotNil(t, found)
	require.Empty(t, found.PGPPublicKey)
	require.Empty(t, found.PGPFingerprint)
	require.Nil(t, found.PGPVerifiedAt)
	require.Empty(t, found.PGPChallengeCiphertext)
	require.Nil(t, found.PGPChallengeExpiresAt)
}

func mustGenerateTestKeyPair(t *testing.T) (string, *crypto.Key) {
	t.Helper()

	pgpHandle := crypto.PGP()
	priv, err := pgpHandle.KeyGeneration().AddUserId("test", "test@example.com").New().GenerateKey()
	require.NoError(t, err)

	pub, err := priv.ToPublic()
	require.NoError(t, err)

	armoredPub, err := pub.Armor()
	require.NoError(t, err)

	return armoredPub, priv
}

func mustDecryptArmored(t *testing.T, privateKey *crypto.Key, armoredCiphertext string) string {
	t.Helper()

	pgpHandle := crypto.PGP()
	decHandle, err := pgpHandle.Decryption().DecryptionKey(privateKey).New()
	require.NoError(t, err)
	decrypted, err := decHandle.Decrypt([]byte(armoredCiphertext), crypto.Armor)
	require.NoError(t, err)
	return string(decrypted.Bytes())
}
