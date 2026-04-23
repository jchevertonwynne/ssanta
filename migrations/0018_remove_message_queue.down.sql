CREATE TABLE message_queue (
    id               BIGSERIAL PRIMARY KEY,
    room_id          BIGINT NOT NULL REFERENCES rooms(id)  ON DELETE CASCADE,
    recipient_id     BIGINT NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    sender_username  TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    message          TEXT NOT NULL,
    pre_encrypted    BOOLEAN NOT NULL DEFAULT false,
    whisper          BOOLEAN NOT NULL DEFAULT false
);

CREATE INDEX idx_message_queue_recipient
    ON message_queue(room_id, recipient_id, created_at);
