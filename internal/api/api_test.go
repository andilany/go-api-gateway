package api

import
(
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationsDir returns the path to migrations (project root/migrations).
func migrationsDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// When running "go test ./internal/api", cwd is project-root/internal/api.
	// Go up two levels to reach project root.
	cwd = filepath.Join(cwd, "..", "..")
	return filepath.Join(cwd, "migrations")
}

// setupTestStore applies migrations up and returns a Store backed by a pgx pool.
// Tests are skipped if DATABASE_URL is not set or the database is unreachable.
func setupTestStore(t *testing.T) (*store.Store, func()) {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping integration tests for config API")
	}

	ctx := context.Background()

	// Apply migrations up (including 000003) using golang-migrate.
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

	st := store.New(pool)

	// Clean tables used by the config API so each test starts from a known state.
	reset := func() {
		_, _ = pool.Exec(ctx, `TRUNCATE TABLE routes RESTART IDENTITY CASCADE`)
		_, _ = pool.Exec(ctx, `DELETE FROM global_header_config`)
		_, _ = pool.Exec(ctx, `INSERT INTO global_header_config (id, headers_to_forward, headers_to_set) VALUES (1, '{}', '{}') ON CONFLICT (id) DO NOTHING`)
	}
	reset()

	cleanup := func() {
		pool.Close()
	}

	return st, cleanup
}

func newTestRouter(t *testing.T, st *store.Store) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	h := &Handler{
		Store: st,
		Log:   slog.Default(),
	}
	h.Mount(r)
	return r
}

func decodeJSONResponse(t *testing.T, rr *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(rr.Body).Decode(v); err != nil {
		t.Fatalf("decode response JSON: %v", err)
	}
}

