ALTER TABLE rooms DROP COLUMN deleted_at;
ALTER TABLE users DROP COLUMN deleted_at;

DROP INDEX IF EXISTS idx_rooms_display_name_active;
ALTER TABLE rooms ADD CONSTRAINT rooms_display_name_key UNIQUE (display_name);

DROP INDEX IF EXISTS idx_rooms_public;
CREATE INDEX idx_rooms_public ON rooms(id) WHERE is_public = true;
