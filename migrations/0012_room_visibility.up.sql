ALTER TABLE rooms
    ADD COLUMN is_public BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX idx_rooms_public ON rooms(is_public) WHERE is_public = true;
