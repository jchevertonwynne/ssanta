DROP INDEX IF EXISTS idx_room_users_pgp_challenge_expires_at;

ALTER TABLE room_users
	DROP COLUMN IF EXISTS pgp_public_key,
	DROP COLUMN IF EXISTS pgp_fingerprint,
	DROP COLUMN IF EXISTS pgp_verified_at,
	DROP COLUMN IF EXISTS pgp_challenge_ciphertext,
	DROP COLUMN IF EXISTS pgp_challenge_hash,
	DROP COLUMN IF EXISTS pgp_challenge_expires_at;
