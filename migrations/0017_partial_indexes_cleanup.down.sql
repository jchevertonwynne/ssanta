-- Restore original indexes
DROP INDEX IF EXISTS idx_room_invites_expires_at_partial;
DROP INDEX IF EXISTS idx_room_users_pgp_challenge_expires_at_partial;

CREATE INDEX idx_room_invites_expires_at ON room_invites(expires_at);
CREATE INDEX idx_room_users_pgp_challenge_expires_at ON room_users(pgp_challenge_expires_at);
