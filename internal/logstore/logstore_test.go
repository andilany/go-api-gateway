package logstore

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSanitizeHeaders_RedactsSensitiveAndJoinsOthers(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret-token")
	h.Add("Cookie", "a=b")
	h.Add("Cookie", "c=d")
	h.Set("X-Api-Key", "super-secret")
	h.Add("X-Custom", "one")
	h.Add("X-Custom", "two")

	got := SanitizeHeaders(h)

	if got["Authorization"] != "[REDACTED]" {
		t.Errorf("Authorization: got %q, want [REDACTED]", got["Authorization"])
	}
	if got["Cookie"] != "[REDACTED]" {
		t.Errorf("Cookie: got %q, want [REDACTED]", got["Cookie"])
	}
	if got["X-Api-Key"] != "[REDACTED]" {
		t.Errorf("X-Api-Key: got %q, want [REDACTED]", got["X-Api-Key"])
	}
	if got["X-Custom"] != "one, two" {
		t.Errorf("X-Custom: got %q, want %q", got["X-Custom"], "one, two")
	}
}

func TestSanitizeHeaders_NilAndEmpty(t *testing.T) {
	gotNil := SanitizeHeaders(nil)
	if gotNil == nil || len(gotNil) != 0 {
		t.Fatalf("SanitizeHeaders(nil): got %#v, want empty non-nil map", gotNil)
	}

	h := http.Header{}
	gotEmpty := SanitizeHeaders(h)
	if gotEmpty == nil || len(gotEmpty) != 0 {
		t.Fatalf("SanitizeHeaders(empty): got %#v, want empty non-nil map", gotEmpty)
	}
}

// TestLogRequest_InsertsRowWithExpectedFields is an integration test that inserts a log entry
// and verifies the row in request_logs has all expected fields (including sanitized headers).
// Skipped when DATABASE_URL is not set.
func TestLogRequest_InsertsRowWithExpectedFields(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping logstore integration test")
	}

	ctx := context.Background()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cwd = filepath.Join(cwd, "..", "..")
	dir := filepath.Join(cwd, "migrations")
	sourceURL := "file://" + filepath.ToSlash(dir)
	m, err := migrate.New(sourceURL, dbURL)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	m.Close()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("database ping: %v", err)
	}

	_, _ = pool.Exec(ctx, `TRUNCATE TABLE request_logs RESTART IDENTITY CASCADE`)

	s := New(pool, nil)
	entry := Entry{
		RouteName:       "test-route",
		Method:          "GET",
		Path:            "/api/foo",
		StatusCode:      200,
		DurationMs:      42,
		RequestHeaders:  map[string]string{"Authorization": "[REDACTED]", "X-Request-ID": "req-1"},
		ResponseHeaders: map[string]string{"Content-Type": "application/json"},
	}
	if err := s.LogRequest(ctx, entry); err != nil {
		t.Fatalf("LogRequest: %v", err)
	}

	var (
		logRouteName  string
		logMethod     string
		logPath       string
		logStatusCode *int
		logDurationMs *int64
		reqHeadersRaw []byte
		respHeadersRaw []byte
	)
	err = pool.QueryRow(ctx,
		`SELECT route_name, method, path, status_code, duration_ms, request_headers, response_headers
		 FROM request_logs WHERE route_name = $1 LIMIT 1`,
		entry.RouteName).
		Scan(&logRouteName, &logMethod, &logPath, &logStatusCode, &logDurationMs, &reqHeadersRaw, &respHeadersRaw)
	if err != nil {
		t.Fatalf("select from request_logs: %v", err)
	}

	if logRouteName != entry.RouteName {
		t.Errorf("route_name: got %q, want %q", logRouteName, entry.RouteName)
	}
	if logMethod != entry.Method {
		t.Errorf("method: got %q, want %q", logMethod, entry.Method)
	}
	if logPath != entry.Path {
		t.Errorf("path: got %q, want %q", logPath, entry.Path)
	}
	if logStatusCode == nil || *logStatusCode != entry.StatusCode {
		t.Errorf("status_code: got %v, want %d", logStatusCode, entry.StatusCode)
	}
	if logDurationMs == nil || *logDurationMs != entry.DurationMs {
		t.Errorf("duration_ms: got %v, want %d", logDurationMs, entry.DurationMs)
	}
	var reqHeaders map[string]string
	if len(reqHeadersRaw) > 0 {
		if err := json.Unmarshal(reqHeadersRaw, &reqHeaders); err != nil {
			t.Fatalf("unmarshal request_headers: %v", err)
		}
	}
	if reqHeaders["Authorization"] != "[REDACTED]" {
		t.Errorf("request_headers Authorization: got %q, want [REDACTED]", reqHeaders["Authorization"])
	}
	if reqHeaders["X-Request-ID"] != "req-1" {
		t.Errorf("request_headers X-Request-ID: got %q, want req-1", reqHeaders["X-Request-ID"])
	}
	var respHeaders map[string]string
	if len(respHeadersRaw) > 0 {
		if err := json.Unmarshal(respHeadersRaw, &respHeaders); err != nil {
			t.Fatalf("unmarshal response_headers: %v", err)
		}
	}
	if respHeaders["Content-Type"] != "application/json" {
		t.Errorf("response_headers Content-Type: got %q, want application/json", respHeaders["Content-Type"])
	}
}

