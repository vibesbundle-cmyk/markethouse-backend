package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"

	"markethouse/internal/models"
	"markethouse/internal/repository"
)

// ContactService handles address-book sync and "People You May Know"
// suggestions built from phone matches + mutual connections.
type ContactService struct {
	Repo *repository.ContactRepo
}

// defaultCountryCode is prepended to local numbers (leading 0) during
// normalization. Kept as a const for now; make it configurable when the
// app expands beyond its launch market.
const defaultCountryCode = "234"

// NormalizePhone strips formatting and converts a number to E.164-ish form:
//   - "0803 123 4567"  -> "2348031234567"
//   - "+2348031234567" -> "2348031234567"
//   - "2348031234567"  -> "2348031234567"
//   - already-international numbers are left as-is
func NormalizePhone(raw string) string {
	digits := strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, raw)

	if digits == "" {
		return ""
	}

	if strings.HasPrefix(digits, "0") {
		// local number: drop the trunk 0, prepend the country code
		return defaultCountryCode + digits[1:]
	}
	return digits
}

// PhoneHash returns a hex sha256 of the normalized number — the value stored
// in user_contacts.phone_hash and used to match users.mobile.
func PhoneHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// SyncResult carries the outcome of POST /contacts/sync.
type SyncResult struct {
	SyncedCount   int64                `json:"synced_count"`
	TotalContacts int64                `json:"total_contacts"`
	ActiveCount   int64                `json:"active_count"`
	ActiveMatches []models.MatchedUser `json:"active_matches"`
}

// SyncContacts imports the user's address book and returns which contacts
// are already active on the platform.
func (s *ContactService) SyncContacts(ctx context.Context, userID int64, incoming []models.IncomingContact) (SyncResult, error) {
	var result SyncResult

	// Normalize + dedupe to one row per distinct number.
	normalized := make([]models.Contact, 0, len(incoming))
	for _, c := range incoming {
		n := NormalizePhone(c.Phone)
		if n == "" {
			continue
		}
		normalized = append(normalized, models.Contact{
			ContactName:  strings.TrimSpace(c.Name),
			ContactPhone: c.Phone,
			PhoneHash:    PhoneHash(n),
		})
	}
	normalized = dedupeContacts(normalized)

	if len(normalized) == 0 {
		result.ActiveMatches = []models.MatchedUser{}
		return result, nil
	}

	synced, err := s.Repo.ReplaceContacts(ctx, userID, normalized)
	if err != nil {
		return result, err
	}
	result.SyncedCount = synced
	result.TotalContacts = int64(len(normalized))

	// Flag which of those numbers already belong to a MarketHouse account.
	matches, activeCount, err := s.MatchByPhone(ctx, userID, normalized)
	if err != nil {
		return result, err
	}
	result.ActiveMatches = matches
	result.ActiveCount = activeCount

	return result, nil
}

// ListContacts returns the user's stored book, marking which numbers already
// belong to a MarketHouse account.
func (s *ContactService) ListContacts(ctx context.Context, userID int64) ([]models.ContactView, int64, error) {
	contacts, err := s.Repo.ListContacts(ctx, userID)
	if err != nil {
		return nil, 0, err
	}

	byPhone, err := s.normalizedMobileIndex(ctx)
	if err != nil {
		return nil, 0, err
	}

	views := make([]models.ContactView, 0, len(contacts))
	var active int64
	for _, c := range contacts {
		v := models.ContactView{
			ID:           c.ID,
			ContactName:  c.ContactName,
			ContactPhone: c.ContactPhone,
		}
		if u, ok := byPhone[NormalizePhone(c.ContactPhone)]; ok {
			v.IsActive = true
			active++
			v.MatchedUser = phoneUserToModel(u)
		}
		views = append(views, v)
	}
	return views, active, nil
}

// MatchByPhone returns the app users whose number appears in the batch.
func (s *ContactService) MatchByPhone(ctx context.Context, userID int64, contacts []models.Contact) ([]models.MatchedUser, int64, error) {
	users, err := s.Repo.AllMobileUsers(ctx)
	if err != nil {
		return nil, 0, err
	}

	hashSet := make(map[string]string, len(contacts)) // hash -> contact name
	for _, c := range contacts {
		hashSet[c.PhoneHash] = c.ContactName
	}

	alreadyFollowed, err := s.Repo.FollowingSet(ctx, userID)
	if err != nil {
		return nil, 0, err
	}

	matches := make([]models.MatchedUser, 0, 8)
	for _, u := range users {
		if u.ID == userID || alreadyFollowed[u.ID] {
			continue
		}
		n := NormalizePhone(u.Mobile)
		if n == "" {
			continue
		}
		name, ok := hashSet[PhoneHash(n)]
		if !ok {
			continue
		}
		m := models.MatchedUser{
			ID:           u.ID,
			Username:     u.Username,
			FullName:     u.FullName,
			ProfilePhoto: u.ProfilePhoto,
			AccountType:  u.AccountType,
			IsVerified:   u.IsVerified,
			MatchSource:  "phone",
			ContactName:  name,
		}
		matches = append(matches, m)
	}

	return matches, int64(len(matches)), nil
}

