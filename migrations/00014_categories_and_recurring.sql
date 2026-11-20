-- F13 (M2 items 1, 6, 7): categories, recurring rules and their exceptions (expand step).
-- tasks.category_id stays NULLable until 00015; a trigger fills it for the previous API version,
-- which still writes only the legacy tasks.category string. tasks.category is never rewritten.

-- +goose Up
CREATE TABLE categories (
    id          bigserial PRIMARY KEY,
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        varchar(64) NOT NULL,
    color       char(7)     NOT NULL,
    icon        varchar(32) NOT NULL,
    kind        varchar(16) NOT NULL,
    system_key  varchar(32),            -- one of the six defaults; NULL for a user's own
    name_custom boolean     NOT NULL DEFAULT false,   -- true once the user renames it
    legacy_name varchar(64),            -- the tasks.category string it came from
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chk_categories_kind  CHECK (kind IN ('focus','study','admin','exercise','rest','personal')),
    CONSTRAINT chk_categories_color CHECK (color ~ '^#[0-9A-F]{6}$'),
    CONSTRAINT chk_categories_icon  CHECK (icon IN (
        'target','book-open','briefcase','dumbbell','coffee','user','graduation-cap','heart',
        'home','moon','music','palette','plane','shopping-cart','users','wrench')),
    CONSTRAINT chk_categories_name  CHECK (char_length(btrim(name)) BETWEEN 1 AND 64)
);
CREATE UNIQUE INDEX uq_categories_user_name   ON categories (user_id, lower(name));
CREATE UNIQUE INDEX uq_categories_user_system ON categories (user_id, system_key) WHERE system_key IS NOT NULL;
CREATE UNIQUE INDEX uq_categories_user_legacy ON categories (user_id, legacy_name) WHERE legacy_name IS NOT NULL;

-- The six defaults, in one place: the backfill, the trigger and EnsureDefaultCategories all
-- read these two tables, so the mapping exists exactly once.
CREATE TABLE category_defaults (
    system_key varchar(32) PRIMARY KEY,
    name       varchar(64) NOT NULL,   -- English fallback; the web translates by system_key
    kind       varchar(16) NOT NULL,
    color      char(7)     NOT NULL,
    icon       varchar(32) NOT NULL,
    position   smallint    NOT NULL
);
INSERT INTO category_defaults VALUES
    ('deep_work','Deep Work','focus',   '#3B82F6','target',    1),
    ('study',    'Study',    'study',   '#8B5CF6','book-open', 2),
    ('admin',    'Admin',    'admin',   '#6B7280','briefcase', 3),
    ('exercise', 'Gym',      'exercise','#10B981','dumbbell',  4),
    ('rest',     'Rest',     'rest',    '#F59E0B','coffee',    5),
    ('personal', 'Personal', 'personal','#EC4899','user',      6);

CREATE TABLE category_legacy_map (
    legacy     varchar(64) PRIMARY KEY,   -- lower(btrim(tasks.category))
    name       varchar(64) NOT NULL,
    kind       varchar(16) NOT NULL,
    system_key varchar(32) REFERENCES category_defaults (system_key)
);
INSERT INTO category_legacy_map VALUES
    ('deep-work','Deep Work','focus','deep_work'), ('deep work','Deep Work','focus','deep_work'),
    ('study','Study','study','study'),             ('admin','Admin','admin','admin'),
    ('gym','Gym','exercise','exercise'),           ('exercise','Gym','exercise','exercise'),
    ('rest','Rest','rest','rest'),                 ('personal','Personal','personal','personal'),
    ('fixed','Fixed','personal',NULL);  -- a commitment, not exercise or focus; keeps its own category

