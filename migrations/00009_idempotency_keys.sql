-- F1: replay-safe POSTs (offline clients resend). One row per (user, key) records the resource the
-- first request created; a replay returns that resource instead of creating another.
-- Rows older than a day are deleted when new keys are written.

-- +goose Up
CREATE TABLE idempotency_keys (
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key         text        NOT NULL CHECK (char_length(key) BETWEEN 8 AND 100),
    resource_id bigint      NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
CREATE INDEX idx_idempotency_keys_created ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE idempotency_keys;
