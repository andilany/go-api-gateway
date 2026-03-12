package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gateway/internal/logstore"
	"gateway/internal/store"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMatchRoute_LongestPrefixAndExact(t *testing.T) {
	routes := []store.Route{
		{Name: "root", PathPrefix: "/"},
		{Name: "foo", PathPrefix: "/foo/"},
		{Name: "foo-bar", PathPrefix: "/foo/bar/"},
		{Name: "foo-exact", Path: "/foo/bar"},
	}

	tests := []struct {
		name     string
		path     string
		wantName string
	}{
		{
			name:     "longest prefix wins",
			path:     "/foo/bar/baz",
			wantName: "foo-bar",
		},
		{
			name:     "exact beats prefix",
			path:     "/foo/bar",
			wantName: "foo-exact",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchRoute(routes, tt.path)
			if !ok {
				t.Fatalf("matchRoute(%q) = no match, want %q", tt.path, tt.wantName)
			}
			if got.Name != tt.wantName {
				t.Fatalf("matchRoute(%q) = %q, want %q", tt.path, got.Name, tt.wantName)
			}
		})
	}
}

func TestMatchRoute_TieBreakOnName(t *testing.T) {
	routes := []store.Route{
		{Name: "b-route", PathPrefix: "/foo/"},
		{Name: "a-route", PathPrefix: "/foo/"},
	}

	got, ok := matchRoute(routes, "/foo/bar")
	if !ok {
		t.Fatalf("matchRoute returned no match")
	}
	if got.Name != "a-route" {
		t.Fatalf("matchRoute tie-breaker: got %q, want %q", got.Name, "a-route")
	}
}

func TestBuildUpstreamURL(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		path     string
		rawQuery string
		want     string
	}{
		{
			name: "no query",
			base: "https://example.com",
			path: "/foo",
			want: "https://example.com/foo",
		},
		{
			name:     "with query",
			base:     "https://example.com",
			path:     "/foo",
			rawQuery: "a=1&b=2",
			want:     "https://example.com/foo?a=1&b=2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildUpstreamURL(tt.base, tt.path, tt.rawQuery)
			if got != tt.want {
				t.Fatalf("buildUpstreamURL(%q, %q, %q) = %q, want %q", tt.base, tt.path, tt.rawQuery, got, tt.want)
			}
		})
	}
}

func TestApplyHeaders_MergeAndOverride(t *testing.T) {
	global := store.GlobalHeaderConfig{
		HeadersToForward: []string{"X-Request-ID"},
		HeadersToSet: map[string]string{
			"X-Global":   "global",
			"X-Override": "from-global",
		},
	}
	routeCfg := store.Route{
		HeadersToForward: []string{"X-Trace-ID", "Authorization"},
		HeadersToSet: map[string]string{
			"X-Route":    "route",
			"X-Override": "from-route",
		},
	}

	orig := httptest.NewRequest(http.MethodGet, "/foo", nil)
	orig.Header.Set("X-Request-ID", "req-123")
	orig.Header.Set("X-Trace-ID", "trace-abc")
	orig.Header.Set("Authorization", "Bearer token")
	orig.Header.Set("X-Other", "should-not-forward")

	upReq := httptest.NewRequest(http.MethodGet, "https://upstream/foo", nil)

	applyHeaders(upReq, orig, global, routeCfg)

	// Forwarded headers: union of global and route HeadersToForward.
	if got := upReq.Header.Get("X-Request-ID"); got != "req-123" {
		t.Errorf("X-Request-ID: got %q, want %q", got, "req-123")
	}
	if got := upReq.Header.Get("X-Trace-ID"); got != "trace-abc" {
		t.Errorf("X-Trace-ID: got %q, want %q", got, "trace-abc")
	}
	if got := upReq.Header.Get("Authorization"); got != "Bearer token" {
		t.Errorf("Authorization: got %q, want %q", got, "Bearer token")
	}
	if got := upReq.Header.Get("X-Other"); got != "" {
		t.Errorf("X-Other should not be forwarded, got %q", got)
	}

	// Set headers: route overrides global on key collision.
	if got := upReq.Header.Get("X-Global"); got != "global" {
		t.Errorf("X-Global: got %q, want %q", got, "global")
	}
	if got := upReq.Header.Get("X-Route"); got != "route" {
		t.Errorf("X-Route: got %q, want %q", got, "route")
	}
	if got := upReq.Header.Get("X-Override"); got != "from-route" {
		t.Errorf("X-Override: got %q, want %q", got, "from-route")
	}
}

