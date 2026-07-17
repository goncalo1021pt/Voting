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
