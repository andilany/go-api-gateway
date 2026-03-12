// Package main runs database migrations. Set DATABASE_URL and run from project root:
//
//	go run ./cmd/migrate [up|down]
//
// Default is "up". Migrations are in migrations/ (sequential order: 000001, 000002, 000003).
package main

import (
	"fmt"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/joho/godotenv"
)

func main() {
	// Load variables from .env if present so DATABASE_URL can be defined there.
	_ = godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}

	// Migrations path relative to current working directory; run from project root.
	migrationsPath := "migrations"
	if p := os.Getenv("MIGRATIONS_PATH"); p != "" {
		migrationsPath = p
	}

	m, err := migrate.New(
		"file://"+migrationsPath,
		dbURL,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	defer m.Close()

	action := "up"
	if len(os.Args) > 1 {
		action = os.Args[1]
	}

	switch action {
	case "up":
		if err := m.Up(); err != nil && err != migrate.ErrNoChange {
			fmt.Fprintf(os.Stderr, "migrate up: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "migrations up applied")
	case "down":
		if err := m.Down(); err != nil && err != migrate.ErrNoChange {
			fmt.Fprintf(os.Stderr, "migrate down: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "migrations down applied")
	default:
		fmt.Fprintf(os.Stderr, "usage: migrate [up|down]\n")
		os.Exit(1)
	}
}
