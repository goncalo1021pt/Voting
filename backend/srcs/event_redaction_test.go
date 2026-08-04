package main

import "testing"

func fullEvent(visibility string, isMember bool) *Event {
	return &Event{
		ID:                7,
		HostID:            1,
		Name:              "Secret Gala",
		Description:       "hush",
		Visibility:        visibility,
		ResultsVisibility: "live",
		IsActive:          true,
		IsMember:          isMember,
		MyVotes:           map[int]int{1: 2},
		RequireFullBallot: true,
		MemberCount:       12,
		VoterCount:        5,
		Categories:        []Category{{ID: 1, EventID: 7, Name: "Best"}},
	}
}

func TestRedactEventForViewer(t *testing.T) {
	tests := []struct {
		name     string
		event    *Event
		viewerID int
		wantFull bool
	}{
		{"host sees full", fullEvent("invite-only", true), 1, true},
		{"member sees full", fullEvent("invite-only", true), 42, true},
		{"public event, anonymous sees full", fullEvent("public", false), 0, true},
		{"public event, non-member sees full", fullEvent("public", false), 42, true},
		{"invite-only, anonymous redacted", fullEvent("invite-only", false), 0, false},
		{"invite-only, authed non-member redacted", fullEvent("invite-only", false), 42, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactEventForViewer(tt.event, tt.viewerID)
			if tt.wantFull {
				if got != tt.event {
					t.Fatal("expected the event returned unmodified")
				}
				return
			}
			if got == tt.event {
				t.Fatal("expected a redacted copy, got the original")
			}
			// The access-wall shell survives...
			if got.ID != tt.event.ID || got.Name != tt.event.Name ||
				got.Visibility != tt.event.Visibility || got.IsActive != tt.event.IsActive ||
				got.HostID != tt.event.HostID || !got.CreatedAt.Equal(tt.event.CreatedAt) {
				t.Errorf("shell fields altered: %+v", got)
			}
			// ...and everything members-only is gone.
			if got.Description != "" || got.Categories != nil || got.MyVotes != nil ||
				got.MemberCount != 0 || got.VoterCount != 0 ||
				got.ResultsVisibility != "" || got.RequireFullBallot || got.IsMember {
				t.Errorf("members-only fields leaked: %+v", got)
			}
		})
	}
}
