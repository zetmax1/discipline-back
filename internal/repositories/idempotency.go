package repositories

import "gorm.io/gorm"

// Replay-safe creates (F13, on F1's idempotency_keys table). An offline client resends a queued
// POST after a lost response; with the same Idempotency-Key the first result is returned and
// nothing new is created.

// lockForKey serialises the user's keyed writes for the rest of the transaction, so two replays
// racing with the same key cannot both miss the lookup. FOR NO KEY UPDATE does not block FK checks.
func lockForKey(tx *gorm.DB, userID uint) error { return lockUserTasks(tx, userID) }

// earlierResource returns the id the first request with this key created, if any.
// Call it after lockForKey.
func earlierResource(tx *gorm.DB, userID uint, key string) (uint, bool, error) {
	var ids []uint
	if err := tx.Raw(`SELECT resource_id FROM idempotency_keys WHERE user_id = ? AND key = ?`, userID, key).
		Scan(&ids).Error; err != nil {
		return 0, false, err
	}
	if len(ids) == 0 {
		return 0, false, nil
	}
	return ids[0], true, nil
}

// rememberKey records what the key created, and prunes keys older than a day (as F1's move does).
func rememberKey(tx *gorm.DB, userID uint, key string, resourceID uint) error {
	if err := tx.Exec(`DELETE FROM idempotency_keys WHERE created_at < now() - interval '1 day'`).Error; err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO idempotency_keys (user_id, key, resource_id) VALUES (?, ?, ?)`,
		userID, key, resourceID).Error
}
