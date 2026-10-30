-- F1 (M3 item 1): morning check-in, one per user and local day. Sleep times are minutes of the
-- day; a start after the end means sleep began the evening before. A skipped check-in is stored
-- so other devices don't ask again.

-- +goose Up
CREATE TABLE checkins (
    id              bigserial   PRIMARY KEY,
    user_id         bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    date            date        NOT NULL,
    skipped         boolean     NOT NULL DEFAULT false,
    sleep_start_min integer     CHECK (sleep_start_min BETWEEN 0 AND 1439),
    sleep_end_min   integer     CHECK (sleep_end_min BETWEEN 0 AND 1440),
    sleep_quality   smallint    CHECK (sleep_quality BETWEEN 1 AND 5),
    energy          smallint    CHECK (energy BETWEEN 1 AND 5),
    note            text        CHECK (note IS NULL OR char_length(note) <= 500),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, date),
    CHECK (skipped OR (sleep_start_min IS NOT NULL AND sleep_end_min IS NOT NULL
                       AND sleep_quality IS NOT NULL AND energy IS NOT NULL))
);

-- +goose Down
DROP TABLE checkins;
