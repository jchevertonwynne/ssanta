-- Drop old indexes
DROP INDEX IF EXISTS idx_room_invites_expires_at;
DROP INDEX IF EXISTS idx_room_users_pgp_challenge_expires_at;

-- Create partial indexes for better cleanup query performance
-- These indexes only include rows that will actually be cleaned up
CREATE INDEX idx_room_invites_expires_at_partial ON room_invites(expires_at)
WHERE expires_at IS NOT NULL;

CREATE INDEX idx_room_users_pgp_challenge_expires_at_partial ON room_users(pgp_challenge_expires_at)
WHERE pgp_challenge_expires_at IS NOT NULL;
