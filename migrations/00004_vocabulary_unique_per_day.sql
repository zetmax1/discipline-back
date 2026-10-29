-- F-14: one vocabulary word per user, date and language.
-- Existing duplicates are merged first: the oldest row (lowest id) is kept and
-- inherits learned/saved if any duplicate had them set; the others are deleted.

-- +goose Up
UPDATE vocabulary_words AS keep
   SET learned = agg.learned,
       saved   = agg.saved
  FROM (SELECT min(id)                         AS keep_id,
               coalesce(bool_or(learned), false) AS learned,
               coalesce(bool_or(saved), false)   AS saved
          FROM vocabulary_words
         GROUP BY user_id, date, language
        HAVING count(*) > 1) AS agg
 WHERE keep.id = agg.keep_id;

DELETE FROM vocabulary_words AS v
 USING vocabulary_words AS older
 WHERE older.user_id  = v.user_id
   AND older.date     = v.date
   AND older.language = v.language
   AND older.id       < v.id;

CREATE UNIQUE INDEX uq_vocabulary_words_user_date_language
    ON vocabulary_words USING btree (user_id, date, language);

-- The unique index leads with (user_id, date), so the plain composite index is redundant.
DROP INDEX idx_vocabulary_words_user_date;

-- +goose Down
-- Merged duplicates are not restored.
CREATE INDEX idx_vocabulary_words_user_date ON vocabulary_words USING btree (user_id, date);
DROP INDEX uq_vocabulary_words_user_date_language;