// --- Integration-style tests using Postgres and real logstore.Store ---

// migrationsDir mirrors internal/api helper, adjusted for this package path.
func migrationsDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// When running "go test ./internal/proxy", cwd is project-root/internal/proxy.
	// Go up two levels to reach project root.
	cwd = filepath.Join(cwd, "..", "..")
	return filepath.Join(cwd, "migrations")
}

// setupTestStoreAndLogs applies migrations and returns a Store, log store, and cleanup.
// Tests are skipped if DATABASE_URL is not set or the database is unreachable.
func setupTestStoreAndLogs(t *testing.T) (*store.Store, *logstore.Store, *pgxpool.Pool, func()) {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping proxy integration tests")
	}

	ctx := context.Background()

	dir := migrationsDir(t)
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
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("database ping: %v", err)
	}

	// Clean tables used by proxy tests so each test starts from a known state.
	_, _ = pool.Exec(ctx, `TRUNCATE TABLE request_logs RESTART IDENTITY CASCADE`)
	_, _ = pool.Exec(ctx, `TRUNCATE TABLE routes RESTART IDENTITY CASCADE`)
	_, _ = pool.Exec(ctx, `DELETE FROM global_header_config`)
	_, _ = pool.Exec(ctx, `INSERT INTO global_header_config (id, headers_to_forward, headers_to_set) VALUES (1, '{}', '{}') ON CONFLICT (id) DO NOTHING`)

	st := store.New(pool)
	logs := logstore.New(pool, slog.Default())

	cleanup := func() {
		pool.Close()
	}
	return st, logs, pool, cleanup
}

