ALTER TABLE room_invites
    ADD COLUMN expires_at TIMESTAMPTZ;

-- Backfill existing rows.
UPDATE room_invites
SET expires_at = created_at + interval '24 hours'
WHERE expires_at IS NULL;

ALTER TABLE room_invites
    ALTER COLUMN expires_at SET NOT NULL;

CREATE INDEX idx_room_invites_expires_at ON room_invites(expires_at);
