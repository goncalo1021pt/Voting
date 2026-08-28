package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Google's OAuth endpoints, hard-coded rather than fetched from the discovery
// document: that would be a network round trip on the path of every login, and
// these URLs have been stable for years.
const (
	googleAuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpoint = "https://oauth2.googleapis.com/token"
)

// Google issues its ID tokens under both spellings.
var googleIssuers = []string{"accounts.google.com", "https://accounts.google.com"}

const (
	// oauthStateCookie carries the CSRF state and the PKCE verifier between the
	// redirect out to Google and the callback. It is scoped to the callback
	// path so it is not attached to any other request.
	oauthStateCookie = "events_oauth"
	oauthStatePath   = "/auth/google"
	oauthStateTTL    = 10 * time.Minute

	// How long the browser has to trade its one-time code for a session.
	oauthExchangeTTL = 2 * time.Minute
)

// googleConfig holds the credentials for the OAuth client. Empty means Google
// sign-in is switched off, which is the default and is not an error: the app
// runs perfectly well with password auth alone.
type googleConfig struct {
	clientID     string
	clientSecret string
	redirectURL  string
}

var googleCfg googleConfig

// oauthClient is used for the server-to-server token exchange. It carries an
// explicit timeout: the default http.Client has none, so a hung Google would
// otherwise pin a request goroutine indefinitely.
var oauthClient = &http.Client{Timeout: 10 * time.Second}

// InitGoogleOAuth reads the Google credentials from the environment. The three
// values are all-or-nothing — a half-configured client fails at the least
// convenient moment, in the middle of a user's login, so refuse to boot
// instead. This mirrors how TRUSTED_PROXY_CIDRS treats a value it cannot parse.
func InitGoogleOAuth() error {
	cfg := googleConfig{
		clientID:     strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID")),
		clientSecret: strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET")),
		redirectURL:  strings.TrimSpace(os.Getenv("GOOGLE_REDIRECT_URL")),
	}

	set := 0
	for _, v := range []string{cfg.clientID, cfg.clientSecret, cfg.redirectURL} {
		if v != "" {
			set++
		}
	}
	if set == 0 {
		return nil // Google sign-in disabled.
	}
	if set != 3 {
		return fmt.Errorf("google oauth is partially configured: GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET and GOOGLE_REDIRECT_URL must all be set, or all be empty")
	}

	u, err := url.Parse(cfg.redirectURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("GOOGLE_REDIRECT_URL is not an absolute URL: %q", cfg.redirectURL)
	}

	googleCfg = cfg
	return nil
}

// googleEnabled reports whether Google sign-in is configured.
func googleEnabled() bool { return googleCfg.clientID != "" }

// oauthCookieSecure mirrors the redirect URL's scheme. In production that is
// https, so the cookie is Secure; a plain-http local build still works.
func oauthCookieSecure() bool {
	return strings.HasPrefix(googleCfg.redirectURL, "https://")
}

// randomToken returns n cryptographically random bytes, hex encoded.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// AuthConfigHandler tells the frontend which sign-in methods exist, so it can
// show the Google button only when the server can actually honour it.
func AuthConfigHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"google": googleEnabled()})
}

// GoogleLoginHandler starts the flow: mint a CSRF state and a PKCE verifier,
// stash both in a short-lived cookie, and bounce the browser to Google.
func GoogleLoginHandler(w http.ResponseWriter, r *http.Request) {
	if !googleEnabled() {
		http.Error(w, "Google sign-in is not configured", http.StatusNotFound)
		return
	}

	state, err := randomToken(32)
	if err != nil {
		serverError(w, r, "Failed to start Google sign-in", err)
		return
	}
	// 64 hex characters, comfortably inside PKCE's 43..128 range and drawn
	// only from the unreserved set the spec allows.
	verifier, err := randomToken(32)
	if err != nil {
		serverError(w, r, "Failed to start Google sign-in", err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state + "." + verifier,
		Path:     oauthStatePath,
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   oauthCookieSecure(),
		// Lax, not Strict: the callback arrives as a top-level navigation from
		// accounts.google.com, and Strict would withhold the cookie on exactly
		// that request, breaking every login.
		SameSite: http.SameSiteLaxMode,
	})

	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {googleCfg.clientID},
		"redirect_uri":          {googleCfg.redirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		// We only ever read the profile once, at sign-in, so there is nothing
		// to refresh and no reason to ask for offline access.
		"access_type": {"online"},
		// Let someone pick which Google account to use rather than silently
		// reusing whichever one the browser is already signed into.
		"prompt": {"select_account"},
	}
	http.Redirect(w, r, googleAuthEndpoint+"?"+q.Encode(), http.StatusFound)
}

// googleClaims is the subset of the ID token we care about.
type googleClaims struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Aud           string `json:"aud"`
	Iss           string `json:"iss"`
	Exp           int64  `json:"exp"`
}

