ALTER TABLE room_users
    ADD COLUMN pgp_public_key TEXT,
    ADD COLUMN pgp_fingerprint TEXT,
    ADD COLUMN pgp_verified_at TIMESTAMPTZ,
    ADD COLUMN pgp_challenge_ciphertext TEXT,
    ADD COLUMN pgp_challenge_hash BYTEA,
    ADD COLUMN pgp_challenge_expires_at TIMESTAMPTZ;

CREATE INDEX idx_room_users_pgp_challenge_expires_at ON room_users(pgp_challenge_expires_at);
