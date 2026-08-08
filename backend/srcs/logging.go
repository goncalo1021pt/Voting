package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"
)

// contextKey is unexported so nothing outside this package can collide with
// the keys stored on a request context.
type contextKey int

const requestIDContextKey contextKey = iota

// newRequestID returns a short random identifier. It only has to be unique
// enough to join an access-log line to the error lines from the same request,
// so 4 bytes is plenty.
func newRequestID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Degrading to a constant is better than failing the request; the
		// access log still records the status and path.
		return "????????"
	}
	return hex.EncodeToString(buf[:])
}

// requestID returns the identifier assigned by LogMiddleware, or "-" if the
// request did not pass through it (tests calling handlers directly).
func requestID(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDContextKey).(string); ok {
		return id
	}
	return "-"
}

// statusRecorder captures what the handler wrote so the access log can report
// it. net/http gives the middleware no other way to see the status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rec *statusRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	// A handler that writes without calling WriteHeader implies 200.
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// redactPath strips invitation tokens out of a request path. Those tokens are
// credentials — anyone holding one can join an invite-only event — and an
// access log is not a place to keep credentials. Everything else is logged
// as-is; the query string is never logged at all.
func redactPath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		// /invitations/{token}          — redemption
		// /events/{id}/invitations/{tok} — revocation
		if seg == "invitations" && i+1 < len(segments) && segments[i+1] != "" {
			segments[i+1] = "***"
		}
	}
	return strings.Join(segments, "/")
}

// LogMiddleware writes one line per request: method, path, status, duration,
// response size and client IP, tagged with a request ID that serverError
// repeats. Without it a production 500 left no trace at all.
func LogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := newRequestID()

		r = r.WithContext(context.WithValue(r.Context(), requestIDContextKey, id))
		// Echoed so a user reporting a failure can quote the ID from their
		// browser's network tab and have it match a log line.
		w.Header().Set("X-Request-Id", id)

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		// A handler that returns without writing anything still sent a 200.
		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		log.Printf("req=%s %s %s %d %s %dB ip=%s",
			id,
			r.Method,
			redactPath(r.URL.Path),
			rec.status,
			// Microsecond, not millisecond: static-asset hits are sub-ms and
			// would all log as "0s".
			time.Since(start).Round(time.Microsecond),
			rec.bytes,
			clientIP(r),
		)
	})
}

// serverError logs the underlying cause and sends the generic message to the
// client. The client learns nothing it shouldn't; the operator gets the error
// that actually happened, tied by request ID to the access-log line.
func serverError(w http.ResponseWriter, r *http.Request, message string, err error) {
	log.Printf("req=%s ERROR %s %s: %s: %v", requestID(r), r.Method, redactPath(r.URL.Path), message, err)
	http.Error(w, message, http.StatusInternalServerError)
}