CREATE TABLE recurring_rules (
    id          bigserial PRIMARY KEY,
    user_id     bigint       NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- NO ACTION (the default), not RESTRICT: deleting a user removes categories and rules in the
    -- same statement, while a category in use still cannot be deleted on its own.
    category_id bigint       NOT NULL REFERENCES categories (id),
    name        varchar(120) NOT NULL,
    weekdays    smallint[]   NOT NULL,            -- ISO: Mon = 1 … Sun = 7
    start_min   integer      NOT NULL,
    end_min     integer      NOT NULL,
    valid_from  date         NOT NULL,
    valid_to    date,
    split_from_rule_id bigint REFERENCES recurring_rules (id) ON DELETE SET NULL,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT chk_recurring_minutes  CHECK (start_min BETWEEN 0 AND 1440 AND end_min BETWEEN 0 AND 1440 AND start_min < end_min),
    CONSTRAINT chk_recurring_dates    CHECK (valid_to IS NULL OR valid_to >= valid_from),
    CONSTRAINT chk_recurring_weekdays CHECK (
        coalesce(array_ndims(weekdays), 0) = 1   -- an empty array has no dimensions (NULL)
        AND array_length(weekdays, 1) BETWEEN 1 AND 7
        AND array_position(weekdays, NULL) IS NULL
        AND weekdays <@ ARRAY[1,2,3,4,5,6,7]::smallint[])
);
CREATE INDEX idx_recurring_rules_user_dates ON recurring_rules (user_id, valid_from, valid_to);

-- A date a rule deliberately has no block on. Never a block with status 'skipped': skipped
-- counts against the user and these are not misses (VISION: rest is never shown as failure).
CREATE TABLE recurring_exceptions (
    id         bigserial PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rule_id    bigint      NOT NULL REFERENCES recurring_rules (id) ON DELETE CASCADE,
    date       date        NOT NULL,
    reason     varchar(16) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chk_recurring_exception_reason CHECK (reason IN ('cancelled','conflict','moved')),
    CONSTRAINT uq_recurring_exception UNIQUE (rule_id, date)
);
CREATE INDEX idx_recurring_exceptions_user_date ON recurring_exceptions (user_id, date);

ALTER TABLE tasks
    ADD COLUMN category_id       bigint REFERENCES categories (id),   -- NULL until 00015
    ADD COLUMN recurring_rule_id bigint REFERENCES recurring_rules (id) ON DELETE SET NULL,
    ADD COLUMN original_date     date,
    ADD COLUMN recurring_edited  boolean NOT NULL DEFAULT false;

-- 'recurring' joins the source values. NOT VALID first, so the ACCESS EXCLUSIVE lock is taken
-- only for the catalogue change; VALIDATE then scans under SHARE UPDATE EXCLUSIVE, which does
-- not block reads or writes.
ALTER TABLE tasks DROP CONSTRAINT chk_tasks_source;
ALTER TABLE tasks ADD CONSTRAINT chk_tasks_source
    CHECK (source IN ('manual','smart-schedule','recurring')) NOT VALID;
ALTER TABLE tasks VALIDATE CONSTRAINT chk_tasks_source;

-- Backfill: one category per (user, mapped name), case-insensitively, so two legacy strings that
-- map to the same name ('gym'/'exercise', 'Reading'/'reading') share one category and
-- uq_categories_user_name cannot be violated. DISTINCT ON makes the survivor deterministic and
-- prefers the mapped row (the one with a system key).
INSERT INTO categories (user_id, name, color, icon, kind, system_key, legacy_name)
SELECT DISTINCT ON (s.user_id, lower(s.name))
       s.user_id, s.name, d.color, d.icon, s.kind, s.system_key, s.legacy
  FROM (SELECT DISTINCT t.user_id,
               left(lower(btrim(t.category)), 64)            AS legacy,
               coalesce(m.name, left(btrim(t.category), 64))  AS name,
               coalesce(m.kind, 'personal')                   AS kind,
               m.system_key
          FROM tasks t
          LEFT JOIN category_legacy_map m ON m.legacy = lower(btrim(t.category))
         WHERE t.category IS NOT NULL AND btrim(t.category) <> '') s
  JOIN category_defaults d ON d.kind = s.kind
 ORDER BY s.user_id, lower(s.name), (s.system_key IS NULL), s.legacy;

