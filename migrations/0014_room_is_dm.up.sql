ALTER TABLE rooms
    ADD COLUMN is_dm BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE rooms
    SET is_dm = TRUE
    WHERE display_name LIKE 'dm:%';

CREATE INDEX idx_rooms_is_dm ON rooms(is_dm) WHERE is_dm = TRUE;
