// Package main_test provides Phase 2 tests for migrations: SQL validation and optional
// integration test (up/down) when DATABASE_URL is set. Run from project root:
//
//	go test ./cmd/migrate
//
// Integration test is skipped if DATABASE_URL is not set.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// migrationsDir returns the path to migrations (project root/migrations). When tests run
// via "go test ./cmd/migrate" from project root, cwd is project root.
func migrationsDir(t *testing.T) string {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// If we're in cmd/migrate, go up to project root.
	if strings.HasSuffix(cwd, string(filepath.Separator)+"migrate") || strings.HasSuffix(cwd, "cmd\\migrate") {
		cwd = filepath.Join(cwd, "..", "..")
	}
	return filepath.Join(cwd, "migrations")
}

func TestMigrationFilesExist(t *testing.T) {
	dir := migrationsDir(t)
	for _, name := range []string{
		"000001_create_routes.up.sql",
		"000001_create_routes.down.sql",
		"000002_create_request_logs.up.sql",
		"000002_create_request_logs.down.sql",
		"000003_create_global_header_config.up.sql",
		"000003_create_global_header_config.down.sql",
	} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing migration file %s: %v", name, err)
		}
	}
}

func TestRoutesUpSQL(t *testing.T) {
	dir := migrationsDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "000001_create_routes.up.sql"))
	if err != nil {
		t.Fatalf("read routes up: %v", err)
	}
	s := string(body)
	required := []string{
		"CREATE TABLE",
		"routes",
		"id",
		"name",
		"path_prefix",
		"upstream",
		"headers_to_forward",
		"headers_to_set",
		"created_at",
		"updated_at",
		"UNIQUE (name)",
		"PRIMARY KEY",
	}
	for _, sub := range required {
		if !strings.Contains(s, sub) {
			t.Errorf("000001_create_routes.up.sql missing required content: %q", sub)
		}
	}
}

func TestRoutesDownSQL(t *testing.T) {
	dir := migrationsDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "000001_create_routes.down.sql"))
	if err != nil {
		t.Fatalf("read routes down: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "DROP TABLE") || !strings.Contains(s, "routes") {
		t.Errorf("000001_create_routes.down.sql should DROP TABLE routes; got: %s", s)
	}
}

func TestRequestLogsUpSQL(t *testing.T) {
	dir := migrationsDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "000002_create_request_logs.up.sql"))
	if err != nil {
		t.Fatalf("read request_logs up: %v", err)
	}
	s := string(body)
	required := []string{
		"CREATE TABLE",
		"request_logs",
		"id",
		"route_name",
		"method",
		"path",
		"status_code",
		"duration_ms",
		"request_headers",
		"response_headers",
		"created_at",
		"PRIMARY KEY",
		"request_logs_created_at_idx",
		"request_logs_route_name_idx",
	}
	for _, sub := range required {
		if !strings.Contains(s, sub) {
			t.Errorf("000002_create_request_logs.up.sql missing required content: %q", sub)
		}
	}
	if !strings.Contains(s, "status_code >= 100") && !strings.Contains(s, "status_code_check") {
		t.Error("000002_create_request_logs.up.sql should have status_code CHECK constraint")
	}
	if !strings.Contains(s, "duration_ms") {
		t.Error("000002_create_request_logs.up.sql should reference duration_ms (and ideally CHECK)")
	}
}

func TestRequestLogsDownSQL(t *testing.T) {
	dir := migrationsDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "000002_create_request_logs.down.sql"))
	if err != nil {
		t.Fatalf("read request_logs down: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "request_logs_route_name_idx") || !strings.Contains(s, "request_logs_created_at_idx") {
		t.Errorf("000002 down should drop indexes first; got: %s", s)
	}
	if !strings.Contains(s, "DROP TABLE") || !strings.Contains(s, "request_logs") {
		t.Errorf("000002 down should DROP TABLE request_logs; got: %s", s)
	}
}

func TestGlobalHeaderConfigUpSQL(t *testing.T) {
	dir := migrationsDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "000003_create_global_header_config.up.sql"))
	if err != nil {
		t.Fatalf("read global_header_config up: %v", err)
	}
	s := string(body)
	required := []string{
		"CREATE TABLE",
		"global_header_config",
		"id",
		"headers_to_forward",
		"headers_to_set",
		"updated_at",
		"PRIMARY KEY",
		"global_header_config_single_row",
		"CHECK (id = 1)",
		"INSERT INTO global_header_config",
		"ON CONFLICT (id) DO NOTHING",
	}
	for _, sub := range required {
		if !strings.Contains(s, sub) {
			t.Errorf("000003_create_global_header_config.up.sql missing required content: %q", sub)
		}
	}
}

func TestGlobalHeaderConfigDownSQL(t *testing.T) {
	dir := migrationsDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "000003_create_global_header_config.down.sql"))
	if err != nil {
		t.Fatalf("read global_header_config down: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "DROP TABLE") || !strings.Contains(s, "global_header_config") {
		t.Errorf("000003_create_global_header_config.down.sql should DROP TABLE global_header_config; got: %s", s)
	}
}

// TestMigrateUpDown runs migrations up then down when DATABASE_URL is set.
// Skipped if DATABASE_URL is empty (e.g. CI without Postgres).
func TestMigrateUpDown(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	dir := migrationsDir(t)
	// golang-migrate file driver expects file:///absolute/path (or file://relative from cwd)
	path := "file://" + filepath.ToSlash(dir)
	m, err := migrate.New(path, dbURL)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate down: %v", err)
	}
	// Bring back up so DB is left in applied state
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up (restore): %v", err)
	}
}
