package models

// IncomingContact is a single address-book entry sent by the client during
// a sync. Phone is stored as entered by the OS (e.g. "+234 803 123 4567"
// or "08031234567"); the service normalizes it to E.164 before storing.
type IncomingContact struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

// Contact is a row from user_contacts (the user's imported address book).
type Contact struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"user_id"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	PhoneHash    string `json:"-"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// ContactUser is the slim public shape of a user attached to a contact view.
type ContactUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	FullName     string `json:"full_name"`
	ProfilePhoto string `json:"profile_photo"`
	AccountType  string `json:"account_type"`
	IsVerified   bool   `json:"is_verified"`
}

// ContactView is a contact returned to the client, with an "active" flag
// set when the phone number matches an existing MarketHouse account.
type ContactView struct {
	ID           int64        `json:"id"`
	ContactName  string       `json:"contact_name"`
	ContactPhone string       `json:"contact_phone"`
	IsActive     bool         `json:"is_active"`
	MatchedUser  *ContactUser `json:"matched_user,omitempty"`
}

// MatchedUser is a user surfaced in "People You May Know".
type MatchedUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	FullName     string `json:"full_name"`
	ProfilePhoto string `json:"profile_photo"`
	AccountType  string `json:"account_type"`
	IsVerified   bool   `json:"is_verified"`
	MutualCount  int    `json:"mutual_count"`
	MatchSource  string `json:"match_source"` // "phone" | "mutual"
	ContactName  string `json:"contact_name,omitempty"`
}

// ContactSyncSetting is the user's contact-sync preference + summary.
type ContactSyncSetting struct {
	ContactSyncEnabled bool   `json:"contact_sync_enabled"`
	ContactSyncAt      string `json:"contact_sync_at,omitempty"`
	SyncedCount        int64  `json:"synced_count"`
	ActiveCount        int64  `json:"active_count"`
}

// SyncResult is returned by POST /contacts/sync.
type SyncResult struct {
	SyncedCount   int64         `json:"synced_count"`
	TotalContacts int64         `json:"total_contacts"`
	ActiveCount   int64         `json:"active_count"`
	ActiveMatches []MatchedUser `json:"active_matches"`
}
