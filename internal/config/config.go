package config

import (
	"os"
	"strconv"
)

// Config holds environment-based configuration. No secrets are hardcoded.
type Config struct {
	// ServerPort is the HTTP listen port (e.g. 8080).
	ServerPort int
	// DatabaseURL is the PostgreSQL connection string (e.g. postgres://user:pass@host:5432/dbname).
	DatabaseURL string
	// CORSAllowedOrigin is the allowed origin for CORS; empty means CORS not configured.
	CORSAllowedOrigin string
}

// Load reads configuration from environment variables.
// Required: none for skeleton (port has default). Document required env vars for full app.
//   - PORT: server port (default 8080)
//   - DATABASE_URL: PostgreSQL connection string (required when DB is used)
//   - CORS_ALLOWED_ORIGIN: optional single origin for CORS
func Load() Config {
	port := 8080
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			port = v
		}
	}
	return Config{
		ServerPort:        port,
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		CORSAllowedOrigin: os.Getenv("CORS_ALLOWED_ORIGIN"),
	}
}
