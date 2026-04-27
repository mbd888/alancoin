package watcher

import (
	"context"
	"database/sql"
	"sync"
)

// MemoryCheckpoint stores the last processed block in memory (for testing/dev).
type MemoryCheckpoint struct {
	mu    sync.Mutex
	block uint64
	hash  string
}

func NewMemoryCheckpoint() *MemoryCheckpoint {
	return &MemoryCheckpoint{}
}

func (c *MemoryCheckpoint) GetLastBlock(_ context.Context) (uint64, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.block, c.hash, nil
}

func (c *MemoryCheckpoint) SetLastBlock(_ context.Context, block uint64, hash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.block = block
	c.hash = hash
	return nil
}

// PostgresCheckpoint persists the last processed block in PostgreSQL.
type PostgresCheckpoint struct {
	db  *sql.DB
	key string // checkpoint key (allows multiple watchers)
}

func NewPostgresCheckpoint(db *sql.DB, key string) *PostgresCheckpoint {
	if key == "" {
		key = "deposit_watcher"
	}
	return &PostgresCheckpoint{db: db, key: key}
}

func (c *PostgresCheckpoint) GetLastBlock(ctx context.Context) (uint64, string, error) {
	var block uint64
	var hash string
	err := c.db.QueryRowContext(ctx,
		`SELECT block_number, COALESCE(block_hash, '') FROM watcher_checkpoints WHERE key = $1`, c.key).
		Scan(&block, &hash)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	return block, hash, err
}

func (c *PostgresCheckpoint) SetLastBlock(ctx context.Context, block uint64, hash string) error {
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO watcher_checkpoints (key, block_number, block_hash, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (key) DO UPDATE SET block_number = $2, block_hash = $3, updated_at = NOW()`,
		c.key, block, hash)
	return err
}

// Compile-time assertions
var _ CheckpointStore = (*MemoryCheckpoint)(nil)
var _ CheckpointStore = (*PostgresCheckpoint)(nil)
