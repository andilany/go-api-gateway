package config

import (
	"testing"
)

func TestLoad_defaultPort(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CORS_ALLOWED_ORIGIN", "")
	cfg := Load()
	if cfg.ServerPort != 8080 {
		t.Errorf("default ServerPort: got %d, want 8080", cfg.ServerPort)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("default DatabaseURL: got %q, want empty", cfg.DatabaseURL)
	}
	if cfg.CORSAllowedOrigin != "" {
		t.Errorf("default CORSAllowedOrigin: got %q, want empty", cfg.CORSAllowedOrigin)
	}
}

func TestLoad_customPort(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CORS_ALLOWED_ORIGIN", "")
	cfg := Load()
	if cfg.ServerPort != 9090 {
		t.Errorf("custom ServerPort: got %d, want 9090", cfg.ServerPort)
	}
}

func TestLoad_invalidPort_fallsBackToDefault(t *testing.T) {
	t.Setenv("PORT", "invalid")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CORS_ALLOWED_ORIGIN", "")
	cfg := Load()
	if cfg.ServerPort != 8080 {
		t.Errorf("invalid PORT should fall back to 8080: got %d", cfg.ServerPort)
	}
}

func TestLoad_zeroPort_fallsBackToDefault(t *testing.T) {
	t.Setenv("PORT", "0")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CORS_ALLOWED_ORIGIN", "")
	cfg := Load()
	if cfg.ServerPort != 8080 {
		t.Errorf("PORT=0 should fall back to 8080: got %d", cfg.ServerPort)
	}
}

func TestLoad_databaseURL(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "postgres://localhost/dummy")
	t.Setenv("CORS_ALLOWED_ORIGIN", "")
	cfg := Load()
	if cfg.DatabaseURL != "postgres://localhost/dummy" {
		t.Errorf("DatabaseURL: got %q, want postgres://localhost/dummy", cfg.DatabaseURL)
	}
}

func TestLoad_corsAllowedOrigin(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CORS_ALLOWED_ORIGIN", "https://app.example.com")
	cfg := Load()
	if cfg.CORSAllowedOrigin != "https://app.example.com" {
		t.Errorf("CORSAllowedOrigin: got %q, want https://app.example.com", cfg.CORSAllowedOrigin)
	}
}
