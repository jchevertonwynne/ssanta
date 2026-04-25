CREATE TABLE admins (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    granted_by  BIGINT REFERENCES users(id) ON DELETE SET NULL,
    admin_since TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id)
);
