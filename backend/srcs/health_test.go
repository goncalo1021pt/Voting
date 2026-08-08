package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// /healthz has to be reachable without credentials — Docker's probe has none —
// and must answer HEAD, which is what a bare `curl -I` probe sends.
func TestHealthRouteIsPublicAndAnswersGetAndHead(t *testing.T) {
	withTestDB(t)

	for _, method := range []string{"GET", "HEAD"} {
		rec := httptest.NewRecorder()
		RouteHandler(rec, httptest.NewRequest(method, healthPath, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s %s = %d, want 200", method, healthPath, rec.Code)
		}
	}
}

func TestHealthReportsUnavailableWhenDatabaseIsDown(t *testing.T) {
	withTestDB(t)

	// Close the pool underneath the handler: the process is up, the database
	// is not. That's exactly the state the probe exists to catch.
	if err := db.Close(); err != nil {
		t.Fatalf("close pool: %v", err)
	}

	rec := httptest.NewRecorder()
	RouteHandler(rec, httptest.NewRequest("GET", healthPath, nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// The pre-existing GET-only guard made `curl -sI http://localhost:8080/` return
// 404 — the smoke test DEPLOYMENT.md tells operators to run.
func TestFrontendServesHeadRequests(t *testing.T) {
	prev := frontendDir
	frontendDir = "../../frontend"
	t.Cleanup(func() { frontendDir = prev })

	for _, method := range []string{"GET", "HEAD"} {
		rec := httptest.NewRecorder()
		RouteHandler(rec, httptest.NewRequest(method, "/", nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s / = %d, want 200", method, rec.Code)
		}
	}
}

// A method the router doesn't serve still 404s; widening to HEAD must not have
// turned the fallback into a catch-all.
func TestUnhandledMethodStillNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	RouteHandler(rec, httptest.NewRequest("PUT", "/anything", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("PUT /anything = %d, want 404", rec.Code)
	}
}

// Successful probes are dropped from the access log so they don't bury real
// traffic; a failing probe must still be logged.
func TestHealthLoggingIsQuietWhenHealthyAndLoudWhenNot(t *testing.T) {
	withTestDB(t)

	buf := captureLog(t)
	handler := LogMiddleware(http.HandlerFunc(RouteHandler))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", healthPath, nil))

	if strings.TrimSpace(buf.String()) != "" {
		t.Errorf("healthy probe should not be logged, got %q", buf.String())
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close pool: %v", err)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", healthPath, nil))

	if !strings.Contains(buf.String(), healthPath) {
		t.Errorf("failing probe should be logged, got %q", buf.String())
	}
}
