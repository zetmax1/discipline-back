-- F-18: bumping a user's token_version revokes every JWT issued before it
-- (logout, password change). Tokens carry it as the "tv" claim.

-- +goose Up
ALTER TABLE users ADD COLUMN token_version bigint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN token_version;
