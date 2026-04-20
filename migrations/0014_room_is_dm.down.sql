DROP INDEX IF EXISTS idx_rooms_is_dm;

ALTER TABLE rooms DROP COLUMN is_dm;
