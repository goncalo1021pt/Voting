package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// makeIDToken builds a JWT-shaped string with the given claims. The signature
// is nonsense on purpose: parseGoogleIDToken does not verify it (the token is
// fetched directly from Google over TLS), and these tests would silently stop
// covering the claim checks if it did.
func makeIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc(payload) + ".not-a-signature"
}

func validClaims() map[string]any {
	return map[string]any{
		"sub":            "1234567890",
		"email":          "voter@example.com",
		"email_verified": true,
		"name":           "A Voter",
		"aud":            "test-client-id",
		"iss":            "https://accounts.google.com",
		"exp":            time.Now().Add(time.Hour).Unix(),
	}
}

func TestParseGoogleIDTokenAcceptsAValidToken(t *testing.T) {
	googleCfg = googleConfig{clientID: "test-client-id"}
	t.Cleanup(func() { googleCfg = googleConfig{} })

	got, err := parseGoogleIDToken(makeIDToken(t, validClaims()))
	if err != nil {
		t.Fatalf("parseGoogleIDToken() error = %v, want nil", err)
	}
	if got.Sub != "1234567890" {
		t.Errorf("Sub = %q, want %q", got.Sub, "1234567890")
	}
	if got.Email != "voter@example.com" {
		t.Errorf("Email = %q, want %q", got.Email, "voter@example.com")
	}
	if !got.EmailVerified {
		t.Error("EmailVerified = false, want true")
	}
}

// The bare issuer spelling is the other form Google uses.
func TestParseGoogleIDTokenAcceptsBareIssuer(t *testing.T) {
	googleCfg = googleConfig{clientID: "test-client-id"}
	t.Cleanup(func() { googleCfg = googleConfig{} })

	c := validClaims()
	c["iss"] = "accounts.google.com"
	if _, err := parseGoogleIDToken(makeIDToken(t, c)); err != nil {
		t.Fatalf("bare issuer rejected: %v", err)
	}
}

func TestParseGoogleIDTokenRejectsBadTokens(t *testing.T) {
	googleCfg = googleConfig{clientID: "test-client-id"}
	t.Cleanup(func() { googleCfg = googleConfig{} })

	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		// A token minted for a different OAuth client must not authenticate
		// anyone here, even though it is a genuine, correctly signed Google
		// token. This is the check that stops one.
		{"audience of another client", func(c map[string]any) { c["aud"] = "someone-elses-client" }, "audience"},
		{"issuer is not google", func(c map[string]any) { c["iss"] = "https://evil.example" }, "issuer"},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, "expired"},
		{"no subject", func(c map[string]any) { delete(c, "sub") }, "subject"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validClaims()
			tc.mutate(c)
			_, err := parseGoogleIDToken(makeIDToken(t, c))
			if err == nil {
				t.Fatalf("parseGoogleIDToken() error = nil, want one mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestParseGoogleIDTokenRejectsMalformedInput(t *testing.T) {
	googleCfg = googleConfig{clientID: "test-client-id"}
	t.Cleanup(func() { googleCfg = googleConfig{} })

	for _, tok := range []string{"", "one-part", "two.parts", "a.!!!not-base64!!!.c"} {
		if _, err := parseGoogleIDToken(tok); err == nil {
			t.Errorf("parseGoogleIDToken(%q) error = nil, want an error", tok)
		}
	}
}

// All three variables or none. A half-configured client would otherwise boot
// happily and fail in the middle of a user's login.
func TestInitGoogleOAuthIsAllOrNothing(t *testing.T) {
	t.Cleanup(func() { googleCfg = googleConfig{} })

	tests := []struct {
		name                 string
		id, secret, redirect string
		wantErr              bool
		wantEnabled          bool
	}{
		{name: "unset leaves it disabled"},
		{
			name: "all three enables it",
			id:   "id", secret: "secret", redirect: "https://voting.example/auth/google/callback",
			wantEnabled: true,
		},
		{name: "id only", id: "id", wantErr: true},
		{name: "missing redirect", id: "id", secret: "secret", wantErr: true},
		{
			name: "redirect must be absolute",
			id:   "id", secret: "secret", redirect: "/auth/google/callback",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			googleCfg = googleConfig{}
			t.Setenv("GOOGLE_CLIENT_ID", tc.id)
			t.Setenv("GOOGLE_CLIENT_SECRET", tc.secret)
			t.Setenv("GOOGLE_REDIRECT_URL", tc.redirect)

			err := InitGoogleOAuth()
			if tc.wantErr {
				if err == nil {
					t.Fatal("InitGoogleOAuth() error = nil, want an error")
				}
				if googleEnabled() {
					t.Error("googleEnabled() = true after a failed init, want false")
				}
				return
			}
			if err != nil {
				t.Fatalf("InitGoogleOAuth() error = %v, want nil", err)
			}
			if googleEnabled() != tc.wantEnabled {
				t.Errorf("googleEnabled() = %v, want %v", googleEnabled(), tc.wantEnabled)
			}
		})
	}
}

// The cookie must only be marked Secure when the deployment is actually https,
// or a local http build could never complete a login.
func TestOAuthCookieSecureFollowsRedirectScheme(t *testing.T) {
	t.Cleanup(func() { googleCfg = googleConfig{} })

	googleCfg = googleConfig{redirectURL: "https://voting.example/auth/google/callback"}
	if !oauthCookieSecure() {
		t.Error("oauthCookieSecure() = false for an https redirect, want true")
	}
	googleCfg = googleConfig{redirectURL: "http://localhost:8080/auth/google/callback"}
	if oauthCookieSecure() {
		t.Error("oauthCookieSecure() = true for an http redirect, want false")
	}
}
