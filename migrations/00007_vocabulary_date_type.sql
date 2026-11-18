-- P5-6f: vocabulary_words.date -> DATE (it was the last text-typed date column).
--
-- Rows whose date is not a real YYYY-MM-DD date are never dropped: the migration
-- aborts, nothing changes, and the error lists every offending row so it can be
-- fixed by hand before re-running. The unique (user_id, date, language) index is
-- rebuilt by Postgres as part of the type change.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    r        record;
    problems text := '';
BEGIN
    FOR r IN SELECT id, user_id, date FROM vocabulary_words ORDER BY id LOOP
        BEGIN
            IF r.date !~ '^\d{4}-\d{2}-\d{2}$' THEN
                RAISE EXCEPTION 'format';
            END IF;
            PERFORM r.date::date;
        EXCEPTION WHEN others THEN
            problems := problems || format(E'\n  vocabulary_words.id=%s user_id=%s date=%L', r.id, r.user_id, r.date);
        END;
    END LOOP;

    IF problems <> '' THEN
        RAISE EXCEPTION 'migration 00007 aborted, nothing was changed. Fix or delete these rows, then re-run:%', problems;
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE vocabulary_words ALTER COLUMN date TYPE date USING date::date;

-- +goose Down
ALTER TABLE vocabulary_words ALTER COLUMN date TYPE text USING to_char(date, 'YYYY-MM-DD');
