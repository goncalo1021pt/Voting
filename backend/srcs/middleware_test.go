package main

import (
	"testing"
	"time"
)

func TestRateLimiterAllowsUpToLimit(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	now := time.Unix(1000, 0)

	for i := 0; i < 3; i++ {
		if !rl.allow("1.2.3.4", now) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if rl.allow("1.2.3.4", now) {
		t.Fatal("4th request within window should be denied")
	}
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	now := time.Unix(1000, 0)

	if !rl.allow("ip", now) {
		t.Fatal("first request should be allowed")
	}
	if rl.allow("ip", now.Add(30*time.Second)) {
		t.Fatal("second request inside window should be denied")
	}
	if !rl.allow("ip", now.Add(2*time.Minute)) {
		t.Fatal("request after window should be allowed again")
	}
}

func TestRateLimiterIsolatesKeys(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	now := time.Unix(1000, 0)

	if !rl.allow("a", now) || !rl.allow("b", now) {
		t.Fatal("distinct keys should each get their own budget")
	}
	if rl.allow("a", now) {
		t.Fatal("key a should now be limited")
	}
}

func TestValidateEventShape(t *testing.T) {
	ok := CreateEventRequest{
		Name: "Game Awards",
		Categories: []CreateCategoryRequest{
			{Name: "Game of the Year", Options: []string{"A", "B"}},
		},
	}
	if msg, valid := validateEventShape(ok); !valid {
		t.Fatalf("expected valid event, got: %s", msg)
	}

	tooManyCats := CreateEventRequest{Name: "x"}
	for i := 0; i <= maxCategories; i++ {
		tooManyCats.Categories = append(tooManyCats.Categories, CreateCategoryRequest{Name: "c"})
	}
	if _, valid := validateEventShape(tooManyCats); valid {
		t.Fatal("expected too-many-categories to be rejected")
	}

	emptyCatName := CreateEventRequest{
		Name:       "x",
		Categories: []CreateCategoryRequest{{Name: "", Options: []string{"A"}}},
	}
	if _, valid := validateEventShape(emptyCatName); valid {
		t.Fatal("expected empty category name to be rejected")
	}
}
