-- F1 (M3 item 5): evening shutdown closes a local day, with one win. Tomorrow's Top-3 comes with F10.

-- +goose Up
CREATE TABLE shutdowns (
    user_id   bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    date      date        NOT NULL,
    win       text        CHECK (win IS NULL OR char_length(win) <= 280),
    closed_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, date)
);

-- +goose Down
DROP TABLE shutdowns;
