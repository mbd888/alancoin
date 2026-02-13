package gateway

import (
	"context"
	"database/sql"
	"log/slog"
)

// orphanedHold represents a ledger hold with no matching gateway session.
type orphanedHold struct {
	AgentAddr string
	Amount    string
	Reference string // gateway session ID (gw_...)
}

// ReconcileOrphanedHolds finds gateway holds in the ledger that have no matching
// session record and releases them. This handles the crash window between
// ledger.Hold() and store.CreateSession() in CreateSession.
//
// Called once at server startup. Only operates on PostgreSQL — in-memory mode
// has no crash recovery story.
func ReconcileOrphanedHolds(ctx context.Context, db *sql.DB, ledger LedgerService, logger *slog.Logger) {
	orphans, err := findOrphanedHolds(ctx, db)
	if err != nil {
		logger.Error("gateway reconciliation: failed to query orphaned holds", "error", err)
		return
	}
	if len(orphans) == 0 {
		return
	}

	logger.Warn("gateway reconciliation: found orphaned holds", "count", len(orphans))

	for _, o := range orphans {
		if err := ledger.ReleaseHold(ctx, o.AgentAddr, o.Amount, o.Reference); err != nil {
			logger.Error("gateway reconciliation: failed to release orphaned hold",
				"session", o.Reference, "agent", o.AgentAddr, "amount", o.Amount, "error", err)
			continue
		}
		logger.Info("gateway reconciliation: released orphaned hold",
			"session", o.Reference, "agent", o.AgentAddr, "amount", o.Amount)
	}
}

// findOrphanedHolds queries for ledger hold entries with gw_ references
// that have no corresponding gateway session AND no corresponding settle/release.
//
// A hold is orphaned if:
//  1. It has type='hold' and reference LIKE 'gw_%' (gateway session hold)
//  2. No gateway_sessions row exists with that ID
//  3. No settle_hold_out or release_hold entry exists for the same (agent, reference)
//
// Condition 3 prevents releasing holds that were already settled/released
// but whose session record was lost.
func findOrphanedHolds(ctx context.Context, db *sql.DB) ([]orphanedHold, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT le.agent_address, le.amount, le.reference
		FROM ledger_entries le
		LEFT JOIN gateway_sessions gs ON gs.id = le.reference
		WHERE le.type = 'hold'
		  AND le.reference LIKE 'gw_%'
		  AND gs.id IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM ledger_entries le2
		      WHERE le2.agent_address = le.agent_address
		        AND le2.reference = le.reference
		        AND le2.type IN ('settle_hold_out', 'release_hold')
		  )
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []orphanedHold
	for rows.Next() {
		var o orphanedHold
		if err := rows.Scan(&o.AgentAddr, &o.Amount, &o.Reference); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}
