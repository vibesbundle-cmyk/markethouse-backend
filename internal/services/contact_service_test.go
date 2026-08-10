package services

import (
	"testing"

	"markethouse/internal/models"
)

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"08031234567", "2348031234567"},      // local NG number
		{"+2348031234567", "2348031234567"},   // international with +
		{"2348031234567", "2348031234567"},    // already normalized
		{"0803 123 4567", "2348031234567"},    // spaced
		{"0803-123-4567", "2348031234567"},    // dashed
		{"(+234) 803 123 4567", "2348031234567"},
		{"14155551234", "14155551234"},        // already-international kept as-is
		{"", ""},                              // empty
		{"not a phone", ""},                   // no digits
	}
	for _, c := range cases {
		if got := NormalizePhone(c.in); got != c.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPhoneHashStable(t *testing.T) {
	// Different raw formats of the same number normalize identically, so
	// their hashes must match (this is how contacts and users are joined).
	n1 := NormalizePhone("08031234567")
	n2 := NormalizePhone("+234 803 123 4567")
	if n1 != n2 {
		t.Fatalf("normalization mismatch: %q vs %q", n1, n2)
	}
	if PhoneHash(n1) != PhoneHash(n2) {
		t.Errorf("hash mismatch for same normalized number")
	}
	if PhoneHash(n1) == "" {
		t.Error("expected non-empty hash")
	}
}

func TestDedupeContacts(t *testing.T) {
	in := []models.Contact{
		{ContactName: "Ada", ContactPhone: "08031234567", PhoneHash: PhoneHash("2348031234567")},
		{ContactName: "Ada Obi", ContactPhone: "+2348031234567", PhoneHash: PhoneHash("2348031234567")},
		{ContactName: "Bola", ContactPhone: "08099999999", PhoneHash: PhoneHash("2348099999999")},
		{ContactName: "", ContactPhone: "", PhoneHash: ""},
	}
	out := dedupeContacts(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 unique contacts, got %d", len(out))
	}
	// The longer, cleaner name wins.
	for _, c := range out {
		if c.PhoneHash == PhoneHash("2348031234567") && c.ContactName != "Ada Obi" {
			t.Errorf("expected dedupe to keep longer name, got %q", c.ContactName)
		}
	}
}