// GoogleCallbackHandler completes the flow and hands the browser a one-time
// code. Every failure path lands on the login page rather than showing a raw
// error: the cause is already in the server log under this request's id.
func GoogleCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if !googleEnabled() {
		http.Error(w, "Google sign-in is not configured", http.StatusNotFound)
		return
	}

	// The state cookie is single-use whatever happens next.
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    "",
		Path:     oauthStatePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   oauthCookieSecure(),
		SameSite: http.SameSiteLaxMode,
	})

	if e := r.URL.Query().Get("error"); e != "" {
		// Usually access_denied — the user pressed Cancel. Not an error worth
		// logging at ERROR level.
		oauthFail(w, r)
		return
	}

	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil {
		logOAuthProblem(r, "missing state cookie (expired, or third-party cookies blocked)")
		oauthFail(w, r)
		return
	}
	state, verifier, ok := strings.Cut(cookie.Value, ".")
	if !ok || state == "" || verifier == "" {
		logOAuthProblem(r, "malformed state cookie")
		oauthFail(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 {
		logOAuthProblem(r, "state mismatch — possible CSRF, or a stale tab")
		oauthFail(w, r)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		logOAuthProblem(r, "callback carried no authorization code")
		oauthFail(w, r)
		return
	}

	claims, err := exchangeGoogleCode(r.Context(), code, verifier)
	if err != nil {
		logOAuthProblem(r, "token exchange failed: "+err.Error())
		oauthFail(w, r)
		return
	}

	user, err := UpsertGoogleUserInDB(claims.Sub, claims.Email, claims.Name, claims.EmailVerified)
	if err != nil {
		logOAuthProblem(r, "could not resolve the Google identity to a user: "+err.Error())
		// The one failure the person in front of the browser can actually do
		// something about: the address is already a password account, so tell
		// them to sign in that way instead of showing a generic failure.
		if errors.Is(err, ErrEmailBelongsToPasswordAccount) {
			http.Redirect(w, r, "/#/auth/google/error-email", http.StatusFound)
			return
		}
		oauthFail(w, r)
		return
	}

	exchange, err := randomToken(32)
	if err != nil {
		serverError(w, r, "Failed to complete Google sign-in", err)
		return
	}
	if err := CreateOAuthExchangeInDB(exchange, user.ID); err != nil {
		serverError(w, r, "Failed to complete Google sign-in", err)
		return
	}

	// The code travels in the fragment-based route, and is worthless after one
	// use and two minutes.
	http.Redirect(w, r, "/#/auth/google/"+exchange, http.StatusFound)
}

// logOAuthProblem records why a sign-in was refused. These are WARN, not
// ERROR: a cancelled consent screen or a tab left open past the state cookie's
// lifetime is a normal thing for a user to do, not a fault in the server. The
// request id ties the line to the access log.
func logOAuthProblem(r *http.Request, detail string) {
	log.Printf("req=%s WARN google sign-in: %s", requestID(r), detail)
}

// oauthFail sends the browser back to the login page.
func oauthFail(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/#/auth/google/error", http.StatusFound)
}

// exchangeGoogleCode trades the authorization code for an ID token and returns
// its claims.
func exchangeGoogleCode(ctx context.Context, code, verifier string) (*googleClaims, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {googleCfg.clientID},
		"client_secret": {googleCfg.clientSecret},
		"redirect_uri":  {googleCfg.redirectURL},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := oauthClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google returned %s", resp.Status)
	}

	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("unreadable token response: %w", err)
	}
	if body.IDToken == "" {
		return nil, fmt.Errorf("token response carried no id_token")
	}
	return parseGoogleIDToken(body.IDToken)
}

// parseGoogleIDToken decodes the ID token's claims and checks the ones that
// matter.
//
// The signature is deliberately not verified. This token did not come from the
// browser: it was just fetched over a verified TLS connection directly from
// Google's token endpoint, which is the case Google's own documentation calls
// out as not needing local validation. Verifying would mean fetching and
// caching Google's JWKS and tracking key rotation, all to re-establish
// something TLS already established. The audience, issuer and expiry checks
// below stay because they are free and catch a misconfigured client.
func parseGoogleIDToken(idToken string) (*googleClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("id_token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("undecodable id_token payload: %w", err)
	}
	var c googleClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("unparseable id_token claims: %w", err)
	}

	if c.Aud != googleCfg.clientID {
		return nil, fmt.Errorf("id_token audience is not this client")
	}
	issuerOK := false
	for _, iss := range googleIssuers {
		if c.Iss == iss {
			issuerOK = true
			break
		}
	}
	if !issuerOK {
		return nil, fmt.Errorf("id_token issuer %q is not Google", c.Iss)
	}
	if c.Exp > 0 && time.Now().After(time.Unix(c.Exp, 0)) {
		return nil, fmt.Errorf("id_token has expired")
	}
	if c.Sub == "" {
		return nil, fmt.Errorf("id_token carried no subject")
	}
	return &c, nil
}

// GoogleExchangeHandler trades a one-time code for a real session, so the
// session token is delivered in a response body rather than a URL.
func GoogleExchangeHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	userID, err := ConsumeOAuthExchangeInDB(req.Code)
	if err != nil {
		http.Error(w, "This sign-in link has already been used or has expired", http.StatusUnauthorized)
		return
	}

	user, err := GetUserByIDFromDB(userID)
	if err != nil {
		serverError(w, r, "Failed to complete Google sign-in", err)
		return
	}

	token, err := issueSession(userID)
	if err != nil {
		serverError(w, r, "Failed to create session", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AuthResponse{User: *user, Token: token})
}
