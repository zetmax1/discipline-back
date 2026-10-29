-- Baseline: exactly the schema GORM AutoMigrate created before versioned
-- migrations existed. IF NOT EXISTS makes it a no-op on existing databases,
-- so production only records version 1 here.

-- +goose Up
CREATE TABLE IF NOT EXISTS users (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL,
    email      text NOT NULL,
    password   text NOT NULL,
    role       text DEFAULT 'user'::text NOT NULL,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users USING btree (email);

CREATE TABLE IF NOT EXISTS tasks (
    id           bigserial PRIMARY KEY,
    user_id      bigint NOT NULL,
    name         text NOT NULL,
    category     text NOT NULL,
    start_time   text NOT NULL,
    end_time     text NOT NULL,
    status       text DEFAULT 'pending'::text NOT NULL,
    distractions bigint DEFAULT 0 NOT NULL,
    energy       bigint DEFAULT 3 NOT NULL,
    date         text NOT NULL,
    created_at   timestamp with time zone,
    updated_at   timestamp with time zone
);
CREATE INDEX IF NOT EXISTS idx_tasks_user_id ON tasks USING btree (user_id);
CREATE INDEX IF NOT EXISTS idx_tasks_date ON tasks USING btree (date);

CREATE TABLE IF NOT EXISTS vocabulary_words (
    id             bigserial PRIMARY KEY,
    user_id        bigint NOT NULL,
    word           text NOT NULL,
    language       text NOT NULL,
    definition     text NOT NULL,
    example        text,
    synonym        text,
    part_of_speech text,
    date           text NOT NULL,
    learned        boolean DEFAULT false,
    saved          boolean DEFAULT false,
    created_at     timestamp with time zone,
    updated_at     timestamp with time zone
);
CREATE INDEX IF NOT EXISTS idx_vocabulary_words_user_id ON vocabulary_words USING btree (user_id);
CREATE INDEX IF NOT EXISTS idx_vocabulary_words_date ON vocabulary_words USING btree (date);

-- +goose Down
DROP TABLE IF EXISTS vocabulary_words;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS users;
