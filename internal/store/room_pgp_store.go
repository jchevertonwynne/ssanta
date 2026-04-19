package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *RoomStore) UpsertRoomUserPGPKeyWithChallenge(ctx context.Context, roomID RoomID, userID UserID, publicKey, fingerprint, challengeCiphertext string, challengeHash []byte, challengeExpiresAt time.Time) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE room_users
		 SET pgp_public_key = $3,
		     pgp_fingerprint = $4,
		     pgp_verified_at = NULL,
		     pgp_challenge_ciphertext = $5,
		     pgp_challenge_hash = $6,
		     pgp_challenge_expires_at = $7
		 WHERE room_id = $1 AND user_id = $2`,
		roomID, userID, publicKey, fingerprint, challengeCiphertext, challengeHash, challengeExpiresAt,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRoomMember
	}
	return nil
}

func (s *RoomStore) VerifyRoomUserPGPChallenge(ctx context.Context, roomID RoomID, userID UserID, providedPlaintext string, now time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var storedHash []byte
	var expiresAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT pgp_challenge_hash, pgp_challenge_expires_at
		 FROM room_users
		 WHERE room_id = $1 AND user_id = $2
		 FOR UPDATE`,
		roomID, userID,
	).Scan(&storedHash, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotRoomMember
	}
	if err != nil {
		return err
	}
	if len(storedHash) == 0 || expiresAt == nil {
		return ErrPGPChallengeMissing
	}
	if now.After(*expiresAt) {
		return ErrPGPChallengeExpired
	}

	computed := sha256.Sum256([]byte(providedPlaintext))
	if subtle.ConstantTimeCompare(storedHash, computed[:]) != 1 {
		return ErrPGPChallengeIncorrect
	}

	_, err = tx.Exec(ctx,
		`UPDATE room_users
		 SET pgp_verified_at = $3,
		     pgp_challenge_ciphertext = NULL,
		     pgp_challenge_hash = NULL,
		     pgp_challenge_expires_at = NULL
		 WHERE room_id = $1 AND user_id = $2`,
		roomID, userID, now,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *RoomStore) ClearRoomUserPGPKey(ctx context.Context, roomID RoomID, userID UserID) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE room_users
		 SET pgp_public_key = NULL,
		     pgp_fingerprint = NULL,
		     pgp_verified_at = NULL,
		     pgp_challenge_ciphertext = NULL,
		     pgp_challenge_hash = NULL,
		     pgp_challenge_expires_at = NULL
		 WHERE room_id = $1 AND user_id = $2`,
		roomID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRoomMember
	}
	return nil
}
