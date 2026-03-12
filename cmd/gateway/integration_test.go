// Package main_test: Phase 5 integration tests for the gateway (health, CORS, rate limits, config API).
// Requires DATABASE_URL. Run from project root: go test ./cmd/gateway -run Integration -v
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gateway/internal/api"
	"gateway/internal/config"
	"gateway/internal/logstore"
	"gateway/internal/middleware"
	"gateway/internal/proxy"
	"gateway/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
)

func migrationsDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// From cmd/gateway go up to project root.
	cwd = filepath.Join(cwd, "..", "..")
	return filepath.Join(cwd, "migrations")
}

// buildTestRouter builds the same handler chain as main.go (CORS, RateLimitByPath, health, api, proxy).
func buildTestRouter(t *testing.T, cfg config.Config, pool *pgxpool.Pool) *chi.Mux {
	t.Helper()
	st := store.New(pool)
	apiHandler := &api.Handler{Store: st, Log: slog.Default()}
	logStore := logstore.New(pool, slog.Default())
	proxyHandler := &proxy.Handler{
		Store:  st,
		Logs:   logStore,
		Log:    slog.Default(),
		Client: &http.Client{Timeout: 30 * time.Second},
	}

	r := chi.NewRouter()
	r.Use(middleware.CORS(cfg.CORSAllowedOrigin))
	r.Use(middleware.RateLimitByPath("/api",
		middleware.NewRateLimiter(20, time.Hour, middleware.GetIP),
		middleware.NewRateLimiter(100, time.Hour, middleware.GetIP),
	))
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("gateway ok\n"))
	})
	apiHandler.Mount(r)
	proxyHandler.Mount(r)
	return r
}

// TestIntegration_Phase5 runs Phase 5 checks: health, CORS (allowed origin only), config API, rate limit 429.
// Skipped when DATABASE_URL is not set.
func TestIntegration_Phase5(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping Phase 5 integration test")
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
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("database ping: %v", err)
	}

	// Use a fixed CORS origin for tests.
	cfg := config.Config{
		ServerPort:        8080,
		DatabaseURL:       dbURL,
		CORSAllowedOrigin: "https://app.example.com",
	}
	router := buildTestRouter(t, cfg, pool)

	// --- Health ---
	t.Run("health", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET / status: got %d, want 200", rr.Code)
		}
		if body := rr.Body.String(); body != "gateway ok\n" {
			t.Errorf("GET / body: got %q, want %q", body, "gateway ok\n")
		}
	})

	// --- CORS: matching origin gets Allow-Origin; wrong origin does not ---
	t.Run("CORS_matching_origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		allowOrigin := rr.Header().Get("Access-Control-Allow-Origin")
		if allowOrigin != "https://app.example.com" {
			t.Errorf("Allow-Origin: got %q, want https://app.example.com", allowOrigin)
		}
	})
	t.Run("CORS_wrong_origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://evil.com")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		allowOrigin := rr.Header().Get("Access-Control-Allow-Origin")
		if allowOrigin != "" {
			t.Errorf("Allow-Origin for wrong origin: got %q, want empty", allowOrigin)
		}
	})

	// --- Config API: GET /api/routes, POST /api/routes ---
	t.Run("config_api_get_routes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /api/routes status: got %d, want 200", rr.Code)
		}
		var routes []store.Route
		if err := json.NewDecoder(rr.Body).Decode(&routes); err != nil {
			t.Fatalf("decode GET /api/routes: %v", err)
		}
		// Response is a JSON array (possibly empty); no further constraint for this test.
		_ = routes
	})
	t.Run("config_api_post_route", func(t *testing.T) {
		body := api.RouteCreateUpdateBody{
			Name:     "phase5-route",
			Path:     "/p5",
			Upstream: "https://example.com",
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/routes", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Errorf("POST /api/routes status: got %d, want 201", rr.Code)
		}
		// Clean up so other tests or reruns don't conflict
		var created store.Route
		_ = json.NewDecoder(rr.Body).Decode(&created)
		if created.ID != "" {
			_, _ = pool.Exec(ctx, `DELETE FROM routes WHERE id = $1`, created.ID)
		}
	})

	// --- Rate limit: 21st request to /api from same IP returns 429 ---
	t.Run("rate_limit_config_api_429", func(t *testing.T) {
		// Use a unique "IP" per run via X-Forwarded-For so we don't collide with other tests.
		uniqueIP := "203.0.113.99"
		var lastStatus int
		for i := 0; i < 21; i++ {
			req := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
			req.Header.Set("X-Forwarded-For", uniqueIP)
			req.RemoteAddr = "127.0.0.1"
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)
			lastStatus = rr.Code
		}
		if lastStatus != http.StatusTooManyRequests {
			t.Errorf("21st request to /api/routes: got status %d, want 429", lastStatus)
		}
	})
}
