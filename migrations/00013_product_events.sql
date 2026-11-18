-- F1 (minimal M1 item 11): append-only product events for retention and habit metrics.
-- Names come from discipline/contracts/events.schema.json. Never sent to any other service;
-- props hold facts about the action (minutes, statuses), never personal data.

-- +goose Up
CREATE TABLE product_events (
    id          bigserial   PRIMARY KEY,
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        text        NOT NULL CHECK (name ~ '^[a-z][a-z0-9_]{2,63}$'),
    props       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_product_events_name_time ON product_events (name, occurred_at);
CREATE INDEX idx_product_events_user_time ON product_events (user_id, occurred_at);

-- +goose Down
DROP TABLE product_events;
