package pgp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
)

const (
	// MaxArmoredKeySize is a sanity limit to avoid gigantic uploads.
	MaxArmoredKeySize = 64 * 1024

	// VerificationChallengePrefix is prepended to every generated challenge so
	// users can more easily recognize what the decrypted plaintext is for.
	VerificationChallengePrefix = "ssanta-verification-"

	// DefaultChallengeSize is the number of random bytes in the challenge token.
	DefaultChallengeSize = 32
)

var (
	ErrKeyEmpty          = errors.New("pgp key cannot be empty")
	ErrKeyTooLarge       = errors.New("pgp key too large")
	ErrKeyMustBePublic   = errors.New("pgp key must be a public key")
	ErrKeyNotEncryptable = errors.New("pgp key cannot be used for encryption")
	ErrKeyRevoked        = errors.New("pgp key is revoked")
	ErrKeyExpired        = errors.New("pgp key is expired")
)

// NormalizePublicKey parses an armored key, rejects private keys, and returns a
// normalized armored public key along with its fingerprint.
func NormalizePublicKey(armored string, now time.Time) (normalizedArmored string, fingerprint string, err error) {
	trimmed := strings.TrimSpace(armored)
	if trimmed == "" {
		return "", "", ErrKeyEmpty
	}
	if len(trimmed) > MaxArmoredKeySize {
		return "", "", ErrKeyTooLarge
	}

	key, err := crypto.NewKeyFromArmored(trimmed)
	if err != nil {
		return "", "", err
	}
	defer key.ClearPrivateParams()

	if key.IsPrivate() {
		return "", "", ErrKeyMustBePublic
	}

	nowUnix := now.Unix()
	if key.IsRevoked(nowUnix) {
		return "", "", ErrKeyRevoked
	}
	if key.IsExpired(nowUnix) {
		return "", "", ErrKeyExpired
	}
	if !key.CanEncrypt(nowUnix) {
		return "", "", ErrKeyNotEncryptable
	}

	fingerprint = key.GetFingerprint()
	normalizedArmored, err = key.GetArmoredPublicKey()
	if err != nil {
		return "", "", err
	}

	return strings.TrimSpace(normalizedArmored), fingerprint, nil
}

// NewChallengeString returns a URL-safe challenge string prefixed with
// VerificationChallengePrefix.
func NewChallengeString(size int) (string, error) {
	if size <= 0 {
		size = DefaultChallengeSize
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return VerificationChallengePrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func HashChallenge(challenge string) []byte {
	sum := sha256.Sum256([]byte(challenge))
	return sum[:]
}

// EncryptToPublicKey encrypts plaintext bytes to an armored public key and
// returns an armored PGP message string.
func EncryptToPublicKey(armoredPublicKey string, plaintext []byte) (string, error) {
	pubKey, err := crypto.NewKeyFromArmored(strings.TrimSpace(armoredPublicKey))
	if err != nil {
		return "", err
	}
	defer pubKey.ClearPrivateParams()

	pgp := crypto.PGP()
	encHandle, err := pgp.Encryption().Recipient(pubKey).New()
	if err != nil {
		return "", err
	}
	pgpMessage, err := encHandle.Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	armored, err := pgpMessage.Armor()
	if err != nil {
		return "", err
	}
	return armored, nil
}
