package reputation

import (
	"context"
)

// SnapshotStore persists reputation snapshots.
type SnapshotStore interface {
	// Save persists a single snapshot.
	Save(ctx context.Context, snap *Snapshot) error

	// SaveBatch persists multiple snapshots in one call.
	SaveBatch(ctx context.Context, snaps []*Snapshot) error

	// Query returns historical snapshots matching the query.
	Query(ctx context.Context, q HistoryQuery) ([]*Snapshot, error)

	// Latest returns the most recent snapshot for an address.
	Latest(ctx context.Context, address string) (*Snapshot, error)
}
