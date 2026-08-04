package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectAssetVersion(t *testing.T) {
	in := `<link rel="stylesheet" href="/styles.css"><script src="/app.js"></script>`
	out := injectAssetVersion(in, "abc123")
	if !strings.Contains(out, `href="/styles.css?v=abc123"`) {
		t.Errorf("stylesheet not fingerprinted: %s", out)
	}
	if !strings.Contains(out, `src="/app.js?v=abc123"`) {
		t.Errorf("script not fingerprinted: %s", out)
	}
}

// seedFrontend writes a throwaway frontend dir and points frontendDir at it.
func seedFrontend(t *testing.T, css, js string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", `<link rel="stylesheet" href="/styles.css"><script src="/app.js"></script>`)
	write("styles.css", css)
	write("app.js", js)
	old := frontendDir
	frontendDir = dir
	t.Cleanup(func() { frontendDir = old })
}

func TestServeFrontendCaching(t *testing.T) {
	seedFrontend(t, "body{}", "//app")
	InitAssetVersion()
	if assetVersion == "" || assetVersion == "dev" {
		t.Fatalf("assetVersion not computed: %q", assetVersion)
	}

	// "/" -> index.html: no-cache HTML with fingerprinted asset URLs.
	rec := httptest.NewRecorder()
	serveFrontend(rec, httptest.NewRequest("GET", "/", nil))
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", got)
	}
	if !strings.Contains(rec.Body.String(), "styles.css?v="+assetVersion) {
		t.Errorf("index missing fingerprint: %s", rec.Body.String())
	}

	// SPA fallback for an unknown client route also renders index.
	rec = httptest.NewRecorder()
	serveFrontend(rec, httptest.NewRequest("GET", "/events/5", nil))
	if !strings.Contains(rec.Body.String(), "app.js?v="+assetVersion) {
		t.Errorf("SPA fallback missing fingerprint")
	}

	// Fingerprinted asset -> immutable long cache.
	rec = httptest.NewRecorder()
	serveFrontend(rec, httptest.NewRequest("GET", "/styles.css?v="+assetVersion, nil))
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("fingerprinted asset Cache-Control = %q, want immutable", got)
	}

	// Bare asset request -> revalidate each load.
	rec = httptest.NewRecorder()
	serveFrontend(rec, httptest.NewRequest("GET", "/app.js", nil))
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("bare asset Cache-Control = %q, want no-cache", got)
	}
}

func TestAssetVersionChangesWithContent(t *testing.T) {
	seedFrontend(t, "body{}", "//app")
	InitAssetVersion()
	v1 := assetVersion
	InitAssetVersion() // same content
	if assetVersion != v1 {
		t.Errorf("version not stable for identical content: %q vs %q", v1, assetVersion)
	}

	seedFrontend(t, "body{color:red}", "//app") // css changed
	InitAssetVersion()
	if assetVersion == v1 {
		t.Errorf("version should change when content changes")
	}
}

func TestIsExactEventPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/events/5", true},
		{"/events/123", true},
		{"/events/abc", true}, // shape only; the handler's Atoi rejects it
		{"/events/", false},
		{"/events/5/members", false},
		{"/events/5/members/3", false},
		{"/events/5/invitations/tok", false},
		{"/events/5/anything", false},
	}
	for _, tt := range tests {
		if got := isExactEventPath(tt.path); got != tt.want {
			t.Errorf("isExactEventPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// A DELETE with trailing segments must never reach DeleteEventHandler. Routed
// destructive paths respond 401 without a token; anything that falls through
// to the default arm responds 404 — which is how we tell them apart with no DB.
func TestDeleteEventRouteRejectsSubpaths(t *testing.T) {
	tests := []struct {
		path       string
		wantStatus int
	}{
		{"/events/5", 401},                  // exact shape: routed, auth rejects
		{"/events/5/members", 404},          // malformed member removal: not routed
		{"/events/5/typo", 404},             // garbage subpath: not routed
		{"/events/5/members/3", 401},        // real member-removal route still matches
		{"/events/5/invitations/tok", 401},  // real invitation-revoke route still matches
	}
	for _, tt := range tests {
		req := httptest.NewRequest("DELETE", tt.path, nil)
		rec := httptest.NewRecorder()
		RouteHandler(rec, req)
		if rec.Code != tt.wantStatus {
			t.Errorf("DELETE %s = %d, want %d", tt.path, rec.Code, tt.wantStatus)
		}
	}
}
