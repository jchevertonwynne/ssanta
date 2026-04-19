DROP INDEX IF EXISTS idx_room_invites_expires_at;

ALTER TABLE room_invites
    DROP COLUMN IF EXISTS expires_at;
