package main

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLog redirects the standard logger into a buffer for the duration of
// the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

func TestLogMiddlewareRecordsRequest(t *testing.T) {
	buf := captureLog(t)

	handler := LogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("hello"))
	}))

	req := httptest.NewRequest("GET", "/events", nil)
	req.Header.Set("CF-Connecting-IP", "203.0.113.7")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	line := buf.String()
	for _, want := range []string{"GET", "/events", "418", "5B", "ip=203.0.113.7", "req="} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q missing %q", strings.TrimSpace(line), want)
		}
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("X-Request-Id header not set")
	}
}

// A handler that writes a body without calling WriteHeader has sent a 200, and
// so has one that writes nothing at all.
func TestLogMiddlewareDefaultsToStatusOK(t *testing.T) {
	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"implicit via Write", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hi")) }},
		{"no write at all", func(w http.ResponseWriter, r *http.Request) {}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureLog(t)
			LogMiddleware(tt.handler).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
			if !strings.Contains(buf.String(), " 200 ") {
				t.Errorf("expected status 200 in log, got %q", strings.TrimSpace(buf.String()))
			}
		})
	}
}

// Invitation tokens grant access to an invite-only event. They must not end up
// in the access log.
func TestRedactPathHidesInvitationTokens(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/invitations/s3cr3t-token", "/invitations/***"},
		{"/events/5/invitations/s3cr3t-token", "/events/5/invitations/***"},
		{"/events/5/invitations", "/events/5/invitations"}, // list/create carry no token
		{"/events/5/members/3", "/events/5/members/3"},
		{"/events", "/events"},
		{"/", "/"},
	}
	for _, tt := range tests {
		if got := redactPath(tt.path); got != tt.want {
			t.Errorf("redactPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestLogMiddlewareRedactsTokenInPath(t *testing.T) {
	buf := captureLog(t)

	handler := LogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/invitations/s3cr3t-token", nil))

	if strings.Contains(buf.String(), "s3cr3t-token") {
		t.Errorf("invitation token leaked into log: %q", strings.TrimSpace(buf.String()))
	}
	if !strings.Contains(buf.String(), "/invitations/***") {
		t.Errorf("expected redacted path in log, got %q", strings.TrimSpace(buf.String()))
	}
}

// serverError must tell the operator what happened without telling the client.
func TestServerErrorLogsCauseButHidesItFromClient(t *testing.T) {
	buf := captureLog(t)

	req := httptest.NewRequest("POST", "/events", nil)
	rec := httptest.NewRecorder()
	serverError(rec, req, "Failed to create event", errors.New("pq: connection refused"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "connection refused") {
		t.Errorf("underlying error leaked to client: %q", body)
	}
	if !strings.Contains(buf.String(), "connection refused") {
		t.Errorf("underlying error not logged: %q", strings.TrimSpace(buf.String()))
	}
}

// The access-log line and any error lines from the same request must share an
// ID, otherwise they can't be correlated under concurrent traffic.
func TestRequestIDTiesErrorLineToAccessLine(t *testing.T) {
	buf := captureLog(t)

	handler := LogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverError(w, r, "Failed to fetch events", errors.New("boom"))
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/events", nil))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected an error line and an access line, got %d: %q", len(lines), buf.String())
	}
	idOf := func(line string) string {
		for _, f := range strings.Fields(line) {
			if strings.HasPrefix(f, "req=") {
				return f
			}
		}
		return ""
	}
	errID, accessID := idOf(lines[0]), idOf(lines[1])
	if errID == "" || errID != accessID {
		t.Errorf("request IDs differ: error=%q access=%q", errID, accessID)
	}
}

// Handlers called directly in tests have no middleware-assigned ID; that must
// not panic or produce a confusing line.
func TestRequestIDWithoutMiddleware(t *testing.T) {
	if got := requestID(httptest.NewRequest("GET", "/", nil)); got != "-" {
		t.Errorf("requestID without middleware = %q, want %q", got, "-")
	}
}
