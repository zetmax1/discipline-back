-- F-28: tasks.date -> DATE, start_time/end_time -> TIME, composite (user_id, date)
-- indexes, and user foreign keys with ON DELETE CASCADE.
--
-- Rows that cannot be converted (non-ISO or impossible dates, unparseable times)
-- and rows whose user no longer exists are never dropped: the migration aborts,
-- nothing changes, and the error lists every offending row so it can be fixed
-- by hand before re-running.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    r        record;
    problems text := '';
BEGIN
    FOR r IN SELECT id, user_id, date, start_time, end_time FROM tasks ORDER BY id LOOP
        BEGIN
            IF r.date !~ '^\d{4}-\d{2}-\d{2}$'
               OR r.start_time !~ '^\d{1,2}:\d{2}$'
               OR r.end_time   !~ '^\d{1,2}:\d{2}$' THEN
                RAISE EXCEPTION 'format';
            END IF;
            PERFORM r.date::date, r.start_time::time, r.end_time::time;
        EXCEPTION WHEN others THEN
            problems := problems || format(E'\n  tasks.id=%s user_id=%s date=%L start_time=%L end_time=%L',
                                           r.id, r.user_id, r.date, r.start_time, r.end_time);
        END;
    END LOOP;

    FOR r IN SELECT t.id, t.user_id FROM tasks t
             WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.id = t.user_id) ORDER BY t.id LOOP
        problems := problems || format(E'\n  tasks.id=%s user_id=%s has no matching user', r.id, r.user_id);
    END LOOP;

    FOR r IN SELECT v.id, v.user_id FROM vocabulary_words v
             WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.id = v.user_id) ORDER BY v.id LOOP
        problems := problems || format(E'\n  vocabulary_words.id=%s user_id=%s has no matching user', r.id, r.user_id);
    END LOOP;

    IF problems <> '' THEN
        RAISE EXCEPTION 'migration 00002 aborted, nothing was changed. Fix or delete these rows, then re-run:%', problems;
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE tasks
    ALTER COLUMN date       TYPE date USING date::date,
    ALTER COLUMN start_time TYPE time USING start_time::time,
    ALTER COLUMN end_time   TYPE time USING end_time::time;

-- The composite indexes make the single user_id indexes redundant.
CREATE INDEX idx_tasks_user_date ON tasks USING btree (user_id, date);
DROP INDEX idx_tasks_user_id;
CREATE INDEX idx_vocabulary_words_user_date ON vocabulary_words USING btree (user_id, date);
DROP INDEX idx_vocabulary_words_user_id;

ALTER TABLE tasks
    ADD CONSTRAINT fk_tasks_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE vocabulary_words
    ADD CONSTRAINT fk_vocabulary_words_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE vocabulary_words DROP CONSTRAINT fk_vocabulary_words_user;
ALTER TABLE tasks DROP CONSTRAINT fk_tasks_user;

CREATE INDEX idx_vocabulary_words_user_id ON vocabulary_words USING btree (user_id);
DROP INDEX idx_vocabulary_words_user_date;
CREATE INDEX idx_tasks_user_id ON tasks USING btree (user_id);
DROP INDEX idx_tasks_user_date;

-- left(x::text, 5) keeps "HH:MM", including "24:00".
ALTER TABLE tasks
    ALTER COLUMN date       TYPE text USING to_char(date, 'YYYY-MM-DD'),
    ALTER COLUMN start_time TYPE text USING left(start_time::text, 5),
    ALTER COLUMN end_time   TYPE text USING left(end_time::text, 5);
