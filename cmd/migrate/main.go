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

	// Pin the pool to a single connection so the advisory lock acquired below
	// is held on the same session that goose subsequently uses. Without this,
	// goose may check out a different conn and the lock would not protect it.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	command := os.Args[1]
	args := os.Args[2:]

	if command == "up" || command == "up-to" || command == "redo" || command == "down" || command == "down-to" {
		if _, err := db.ExecContext(context.Background(), "SELECT pg_advisory_lock(5432001)"); err != nil {
			log.Fatalf("Failed to acquire migration lock: %v", err)
		}
		log.Println("Acquired migration advisory lock")
	}

	if err := goose.RunContext(context.Background(), command, db, migrationsDir, args...); err != nil {
		log.Fatalf("Migration %q failed: %v", command, err) //nolint:gosec // command is from os.Args, operator input
	}
}
