-- F9a (docs/design/F9a.md §2): commitments, weekly targets and calendar sources on the server.
-- Additive only: no existing table or row changes, so the previous API runs on this schema.

-- +goose Up
CREATE TABLE calendar_sources (
  id              bigserial PRIMARY KEY,
  user_id         bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  url_ciphertext  bytea  NOT NULL,            -- AES-256-GCM: "v1" key id + nonce + sealed URL
  url_fingerprint bytea  NOT NULL,            -- HMAC-SHA256(derived key, normalised URL)
  url_host        text   NOT NULL CHECK (length(url_host) BETWEEN 1 AND 253),
  last_fetched_at timestamptz,
  last_status     text CHECK (last_status IN ('ok','blocked','failed','timeout','too_large','invalid')),
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, url_fingerprint)
);

CREATE TABLE commitments (
  id                 bigserial PRIMARY KEY,
  user_id            bigint     NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name               text       NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  kind               text       NOT NULL CHECK (kind IN ('focus','study','admin','exercise','rest','personal')),
  weekdays           smallint[] NOT NULL
                     CHECK (coalesce(array_ndims(weekdays), 0) = 1
                            AND weekdays <@ '{1,2,3,4,5,6,7}' AND array_position(weekdays, NULL) IS NULL),
  start_min          integer    NOT NULL CHECK (start_min BETWEEN 0 AND 1439),
  end_min            integer    NOT NULL CHECK (end_min BETWEEN 1 AND 1440),
  valid_from         date       NOT NULL,
  valid_to           date       CHECK (valid_to IS NULL OR valid_to >= valid_from),
  source             text       NOT NULL CHECK (source IN ('manual','local_import','ics_file','ics_url')),
  calendar_source_id bigint     REFERENCES calendar_sources(id) ON DELETE SET NULL,
  external_key       bytea,     -- SHA-256(UID || ':' || group index); never the raw UID
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CHECK (start_min <> end_min)
);
CREATE INDEX ix_commitments_user_valid ON commitments (user_id, valid_from);
-- Two devices confirming one import create one row.
CREATE UNIQUE INDEX uq_commitments_external ON commitments (user_id, external_key) WHERE external_key IS NOT NULL;

CREATE TABLE weekly_targets (
  user_id               bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  max_focus_min_per_day integer CHECK (max_focus_min_per_day BETWEEN 60 AND 960 AND max_focus_min_per_day % 15 = 0),
  rest_day              smallint CHECK (rest_day BETWEEN 1 AND 7),
  local_import_at       timestamptz,          -- S2: the first import of the browser's data
  updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE category_targets (
  user_id          bigint  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  category_id      bigint  NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
  minutes_per_week integer NOT NULL CHECK (minutes_per_week BETWEEN 0 AND 4800 AND minutes_per_week % 15 = 0),
  PRIMARY KEY (user_id, category_id)
);

-- +goose Down
DROP TABLE category_targets;
DROP TABLE weekly_targets;
DROP TABLE commitments;
DROP TABLE calendar_sources;
