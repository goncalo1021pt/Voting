package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means the address should be rejected
	}{
		{"plain", "bob@example.com", "bob@example.com"},
		{"surrounding whitespace trimmed", "  bob@example.com\t", "bob@example.com"},
		{"subaddressing", "bob+awards@example.com", "bob+awards@example.com"},
		{"subdomain", "bob@mail.example.co.uk", "bob@mail.example.co.uk"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"no at sign", "bobexample.com", ""},
		{"no domain", "bob@", ""},
		{"no local part", "@example.com", ""},
		{"spaces inside", "bob smith@example.com", ""},
		{"display name form", "Bob <bob@example.com>", ""},
		{"angle brackets", "<bob@example.com>", ""},
		{"two addresses", "bob@example.com, eve@example.com", ""},
		{"too long", strings.Repeat("a", maxEmailLen) + "@example.com", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeEmail(tc.in)
			if tc.want == "" {
				if ok {
					t.Errorf("normalizeEmail(%q) = %q, accepted; want rejected", tc.in, got)
				}
				return
			}
			if !ok {
				t.Errorf("normalizeEmail(%q) rejected; want %q", tc.in, tc.want)
				return
			}
			if got != tc.want {
				t.Errorf("normalizeEmail(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A malformed email must be rejected before the handler reaches bcrypt or the
// database, so this passes with no DB configured.
func TestRegisterHandlerRejectsMalformedEmail(t *testing.T) {
	body := `{"username":"bob","email":"not-an-email","password":"hunter2000"}`
	req := httptest.NewRequest("POST", "/auth/register", strings.NewReader(body))
	rec := httptest.NewRecorder()

	RegisterHandler(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d; want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid email address") {
		t.Errorf("body = %q; want it to mention the email", rec.Body.String())
	}
}
