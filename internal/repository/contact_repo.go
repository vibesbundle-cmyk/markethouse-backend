package repository

import (
	"context"
	"database/sql"
	"fmt"

	"markethouse/internal/models"
)

type ContactRepo struct {
	DB *sql.DB
}

// PhoneUser is a minimal user row used for phone-number matching. Matching
// happens in Go (see ContactService) because normalizing users.mobile the
// same way we normalize address-book numbers can't be expressed cleanly in
// SQL across all the formats people enter numbers in.
type PhoneUser struct {
	ID           int64
	Mobile       string
	Username     string
	FullName     string
	ProfilePhoto string
	AccountType  string
	IsVerified   bool
}

// ReplaceContacts swaps the user's entire address book for the given batch.
// Delete-then-insert in one transaction keeps the stored book in sync with
// the device's book even when numbers are removed on the phone.
func (r *ContactRepo) ReplaceContacts(ctx context.Context, userID int64, contacts []models.Contact) (int64, error) {
	if len(contacts) == 0 {
		return 0, nil
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_contacts WHERE user_id = $1`, userID); err != nil {
		return 0, fmt.Errorf("clear previous contacts: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO user_contacts (user_id, contact_name, contact_phone, phone_hash)
		VALUES ($1, $2, $3, $4)`)
	if err != nil {
		return 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	var inserted int64
	for _, c := range contacts {
		res, err := stmt.ExecContext(ctx, userID, c.ContactName, c.ContactPhone, c.PhoneHash)
		if err != nil {
			return inserted, fmt.Errorf("insert contact: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil {
			inserted += n
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return inserted, nil
}

// CountContacts returns how many address-book entries the user has stored.
func (r *ContactRepo) CountContacts(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := r.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_contacts WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// ListContacts returns the user's stored address book with phone_hash intact
// so the service can flag which entries are already on the platform.
func (r *ContactRepo) ListContacts(ctx context.Context, userID int64) ([]models.Contact, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT id, user_id, contact_name, contact_phone, phone_hash,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS')::text,
		       to_char(updated_at, 'YYYY-MM-DD"T"HH24:MI:SS')::text
		FROM user_contacts
		WHERE user_id = $1
		ORDER BY contact_name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	contacts := make([]models.Contact, 0, 32)
	for rows.Next() {
		var c models.Contact
		if err := rows.Scan(&c.ID, &c.UserID, &c.ContactName, &c.ContactPhone, &c.PhoneHash, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

// AllMobileUsers returns every user that has a phone number on file, which
// the service matches against the synced address book.
func (r *ContactRepo) AllMobileUsers(ctx context.Context) ([]PhoneUser, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT id, mobile, COALESCE(username, ''), COALESCE(full_name, ''),
		       COALESCE(profile_photo, ''), COALESCE(account_type, 'personal'),
		       COALESCE(is_verified, false)
		FROM users
		WHERE mobile IS NOT NULL AND mobile <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]PhoneUser, 0, 64)
	for rows.Next() {
		var u PhoneUser
		if err := rows.Scan(&u.ID, &u.Mobile, &u.Username, &u.FullName,
			&u.ProfilePhoto, &u.AccountType, &u.IsVerified); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// FollowingSet returns the set of user IDs the viewer already follows, used
// to keep suggestions fresh (never suggest someone we already follow).
func (r *ContactRepo) FollowingSet(ctx context.Context, userID int64) (map[int64]bool, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT following_id FROM follows WHERE follower_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	set := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			set[id] = true
		}
	}
	return set, rows.Err()
}

// MutualConnections finds users followed by the accounts the viewer follows
// (2-hop suggestions), ranked by how many shared followees each has.
func (r *ContactRepo) MutualConnections(ctx context.Context, userID int64, limit int) ([]models.MatchedUser, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT u.id, COALESCE(u.username, ''), COALESCE(u.full_name, ''),
		       COALESCE(u.profile_photo, ''), COALESCE(u.account_type, 'personal'),
		       COALESCE(u.is_verified, false),
		       COUNT(*) AS mutual_count
		FROM follows f1
		JOIN follows f2 ON f2.follower_id = f1.following_id
		JOIN users u ON u.id = f2.following_id
		WHERE f1.follower_id = $1
		  AND u.id <> $1
		  AND NOT EXISTS (SELECT 1 FROM follows mine
		                  WHERE mine.follower_id = $1 AND mine.following_id = u.id)
		GROUP BY u.id
		ORDER BY mutual_count DESC, u.id
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	matches := make([]models.MatchedUser, 0, 8)
	for rows.Next() {
		var m models.MatchedUser
		if err := rows.Scan(&m.ID, &m.Username, &m.FullName, &m.ProfilePhoto,
			&m.AccountType, &m.IsVerified, &m.MutualCount); err != nil {
			return nil, err
		}
		m.MatchSource = "mutual"
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// ClearContacts wipes the user's synced address book (e.g. when they turn
// contact syncing off).
func (r *ContactRepo) ClearContacts(ctx context.Context, userID int64) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM user_contacts WHERE user_id = $1`, userID)
	return err
}

// SetContactSyncEnabled flips the master toggle; enabling records the sync time.
func (r *ContactRepo) SetContactSyncEnabled(ctx context.Context, userID int64, enabled bool) error {
	if enabled {
		_, err := r.DB.ExecContext(ctx, `
			UPDATE users SET contact_sync_enabled = true, contact_sync_at = NOW()
			WHERE id = $1`, userID)
		return err
	}
	_, err := r.DB.ExecContext(ctx, `UPDATE users SET contact_sync_enabled = false WHERE id = $1`, userID)
	return err
}

// GetContactSync reads the user's contact-sync setting and stored count.
func (r *ContactRepo) GetContactSync(ctx context.Context, userID int64) (models.ContactSyncSetting, error) {
	var s models.ContactSyncSetting
	var lastSync sql.NullTime
	var syncedCount int64

	err := r.DB.QueryRowContext(ctx, `
		SELECT COALESCE(contact_sync_enabled, false), contact_sync_at,
		       (SELECT COUNT(*) FROM user_contacts WHERE user_id = $1)
		FROM users WHERE id = $1`, userID).Scan(&s.ContactSyncEnabled, &lastSync, &syncedCount)
	if err != nil {
		return s, err
	}
	s.SyncedCount = syncedCount
	if lastSync.Valid && !lastSync.Time.IsZero() {
		s.ContactSyncAt = lastSync.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return s, nil
}
