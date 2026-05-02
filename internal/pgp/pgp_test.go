package pgp

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/stretchr/testify/require"
)

func generateTestKeyPair(t *testing.T) (armoredPub string, priv *crypto.Key) {
	t.Helper()
	pgpHandle := crypto.PGP()
	priv, err := pgpHandle.KeyGeneration().AddUserId("test", "test@example.com").New().GenerateKey()
	require.NoError(t, err)
	pub, err := priv.ToPublic()
	require.NoError(t, err)
	armoredPub, err = pub.Armor()
	require.NoError(t, err)
	return armoredPub, priv
}

func TestNewChallengeString_PrefixedAndURLSafe(t *testing.T) {
	t.Parallel()
	challenge, err := NewChallengeString(0)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(challenge, VerificationChallengePrefix))

	suffix := strings.TrimPrefix(challenge, VerificationChallengePrefix)
	require.Len(t, suffix, base64.RawURLEncoding.EncodedLen(DefaultChallengeSize))

	for _, r := range suffix {
		isUpper := r >= 'A' && r <= 'Z'
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		isDash := r == '-'
		isUnderscore := r == '_'
		require.True(t, isUpper || isLower || isDigit || isDash || isUnderscore)
	}
}

func TestNewChallengeString_RespectsSize(t *testing.T) {
	t.Parallel()
	challenge, err := NewChallengeString(1)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(challenge, VerificationChallengePrefix))

	suffix := strings.TrimPrefix(challenge, VerificationChallengePrefix)
	require.Len(t, suffix, base64.RawURLEncoding.EncodedLen(1))
}

func TestNormalizePublicKey_Empty_ReturnsError(t *testing.T) {
	t.Parallel()
	_, _, err := NormalizePublicKey("", time.Now())
	require.ErrorIs(t, err, ErrKeyEmpty)
}

func TestNormalizePublicKey_TooLarge_ReturnsError(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("a", MaxArmoredKeySize+1)
	_, _, err := NormalizePublicKey(big, time.Now())
	require.ErrorIs(t, err, ErrKeyTooLarge)
}

func TestNormalizePublicKey_PrivateKey_ReturnsError(t *testing.T) {
	t.Parallel()
	_, priv := generateTestKeyPair(t)
	armoredPriv, err := priv.Armor()
	require.NoError(t, err)
	_, _, err = NormalizePublicKey(armoredPriv, time.Now())
	require.ErrorIs(t, err, ErrKeyMustBePublic)
}

func TestNormalizePublicKey_ValidKey_ReturnsFingerprintAndArmored(t *testing.T) {
	t.Parallel()
	armoredPub, _ := generateTestKeyPair(t)
	normalized, fingerprint, err := NormalizePublicKey(armoredPub, time.Now())
	require.NoError(t, err)
	require.NotEmpty(t, fingerprint)
	require.True(t, strings.HasPrefix(strings.TrimSpace(normalized), "-----BEGIN PGP PUBLIC KEY BLOCK-----"))
}

func TestHashChallenge_KnownInput(t *testing.T) {
	t.Parallel()
	input := "ssanta-verification-abc123"
	got := HashChallenge(input)
	want := sha256.Sum256([]byte(input))
	require.Equal(t, want[:], got)
}

func TestHashChallenge_Deterministic(t *testing.T) {
	t.Parallel()
	input := "some-challenge"
	require.Equal(t, HashChallenge(input), HashChallenge(input))
}

func TestHashChallenge_Length(t *testing.T) {
	t.Parallel()
	require.Len(t, HashChallenge("x"), 32)
}

func TestEncryptToPublicKey_InvalidKey_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := EncryptToPublicKey("not-pgp-armored", []byte("hello"))
	require.Error(t, err)
}

func TestEncryptToPublicKey_ValidKey_ProducesArmoredMessage(t *testing.T) {
	t.Parallel()
	armoredPub, _ := generateTestKeyPair(t)
	armored, err := EncryptToPublicKey(armoredPub, []byte("hello world"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(strings.TrimSpace(armored), "-----BEGIN PGP MESSAGE-----"))
}

func TestEncryptToPublicKey_RoundTrip(t *testing.T) {
	t.Parallel()
	armoredPub, priv := generateTestKeyPair(t)
	plaintext := []byte("secret message")

	armored, err := EncryptToPublicKey(armoredPub, plaintext)
	require.NoError(t, err)

	pgpHandle := crypto.PGP()
	decHandle, err := pgpHandle.Decryption().DecryptionKey(priv).New()
	require.NoError(t, err)
	result, err := decHandle.Decrypt([]byte(armored), crypto.Armor)
	require.NoError(t, err)
	require.Equal(t, plaintext, result.Bytes())
}
