-- F13 review M2: during the deploy the previous API changes a block's legacy string with an
-- UPDATE that leaves category_id as it was, so 00014's trigger returned early and the block kept
-- its old category. The function now re-resolves when only the string changed. The trigger itself
-- (BEFORE INSERT OR UPDATE OF category) is unchanged; the contract migration that drops it and
-- sets NOT NULL follows in a later release.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tasks_fill_category_id() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_legacy text; m category_legacy_map%ROWTYPE; want_name text; want_kind text;
BEGIN
    IF btrim(coalesce(NEW.category, '')) = '' THEN
        RETURN NEW;
    END IF;
    IF NEW.category_id IS NOT NULL THEN
        -- The new API sends the id. Only the previous API changes the string and leaves the id
        -- as it was: then the block moves to the category of its new string (F13 review M2).
        IF TG_OP = 'INSERT' OR NEW.category IS NOT DISTINCT FROM OLD.category
           OR NEW.category_id IS DISTINCT FROM OLD.category_id THEN
            RETURN NEW;
        END IF;
        NEW.category_id := NULL;
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tasks_fill_category_id() RETURNS trigger LANGUAGE plpgsql AS $$
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
