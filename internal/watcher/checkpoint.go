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
}

func NewMemoryCheckpoint() *MemoryCheckpoint {
	return &MemoryCheckpoint{}
}

func (c *MemoryCheckpoint) GetLastBlock(_ context.Context) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.block, nil
}

func (c *MemoryCheckpoint) SetLastBlock(_ context.Context, blockNum uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.block = blockNum
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

func (c *PostgresCheckpoint) GetLastBlock(ctx context.Context) (uint64, error) {
	var block uint64
	err := c.db.QueryRowContext(ctx,
		`SELECT block_number FROM watcher_checkpoints WHERE key = $1`, c.key).
		Scan(&block)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return block, err
}

func (c *PostgresCheckpoint) SetLastBlock(ctx context.Context, blockNum uint64) error {
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO watcher_checkpoints (key, block_number, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (key) DO UPDATE SET block_number = $2, updated_at = NOW()`,
		c.key, blockNum)
	return err
}

// Compile-time assertions
var _ CheckpointStore = (*MemoryCheckpoint)(nil)
var _ CheckpointStore = (*PostgresCheckpoint)(nil)
