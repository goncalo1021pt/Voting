package main

import (
	"strings"
	"testing"
)

func TestParseInvitationRequest(t *testing.T) {
	intPtr := func(n int) *int { return &n }

	tests := []struct {
		name        string
		body        string
		wantTTL     bool // an expiry was requested
		wantUses    *int // nil means unlimited
		wantErr     bool
		unlimitedOK bool // wantUses nil is the answer, not "don't care"
	}{
		{name: "empty body", body: "", wantUses: intPtr(1)},
		{name: "empty object", body: "{}", wantUses: intPtr(1)},
		{name: "explicit null expiry", body: `{"expires_in_hours":null}`, wantUses: intPtr(1)},
		{name: "one hour", body: `{"expires_in_hours":1}`, wantTTL: true, wantUses: intPtr(1)},
		{name: "one day", body: `{"expires_in_hours":24}`, wantTTL: true, wantUses: intPtr(1)},
		{name: "max allowed ttl", body: `{"expires_in_hours":8760}`, wantTTL: true, wantUses: intPtr(1)},
		{name: "zero ttl", body: `{"expires_in_hours":0}`, wantErr: true},
		{name: "negative ttl", body: `{"expires_in_hours":-5}`, wantErr: true},
		{name: "over max ttl", body: `{"expires_in_hours":8761}`, wantErr: true},
		{name: "not json", body: "not json", wantErr: true},
		{name: "wrong ttl type", body: `{"expires_in_hours":"24"}`, wantErr: true},

		// max_uses: absent is single use, null is unlimited, a number caps it.
		{name: "unlimited uses", body: `{"max_uses":null}`, wantUses: nil, unlimitedOK: true},
		{name: "capped uses", body: `{"max_uses":25}`, wantUses: intPtr(25)},
		{name: "single use spelled out", body: `{"max_uses":1}`, wantUses: intPtr(1)},
		{name: "unlimited with expiry", body: `{"expires_in_hours":24,"max_uses":null}`, wantTTL: true, wantUses: nil, unlimitedOK: true},
		{name: "max allowed uses", body: `{"max_uses":10000}`, wantUses: intPtr(10000)},
		{name: "zero uses", body: `{"max_uses":0}`, wantErr: true},
		{name: "negative uses", body: `{"max_uses":-1}`, wantErr: true},
		{name: "over max uses", body: `{"max_uses":10001}`, wantErr: true},
		{name: "wrong uses type", body: `{"max_uses":"lots"}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseInvitationRequest(strings.NewReader(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseInvitationRequest(%q) error = %v, wantErr %v", tt.body, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if (got.expiresAt != nil) != tt.wantTTL {
				t.Errorf("parseInvitationRequest(%q) expiresAt = %v, wantTTL %v", tt.body, got.expiresAt, tt.wantTTL)
			}
			switch {
			case tt.wantUses == nil && !tt.unlimitedOK:
			case tt.wantUses == nil:
				if got.maxUses != nil {
					t.Errorf("parseInvitationRequest(%q) maxUses = %d, want unlimited", tt.body, *got.maxUses)
				}
			case got.maxUses == nil:
				t.Errorf("parseInvitationRequest(%q) maxUses = unlimited, want %d", tt.body, *tt.wantUses)
			case *got.maxUses != *tt.wantUses:
				t.Errorf("parseInvitationRequest(%q) maxUses = %d, want %d", tt.body, *got.maxUses, *tt.wantUses)
			}
		})
	}
}
