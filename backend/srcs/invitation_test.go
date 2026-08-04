package main

import (
	"strings"
	"testing"
)

func TestParseInvitationExpiry(t *testing.T) {
	intPtr := func(n int) *int { return &n }

	tests := []struct {
		name    string
		body    string
		want    *int // nil means no expiry
		wantErr bool
	}{
		{"empty body", "", nil, false},
		{"empty object", "{}", nil, false},
		{"explicit null", `{"expires_in_hours":null}`, nil, false},
		{"one hour", `{"expires_in_hours":1}`, intPtr(1), false},
		{"one day", `{"expires_in_hours":24}`, intPtr(24), false},
		{"max allowed", `{"expires_in_hours":8760}`, intPtr(8760), false},
		{"zero", `{"expires_in_hours":0}`, nil, true},
		{"negative", `{"expires_in_hours":-5}`, nil, true},
		{"over max", `{"expires_in_hours":8761}`, nil, true},
		{"not json", "not json", nil, true},
		{"wrong type", `{"expires_in_hours":"24"}`, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseInvitationExpiry(strings.NewReader(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseInvitationExpiry(%q) error = %v, wantErr %v", tt.body, err, tt.wantErr)
			}
			if tt.want == nil && got != nil {
				t.Fatalf("parseInvitationExpiry(%q) = %d, want nil", tt.body, *got)
			}
			if tt.want != nil && (got == nil || *got != *tt.want) {
				t.Fatalf("parseInvitationExpiry(%q) = %v, want %d", tt.body, got, *tt.want)
			}
		})
	}
}
