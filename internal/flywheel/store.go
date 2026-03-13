package flywheel

import "context"

// SnapshotStore persists flywheel state snapshots for historical analysis.
// The flywheel dashboard uses these to show network health trends over time.
type SnapshotStore interface {
	// Save persists a flywheel state snapshot.
	Save(ctx context.Context, state *State) error
	// Recent returns the most recent snapshots (newest first), up to limit.
	Recent(ctx context.Context, limit int) ([]*State, error)
}