func TestProxy_ForwardsAndLogsSuccess(t *testing.T) {
	st, logs, pool, cleanup := setupTestStoreAndLogs(t)
	defer cleanup()

	// Fake upstream server to capture request details.
	var (
		gotMethod string
		gotPath   string
		gotQuery  string
		gotHeader http.Header
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotHeader = r.Header.Clone()
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	// Configure global and route-specific headers.
	_, err := st.SetGlobalHeaderConfig(context.Background(),
		[]string{"Authorization", "X-Request-ID"},
		map[string]string{"X-Global": "global"},
	)
	if err != nil {
		t.Fatalf("SetGlobalHeaderConfig: %v", err)
	}

	route, err := st.CreateRoute(context.Background(),
		"test-route",
		"/api/",
		upstream.URL,
		[]string{"X-Trace-ID"},
		map[string]string{"X-Route": "route"},
	)
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	h := &Handler{
		Store:  st,
		Logs:   logs,
		Log:    slog.Default(),
		Client: upstream.Client(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/hello?x=1", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Cookie", "a=b")
	req.Header.Set("X-Request-ID", "req-123")
	req.Header.Set("X-Trace-ID", "trace-abc")
	req.Header.Set("X-Other", "ignore-me")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("proxy response status: got %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("proxy response body: got %q, want %q", rr.Body.String(), "ok")
	}
	if got := rr.Header().Get("X-Upstream"); got != "yes" {
		t.Fatalf("proxy response header X-Upstream: got %q, want %q", got, "yes")
	}

	// Verify upstream received expected method, path, query, and headers.
	if gotMethod != http.MethodGet {
		t.Errorf("upstream method: got %q, want %q", gotMethod, http.MethodGet)
	}
	if gotPath != "/api/hello" {
		t.Errorf("upstream path: got %q, want %q", gotPath, "/api/hello")
	}
	if gotQuery != "x=1" {
		t.Errorf("upstream query: got %q, want %q", gotQuery, "x=1")
	}
	if got := gotHeader.Get("X-Request-ID"); got != "req-123" {
		t.Errorf("upstream X-Request-ID: got %q, want %q", got, "req-123")
	}
	if got := gotHeader.Get("X-Trace-ID"); got != "trace-abc" {
		t.Errorf("upstream X-Trace-ID: got %q, want %q", got, "trace-abc")
	}
	if got := gotHeader.Get("Authorization"); got != "Bearer secret" {
		t.Errorf("upstream Authorization: got %q, want %q", got, "Bearer secret")
	}
	if got := gotHeader.Get("X-Global"); got != "global" {
		t.Errorf("upstream X-Global: got %q, want %q", got, "global")
	}
	if got := gotHeader.Get("X-Route"); got != "route" {
		t.Errorf("upstream X-Route: got %q, want %q", got, "route")
	}
	if got := gotHeader.Get("X-Other"); got != "" {
		t.Errorf("upstream X-Other should be empty, got %q", got)
	}

	// Verify request was logged with expected fields and sanitized headers.
	var (
		logRouteName   string
		logMethod      string
		logPath        string
		logStatusCode  int
		logDurationMs  int64
		reqHeadersRaw  []byte
		respHeadersRaw []byte
	)
	err = pool.QueryRow(context.Background(),
		`SELECT route_name, method, path, status_code, duration_ms, request_headers, response_headers
		 FROM request_logs
		 WHERE route_name = $1
		 ORDER BY created_at DESC
		 LIMIT 1`, route.Name).
		Scan(&logRouteName, &logMethod, &logPath, &logStatusCode, &logDurationMs, &reqHeadersRaw, &respHeadersRaw)
	if err != nil {
		t.Fatalf("select from request_logs: %v", err)
	}

	if logRouteName != route.Name {
		t.Errorf("logged route_name: got %q, want %q", logRouteName, route.Name)
	}
	if logMethod != http.MethodGet {
		t.Errorf("logged method: got %q, want %q", logMethod, http.MethodGet)
	}
	if logPath != "/api/hello" {
		t.Errorf("logged path: got %q, want %q", logPath, "/api/hello")
	}
	if logStatusCode != http.StatusOK {
		t.Errorf("logged status_code: got %d, want %d", logStatusCode, http.StatusOK)
	}
	if logDurationMs < 0 {
		t.Errorf("logged duration_ms: got %d, must be >= 0", logDurationMs)
	}

	var reqHeaders map[string]string
	if len(reqHeadersRaw) > 0 {
		if err := json.Unmarshal(reqHeadersRaw, &reqHeaders); err != nil {
			t.Fatalf("unmarshal request_headers: %v", err)
		}
	}
	if reqHeaders["Authorization"] != "[REDACTED]" {
		t.Errorf("logged Authorization: got %q, want [REDACTED]", reqHeaders["Authorization"])
	}
	if reqHeaders["Cookie"] != "[REDACTED]" {
		t.Errorf("logged Cookie: got %q, want [REDACTED]", reqHeaders["Cookie"])
	}
	if reqHeaders["X-Request-ID"] != "req-123" {
		t.Errorf("logged X-Request-ID: got %q, want %q", reqHeaders["X-Request-ID"], "req-123")
	}

	var respHeaders map[string]string
	if len(respHeadersRaw) > 0 {
		if err := json.Unmarshal(respHeadersRaw, &respHeaders); err != nil {
			t.Fatalf("unmarshal response_headers: %v", err)
		}
	}
	if respHeaders["X-Upstream"] != "yes" {
		t.Errorf("logged response X-Upstream: got %q, want %q", respHeaders["X-Upstream"], "yes")
	}
}

type timeoutRoundTripper struct{}

func (timeoutRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

func TestProxy_UpstreamTimeoutLogsAndReturns504(t *testing.T) {
	st, logs, pool, cleanup := setupTestStoreAndLogs(t)
	defer cleanup()

	// Ensure clean logs for this test.
	_, _ = pool.Exec(context.Background(), `TRUNCATE TABLE request_logs RESTART IDENTITY CASCADE`)

	_, err := st.CreateRoute(context.Background(),
		"timeout-route",
		"/slow/",
		"http://upstream.invalid",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	h := &Handler{
		Store: st,
		Logs:  logs,
		Log:   slog.Default(),
		Client: &http.Client{
			Timeout:   50 * time.Millisecond,
			Transport: timeoutRoundTripper{},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/slow/test", bytes.NewReader(nil))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("proxy timeout status: got %d, want %d", rr.Code, http.StatusGatewayTimeout)
	}

	// Verify a log entry was written with 504 status.
	var status int
	err = pool.QueryRow(context.Background(),
		`SELECT status_code FROM request_logs WHERE route_name = $1 ORDER BY created_at DESC LIMIT 1`,
		"timeout-route").
		Scan(&status)
	if err != nil {
		t.Fatalf("select status_code from request_logs: %v", err)
	}
	if status != http.StatusGatewayTimeout {
		t.Errorf("logged status_code: got %d, want %d", status, http.StatusGatewayTimeout)
	}
}

