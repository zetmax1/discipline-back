-- F1 (M2 item 5): block lifecycle. Times become minutes 0-1440 (start_min/end_min; the TIME
-- columns stay, written alongside, and are dropped in a later migration). Statuses become
-- planned -> in_progress -> done | partial | skipped | moved; blocks gain actual start/end,
-- a focus rating, a note and moved_from_id. Legacy inverted rows (end <= start) are kept.
-- A status the mapping doesn't know aborts the migration and lists the rows (nothing changes).

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    r        record;
    problems text := '';
BEGIN
    FOR r IN SELECT id, user_id, status FROM tasks
             WHERE status NOT IN ('pending', 'completed', 'skipped') ORDER BY id LOOP
        problems := problems || format(E'\n  tasks.id=%s user_id=%s status=%L', r.id, r.user_id, r.status);
    END LOOP;
    IF problems <> '' THEN
        RAISE EXCEPTION 'migration 00008 aborted, nothing was changed. Fix these statuses, then re-run:%', problems;
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE tasks
    ADD COLUMN start_min        integer,
    ADD COLUMN end_min          integer,
    ADD COLUMN actual_start_min integer,
    ADD COLUMN actual_end_min   integer,
    ADD COLUMN focus_rating     smallint,
    ADD COLUMN note             text,
    ADD COLUMN moved_from_id    bigint REFERENCES tasks (id) ON DELETE SET NULL;

UPDATE tasks SET
    start_min = EXTRACT(HOUR FROM start_time)::int * 60 + EXTRACT(MINUTE FROM start_time)::int,
    end_min   = EXTRACT(HOUR FROM end_time)::int * 60 + EXTRACT(MINUTE FROM end_time)::int,
    status    = CASE status WHEN 'pending' THEN 'planned' WHEN 'completed' THEN 'done' ELSE status END;

ALTER TABLE tasks
    ALTER COLUMN start_min SET NOT NULL,
    ALTER COLUMN end_min SET NOT NULL,
    ALTER COLUMN status SET DEFAULT 'planned',
    ADD CONSTRAINT chk_tasks_minutes CHECK (start_min BETWEEN 0 AND 1440 AND end_min BETWEEN 0 AND 1440),
    ADD CONSTRAINT chk_tasks_actual_minutes CHECK (
        (actual_start_min IS NULL OR actual_start_min BETWEEN 0 AND 1440)
        AND (actual_end_min IS NULL OR actual_end_min BETWEEN 0 AND 1440)),
    ADD CONSTRAINT chk_tasks_status CHECK (status IN ('planned', 'in_progress', 'done', 'partial', 'skipped', 'moved')),
    ADD CONSTRAINT chk_tasks_focus_rating CHECK (focus_rating IS NULL OR focus_rating BETWEEN 1 AND 5),
    ADD CONSTRAINT chk_tasks_note CHECK (note IS NULL OR char_length(note) <= 500);

CREATE INDEX idx_tasks_moved_from ON tasks (moved_from_id) WHERE moved_from_id IS NOT NULL;

-- Until the TIME columns are dropped: keep minutes and TIME in sync whichever side is written,
-- and translate old statuses, so the previous API version keeps working during a deploy.
-- +goose StatementBegin
CREATE FUNCTION tasks_sync_minutes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'pending' THEN NEW.status := 'planned'; END IF;
    IF NEW.status = 'completed' THEN NEW.status := 'done'; END IF;
    IF TG_OP = 'UPDATE' THEN
        IF NEW.start_time IS DISTINCT FROM OLD.start_time AND NEW.start_min IS NOT DISTINCT FROM OLD.start_min THEN
            NEW.start_min := NULL;
        ELSIF NEW.start_min IS DISTINCT FROM OLD.start_min AND NEW.start_time IS NOT DISTINCT FROM OLD.start_time THEN
            NEW.start_time := NULL;
        END IF;
        IF NEW.end_time IS DISTINCT FROM OLD.end_time AND NEW.end_min IS NOT DISTINCT FROM OLD.end_min THEN
            NEW.end_min := NULL;
        ELSIF NEW.end_min IS DISTINCT FROM OLD.end_min AND NEW.end_time IS NOT DISTINCT FROM OLD.end_time THEN
            NEW.end_time := NULL;
        END IF;
    END IF;
    IF NEW.start_min IS NULL AND NEW.start_time IS NOT NULL THEN
        NEW.start_min := EXTRACT(HOUR FROM NEW.start_time)::int * 60 + EXTRACT(MINUTE FROM NEW.start_time)::int;
    END IF;
    IF NEW.end_min IS NULL AND NEW.end_time IS NOT NULL THEN
        NEW.end_min := EXTRACT(HOUR FROM NEW.end_time)::int * 60 + EXTRACT(MINUTE FROM NEW.end_time)::int;
    END IF;
    IF NEW.start_time IS NULL AND NEW.start_min IS NOT NULL THEN
        NEW.start_time := CASE WHEN NEW.start_min = 1440 THEN '24:00'::time ELSE make_time(NEW.start_min / 60, NEW.start_min % 60, 0) END;
    END IF;
    IF NEW.end_time IS NULL AND NEW.end_min IS NOT NULL THEN
        NEW.end_time := CASE WHEN NEW.end_min = 1440 THEN '24:00'::time ELSE make_time(NEW.end_min / 60, NEW.end_min % 60, 0) END;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER trg_tasks_sync_minutes BEFORE INSERT OR UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION tasks_sync_minutes();

-- +goose Down
DROP TRIGGER trg_tasks_sync_minutes ON tasks;
DROP FUNCTION tasks_sync_minutes();
DROP INDEX idx_tasks_moved_from;
ALTER TABLE tasks
    DROP CONSTRAINT chk_tasks_note,
    DROP CONSTRAINT chk_tasks_focus_rating,
    DROP CONSTRAINT chk_tasks_status,
    DROP CONSTRAINT chk_tasks_actual_minutes,
    DROP CONSTRAINT chk_tasks_minutes,
    ALTER COLUMN status SET DEFAULT 'pending';
UPDATE tasks SET status = CASE status
    WHEN 'planned' THEN 'pending' WHEN 'in_progress' THEN 'pending'
    WHEN 'done' THEN 'completed' WHEN 'partial' THEN 'completed'
    WHEN 'moved' THEN 'skipped' ELSE status END;
ALTER TABLE tasks
    DROP COLUMN moved_from_id,
    DROP COLUMN note,
    DROP COLUMN focus_rating,
    DROP COLUMN actual_end_min,
    DROP COLUMN actual_start_min,
    DROP COLUMN end_min,
    DROP COLUMN start_min;
