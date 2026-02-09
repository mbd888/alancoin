// Command migrate runs database migrations via goose.
//
// Usage:
//
//	go run ./cmd/migrate up          # Apply all pending migrations
//	go run ./cmd/migrate down        # Roll back the last migration
//	go run ./cmd/migrate status      # Show migration status
//	go run ./cmd/migrate version     # Show current schema version
//	go run ./cmd/migrate redo        # Roll back and re-apply last migration
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

const migrationsDir = "migrations"

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: migrate <command>")
		fmt.Println("Commands: up, down, status, version, redo, up-to <version>, down-to <version>")
		os.Exit(1)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	command := os.Args[1]
	args := os.Args[2:]

	if err := goose.RunContext(context.Background(), command, db, migrationsDir, args...); err != nil {
		log.Fatalf("Migration %s failed: %v", command, err)
	}
}
