// Package testutil provides shared test infrastructure for integration tests.
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// PGTest opens a test database connection, runs all migrations from the
// migrations/ directory, and returns the *sql.DB plus a cleanup function.
//
// Tests should call this at the top:
//
//	db, cleanup := testutil.PGTest(t)
//	defer cleanup()
//
// If POSTGRES_URL is not set, the test is skipped.
// The cleanup function truncates all application tables (not system tables).
func PGTest(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	dbURL := os.Getenv("POSTGRES_URL")
	if dbURL == "" {
		t.Skip("POSTGRES_URL not set, skipping integration test")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("pgtest: open database: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("pgtest: connect to database: %v", err)
	}

	ctx := context.Background()

	// Find and run all migrations in order.
	migrationsDir := findMigrationsDir(t)
	if err := runMigrations(ctx, db, migrationsDir); err != nil {
		_ = db.Close()
		t.Fatalf("pgtest: run migrations: %v", err)
	}

	cleanup := func() {
		// Truncate all application tables.
		truncateAll(ctx, db)
		_ = db.Close()
	}

	return db, cleanup
}

// findMigrationsDir walks up from the test working directory to find
// the project-level migrations/ directory.
func findMigrationsDir(t *testing.T) string {
	t.Helper()

	// Start from the current working directory and walk up.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("pgtest: getwd: %v", err)
	}

	for {
		candidate := filepath.Join(dir, "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("pgtest: could not find migrations/ directory walking up from cwd")
		}
		dir = parent
	}
}

// runMigrations reads all .sql files from the directory, sorts them by name,
// and executes them in order. The file paths are constructed from a trusted
// directory discovered by walking up from cwd — not from user input.
func runMigrations(ctx context.Context, db *sql.DB, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- path built from trusted migrations dir
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := db.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("execute %s: %w", name, err)
		}
	}

	return nil
}

// truncateAll truncates all user-created tables to provide a clean slate
// between tests. Uses TRUNCATE ... CASCADE to handle foreign keys.
func truncateAll(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `
		SELECT tablename FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename NOT LIKE 'pg_%'
		  AND tablename NOT LIKE 'sql_%'
	`)
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tables = append(tables, name)
		}
	}

	if len(tables) > 0 {
		// TRUNCATE all at once with CASCADE to handle FK dependencies.
		// Table names come from pg_tables system catalog, not user input.
		stmt := "TRUNCATE " + strings.Join(tables, ", ") + " CASCADE" // #nosec G202 -- table names from pg_tables, not user input
		_, _ = db.ExecContext(ctx, stmt)                              // #nosec G104 -- best-effort cleanup in test teardown
	}
}