// PeopleYouMayKnow merges phone matches (highest signal — they're literally
// in your phonebook) with mutual connections (people your follows follow),
// dedupes, and caps the list. No already-followed users, never yourself.
func (s *ContactService) PeopleYouMayKnow(ctx context.Context, userID int64, limit int) ([]models.MatchedUser, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	contacts, err := s.Repo.ListContacts(ctx, userID)
	if err != nil {
		return nil, err
	}
	phoneMatches, _, err := s.MatchByPhone(ctx, userID, contacts)
	if err != nil {
		return nil, err
	}

	mutual, err := s.Repo.MutualConnections(ctx, userID, limit)
	if err != nil {
		return nil, err
	}

	seen := make(map[int64]bool, len(phoneMatches)+len(mutual))
	merged := make([]models.MatchedUser, 0, len(phoneMatches)+len(mutual))
	for _, m := range phoneMatches {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		merged = append(merged, m)
	}
	for _, m := range mutual {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		merged = append(merged, m)
	}

	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

// UpdateSetting flips the master contact-sync toggle. Turning it off also
// wipes the stored book so no contact data lingers on the server.
func (s *ContactService) UpdateSetting(ctx context.Context, userID int64, enabled bool) (models.ContactSyncSetting, error) {
	if err := s.Repo.SetContactSyncEnabled(ctx, userID, enabled); err != nil {
		return models.ContactSyncSetting{}, err
	}
	if !enabled {
		if err := s.Repo.ClearContacts(ctx, userID); err != nil {
			return models.ContactSyncSetting{}, err
		}
	}
	return s.GetSetting(ctx, userID)
}

// GetSetting returns the user's current contact-sync preference, including
// how many of their synced numbers already belong to an account.
func (s *ContactService) GetSetting(ctx context.Context, userID int64) (models.ContactSyncSetting, error) {
	setting, err := s.Repo.GetContactSync(ctx, userID)
	if err != nil {
		return setting, err
	}
	active, err := s.activeContactCount(ctx, userID)
	if err != nil {
		return setting, err
	}
	setting.ActiveCount = active
	return setting, nil
}

// ClearContacts removes all synced contact data without touching the toggle.
func (s *ContactService) ClearContacts(ctx context.Context, userID int64) error {
	return s.Repo.ClearContacts(ctx, userID)
}

// normalizedMobileIndex builds a map of normalized mobile -> user for phone
// matching, preferring the first user that claims a given number.
func (s *ContactService) normalizedMobileIndex(ctx context.Context) (map[string]repository.PhoneUser, error) {
	users, err := s.Repo.AllMobileUsers(ctx)
	if err != nil {
		return nil, err
	}
	byPhone := make(map[string]repository.PhoneUser, len(users))
	for _, u := range users {
		n := NormalizePhone(u.Mobile)
		if n == "" {
			continue
		}
		if _, exists := byPhone[n]; !exists {
			byPhone[n] = u
		}
	}
	return byPhone, nil
}

// activeContactCount counts how many of the user's stored contacts match an
// existing account.
func (s *ContactService) activeContactCount(ctx context.Context, userID int64) (int64, error) {
	contacts, err := s.Repo.ListContacts(ctx, userID)
	if err != nil {
		return 0, err
	}
	if len(contacts) == 0 {
		return 0, nil
	}
	byPhone, err := s.normalizedMobileIndex(ctx)
	if err != nil {
		return 0, err
	}
	var active int64
	for _, c := range contacts {
		if _, ok := byPhone[NormalizePhone(c.ContactPhone)]; ok {
			active++
		}
	}
	return active, nil
}

// ─── helpers ────────────────────────────────────────────────────────────────

// dedupeContacts collapses a batch to one entry per normalized phone,
// keeping the longest/cleanest name and never storing empty phones.
func dedupeContacts(in []models.Contact) []models.Contact {
	type entry struct {
		name  string
		phone string
	}
	seen := make(map[string]entry, len(in)) // phone_hash -> {name, phone}
	for _, c := range in {
		if strings.TrimSpace(c.ContactPhone) == "" || c.PhoneHash == "" {
			continue
		}
		cur, ok := seen[c.PhoneHash]
		if !ok || len(c.ContactName) > len(cur.name) {
			seen[c.PhoneHash] = entry{name: c.ContactName, phone: c.ContactPhone}
		}
	}
	out := make([]models.Contact, 0, len(seen))
	for hash, e := range seen {
		out = append(out, models.Contact{ContactName: e.name, ContactPhone: e.phone, PhoneHash: hash})
	}
	return out
}

func phoneUserToModel(u repository.PhoneUser) *models.ContactUser {
	return &models.ContactUser{
		ID:           u.ID,
		Username:     u.Username,
		FullName:     u.FullName,
		ProfilePhoto: u.ProfilePhoto,
		AccountType:  u.AccountType,
		IsVerified:   u.IsVerified,
	}
}