-- Deterministic: exactly one category matches a (user, legacy string) pair.
UPDATE tasks t SET category_id = c.id
  FROM categories c
 WHERE c.user_id = t.user_id
   AND lower(c.name) = lower(coalesce((SELECT m.name FROM category_legacy_map m
                                        WHERE m.legacy = lower(btrim(t.category))),
                                      left(btrim(t.category), 64)));

CREATE INDEX idx_tasks_category_id ON tasks (category_id);
-- One materialized occurrence per (rule, date). A row moved here from another date carries
-- original_date and is left out, so a same-day move (old row 'moved', new row on the same date)
-- does not collide.
CREATE UNIQUE INDEX uq_tasks_recurring_slot ON tasks (user_id, recurring_rule_id, date)
    WHERE recurring_rule_id IS NOT NULL AND original_date IS NULL;
CREATE INDEX idx_tasks_original_date ON tasks (recurring_rule_id, original_date) WHERE original_date IS NOT NULL;

-- Old writers during the deploy: fill category_id, never write tasks.category. Resolves by
-- 1. system_key, 2. legacy_name, 3. lower(name), 4. otherwise inserts one.
-- +goose StatementBegin
CREATE FUNCTION tasks_fill_category_id() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_legacy text; m category_legacy_map%ROWTYPE; want_name text; want_kind text;
BEGIN
    IF NEW.category_id IS NOT NULL OR btrim(coalesce(NEW.category, '')) = '' THEN
        RETURN NEW;                                   -- the new API sends the id
    END IF;
    v_legacy := left(lower(btrim(NEW.category)), 64);
    SELECT * INTO m FROM category_legacy_map WHERE category_legacy_map.legacy = v_legacy;
    want_name := coalesce(m.name, left(btrim(NEW.category), 64));
    want_kind := coalesce(m.kind, 'personal');
    SELECT c.id INTO NEW.category_id FROM categories c
     WHERE c.user_id = NEW.user_id
       AND (c.system_key = m.system_key OR c.legacy_name = v_legacy OR lower(c.name) = lower(want_name))
     ORDER BY (c.system_key IS DISTINCT FROM m.system_key), (c.legacy_name IS DISTINCT FROM v_legacy)
     LIMIT 1;
    IF NEW.category_id IS NULL THEN
        INSERT INTO categories (user_id, name, color, icon, kind, system_key, legacy_name)
        SELECT NEW.user_id, want_name, d.color, d.icon, want_kind, m.system_key, v_legacy
          FROM category_defaults d WHERE d.kind = want_kind
        ON CONFLICT DO NOTHING RETURNING id INTO NEW.category_id;
        IF NEW.category_id IS NULL THEN
            SELECT c.id INTO NEW.category_id FROM categories c
             WHERE c.user_id = NEW.user_id AND lower(c.name) = lower(want_name);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER trg_tasks_fill_category_id BEFORE INSERT OR UPDATE OF category ON tasks
    FOR EACH ROW EXECUTE FUNCTION tasks_fill_category_id();

-- +goose Down
DROP TRIGGER trg_tasks_fill_category_id ON tasks;
DROP FUNCTION tasks_fill_category_id();
DROP INDEX idx_tasks_original_date;
DROP INDEX uq_tasks_recurring_slot;
DROP INDEX idx_tasks_category_id;
UPDATE tasks SET source = 'manual' WHERE source = 'recurring';
ALTER TABLE tasks DROP CONSTRAINT chk_tasks_source;
ALTER TABLE tasks ADD CONSTRAINT chk_tasks_source CHECK (source IN ('manual', 'smart-schedule'));
ALTER TABLE tasks
    DROP COLUMN recurring_edited,
    DROP COLUMN original_date,
    DROP COLUMN recurring_rule_id,
    DROP COLUMN category_id;
DROP TABLE recurring_exceptions;
DROP TABLE recurring_rules;
DROP TABLE category_legacy_map;
DROP TABLE category_defaults;
DROP TABLE categories;
