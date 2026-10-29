-- F-05: each user's IANA time zone, used for "today" and week/month boundaries.
-- Existing users get the default; the application validates values with time.LoadLocation.

-- +goose Up
ALTER TABLE users ADD COLUMN timezone text NOT NULL DEFAULT 'Asia/Tashkent';

-- +goose Down
ALTER TABLE users DROP COLUMN timezone;
