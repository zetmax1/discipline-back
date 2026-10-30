-- F1 (M3 item 3): focus sessions and the parking lot. Session ids are UUIDs made by the client, so
-- an offline client can replay its queue safely (the id is the idempotency key). The server's
-- started_at is the reference every device counts down from.

-- +goose Up
CREATE TABLE focus_sessions (
    id              uuid        PRIMARY KEY,
    user_id         bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    block_id        bigint      REFERENCES tasks (id) ON DELETE SET NULL,
    started_at      timestamptz NOT NULL,
    planned_minutes integer     NOT NULL CHECK (planned_minutes BETWEEN 10 AND 180),
    break_minutes   integer     NOT NULL CHECK (break_minutes BETWEEN 0 AND 60),
    ended_at        timestamptz,
    outcome         text        CHECK (outcome IN ('completed', 'abandoned')),
    distractions    integer     NOT NULL DEFAULT 0 CHECK (distractions BETWEEN 0 AND 500),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ((ended_at IS NULL) = (outcome IS NULL)),
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE INDEX idx_focus_sessions_user_started ON focus_sessions (user_id, started_at);
CREATE UNIQUE INDEX idx_focus_sessions_one_active ON focus_sessions (user_id) WHERE ended_at IS NULL;

-- Thoughts parked during a session; F10 turns them into inbox tasks.
CREATE TABLE parking_lot (
    id         uuid        PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id uuid        REFERENCES focus_sessions (id) ON DELETE SET NULL,
    text       text        NOT NULL CHECK (char_length(text) BETWEEN 1 AND 280),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_parking_lot_user ON parking_lot (user_id, created_at);

-- +goose Down
DROP TABLE parking_lot;
DROP TABLE focus_sessions;