func TestRoutesCRUDAndValidation(t *testing.T) {
	st, cleanup := setupTestStore(t)
	defer cleanup()

	r := newTestRouter(t, st)

	// 1) Initial GET /api/routes should return 200 and empty array.
	req := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/routes status: got %d, want %d", rr.Code, http.StatusOK)
	}
	var gotRoutes []store.Route
	decodeJSONResponse(t, rr, &gotRoutes)
	if len(gotRoutes) != 0 {
		t.Fatalf("GET /api/routes length: got %d, want 0", len(gotRoutes))
	}

	// 2) POST /api/routes with valid body should create a route.
	createBody := RouteCreateUpdateBody{
		Name:             "route-one",
		Path:             "/api/foo",
		Upstream:         "https://example.com",
		HeadersToForward: []string{"X-Request-ID"},
		HeadersToSet:     map[string]string{"X-From-Gateway": "true"},
	}
	bodyBytes, _ := json.Marshal(createBody)
	req = httptest.NewRequest(http.MethodPost, "/api/routes", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /api/routes status: got %d, want %d", rr.Code, http.StatusCreated)
	}
	var created store.Route
	decodeJSONResponse(t, rr, &created)
	if created.ID == "" {
		t.Fatalf("created route ID is empty")
	}
	if created.Name != createBody.Name {
		t.Errorf("created route Name: got %q, want %q", created.Name, createBody.Name)
	}
	if created.Path != createBody.Path {
		t.Errorf("created route Path: got %q, want %q", created.Path, createBody.Path)
	}
	if created.Upstream != createBody.Upstream {
		t.Errorf("created route Upstream: got %q, want %q", created.Upstream, createBody.Upstream)
	}
	if loc := rr.Header().Get("Location"); loc != "/api/routes/"+created.ID {
		t.Errorf("Location header: got %q, want %q", loc, "/api/routes/"+created.ID)
	}

	// 3) GET /api/routes/{id} should return the created route.
	req = httptest.NewRequest(http.MethodGet, "/api/routes/"+created.ID, nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/routes/{id} status: got %d, want %d", rr.Code, http.StatusOK)
	}
	var fetched store.Route
	decodeJSONResponse(t, rr, &fetched)
	if fetched.ID != created.ID {
		t.Errorf("fetched route ID: got %q, want %q", fetched.ID, created.ID)
	}

	// 4) PUT /api/routes/{id} to update upstream and path.
	updateBody := RouteCreateUpdateBody{
		Name:             "route-one",
		Path:             "/api/foo-updated",
		Upstream:         "https://backend.example.com",
		HeadersToForward: []string{"X-Request-ID", "X-Trace-ID"},
		HeadersToSet:     map[string]string{"X-From-Gateway": "updated"},
	}
	bodyBytes, _ = json.Marshal(updateBody)
	req = httptest.NewRequest(http.MethodPut, "/api/routes/"+created.ID, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /api/routes/{id} status: got %d, want %d", rr.Code, http.StatusOK)
	}
	var updated store.Route
	decodeJSONResponse(t, rr, &updated)
	if updated.Path != updateBody.Path {
		t.Errorf("updated route Path: got %q, want %q", updated.Path, updateBody.Path)
	}
	if updated.Upstream != updateBody.Upstream {
		t.Errorf("updated route Upstream: got %q, want %q", updated.Upstream, updateBody.Upstream)
	}

	// 5) POST /api/routes with duplicate name should return 409.
	dupBody := RouteCreateUpdateBody{
		Name:     "route-one",
		Path:     "/api/other",
		Upstream: "https://example.com",
	}
	bodyBytes, _ = json.Marshal(dupBody)
	req = httptest.NewRequest(http.MethodPost, "/api/routes", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("POST /api/routes duplicate name status: got %d, want %d", rr.Code, http.StatusConflict)
	}

	// 6) POST /api/routes with invalid upstream should return 400.
	invalidUpstream := RouteCreateUpdateBody{
		Name:     "bad-upstream",
		Path:     "/api/bad",
		Upstream: "not-a-url",
	}
	bodyBytes, _ = json.Marshal(invalidUpstream)
	req = httptest.NewRequest(http.MethodPost, "/api/routes", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/routes invalid upstream status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}

	// 7) POST /api/routes with missing path/path_prefix should return 400.
	missingPath := RouteCreateUpdateBody{
		Name:     "missing-path",
		Upstream: "https://example.com",
	}
	bodyBytes, _ = json.Marshal(missingPath)
	req = httptest.NewRequest(http.MethodPost, "/api/routes", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/routes missing path status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}

	// 8) DELETE /api/routes/{id} should return 204 and subsequent GET should be 404.
	req = httptest.NewRequest(http.MethodDelete, "/api/routes/"+created.ID, nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/routes/{id} status: got %d, want %d", rr.Code, http.StatusNoContent)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/routes/"+created.ID, nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET /api/routes/{id} after delete status: got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestGlobalHeaderConfigEndpoints(t *testing.T) {
	st, cleanup := setupTestStore(t)
	defer cleanup()

	r := newTestRouter(t, st)

	// 1) Initial GET /api/config/headers should return defaults (empty arrays/map).
	req := httptest.NewRequest(http.MethodGet, "/api/config/headers", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/config/headers status: got %d, want %d", rr.Code, http.StatusOK)
	}
	var got GlobalHeaderConfigBody
	decodeJSONResponse(t, rr, &got)
	if len(got.HeadersToForward) != 0 {
		t.Errorf("default HeadersToForward length: got %d, want 0", len(got.HeadersToForward))
	}
	if got.HeadersToSet == nil || len(got.HeadersToSet) != 0 {
		if got.HeadersToSet == nil {
			t.Errorf("default HeadersToSet is nil; want empty map")
		} else if len(got.HeadersToSet) != 0 {
			t.Errorf("default HeadersToSet length: got %d, want 0", len(got.HeadersToSet))
		}
	}

	// 2) PUT /api/config/headers with valid values should be persisted and returned by GET.
	update := GlobalHeaderConfigBody{
		HeadersToForward: []string{"Authorization", "X-Request-ID"},
		HeadersToSet: map[string]string{
			"X-Gateway": "config",
		},
	}
	bodyBytes, _ := json.Marshal(update)
	req = httptest.NewRequest(http.MethodPut, "/api/config/headers", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /api/config/headers status: got %d, want %d", rr.Code, http.StatusOK)
	}
	var putResp GlobalHeaderConfigBody
	decodeJSONResponse(t, rr, &putResp)
	if !equalStringSlicesIgnoreOrder(putResp.HeadersToForward, update.HeadersToForward) {
		t.Errorf("PUT headers_to_forward: got %v, want %v", putResp.HeadersToForward, update.HeadersToForward)
	}
	if len(putResp.HeadersToSet) != len(update.HeadersToSet) || putResp.HeadersToSet["X-Gateway"] != "config" {
		t.Errorf("PUT headers_to_set: got %v, want %v", putResp.HeadersToSet, update.HeadersToSet)
	}

	// GET again to verify persistence.
	req = httptest.NewRequest(http.MethodGet, "/api/config/headers", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/config/headers after PUT status: got %d, want %d", rr.Code, http.StatusOK)
	}
	var gotAfter GlobalHeaderConfigBody
	decodeJSONResponse(t, rr, &gotAfter)
	if !equalStringSlicesIgnoreOrder(gotAfter.HeadersToForward, update.HeadersToForward) {
		t.Errorf("GET after PUT headers_to_forward: got %v, want %v", gotAfter.HeadersToForward, update.HeadersToForward)
	}
	if len(gotAfter.HeadersToSet) != len(update.HeadersToSet) || gotAfter.HeadersToSet["X-Gateway"] != "config" {
		t.Errorf("GET after PUT headers_to_set: got %v, want %v", gotAfter.HeadersToSet, update.HeadersToSet)
	}

	// 3) PUT /api/config/headers with invalid header name should return 400.
	bad := GlobalHeaderConfigBody{
		HeadersToForward: []string{"Good-Header", "Bad\r\nHeader"},
	}
	bodyBytes, _ = json.Marshal(bad)
	req = httptest.NewRequest(http.MethodPut, "/api/config/headers", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/config/headers invalid header status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func equalStringSlicesIgnoreOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ma := make(map[string]int, len(a))
	for _, v := range a {
		ma[v]++
	}
	for _, v := range b {
		if ma[v] == 0 {
			return false
		}
		ma[v]--
	}
	for _, c := range ma {
		if c != 0 {
			return false
		}
	}
	return true
}

