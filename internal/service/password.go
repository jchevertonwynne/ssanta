package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const argon2SaltLen = 16
const argon2KeyLen = 32

var (
	errInvalidHashFormat         = errors.New("invalid hash format")
	errIncompatibleArgon2Version = errors.New("incompatible argon2 version")
)

const argon2DefaultMemory = 64 * 1024
const argon2DefaultIterations = 1
const argon2DefaultParallelism = 4

// Argon2Params configures Argon2 password hashing.
type Argon2Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
}

// DefaultArgon2Params returns the default password hashing parameters.
func DefaultArgon2Params() Argon2Params {
	return Argon2Params{
		Memory:      argon2DefaultMemory,
		Iterations:  argon2DefaultIterations,
		Parallelism: argon2DefaultParallelism,
	}
}

// dummyHashSentinel is a real argon2id hash of a random secret, used as a
// constant-cost target when the requested user does not exist. This keeps the
// time taken by LoginUser approximately constant whether or not a username is
// known, removing a trivial enumeration oracle.
var dummyHashSentinel = mustHash("not-a-real-password", DefaultArgon2Params()) //nolint:gochecknoglobals // constant-time sentinel

func mustHash(password string, p Argon2Params) string {
	h, err := hashPassword(password, p)
	if err != nil {
		panic(err)
	}
	return h
}

func hashPassword(password string, params Argon2Params) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, argon2KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.Memory, params.Iterations, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func verifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	// expected: ["", "argon2id", "v=19", "m=65536,t=1,p=4", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errInvalidHashFormat
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse version: %w", err)
	}
	if version != argon2.Version {
		return false, errIncompatibleArgon2Version
	}

	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, fmt.Errorf("parse params: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	storedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode hash: %w", err)
	}

	computed := argon2.IDKey(
		[]byte(password),
		salt,
		iterations,
		memory,
		parallelism,
		uint32(len(storedHash)), //nolint:gosec
	)
	return subtle.ConstantTimeCompare(computed, storedHash) == 1, nil
}
