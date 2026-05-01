ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE rooms ADD COLUMN deleted_at TIMESTAMPTZ;

ALTER TABLE rooms DROP CONSTRAINT rooms_display_name_key;
CREATE UNIQUE INDEX idx_rooms_display_name_active ON rooms(display_name) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_rooms_public;
CREATE INDEX idx_rooms_public ON rooms(id) WHERE is_public = true AND deleted_at IS NULL;
