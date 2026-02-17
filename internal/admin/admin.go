// Package admin provides admin-only endpoints for resolving stuck financial states.
package admin

import "time"

// StuckSession represents a gateway session in settlement_failed status.
type StuckSession struct {
	ID         string    `json:"id"`
	AgentAddr  string    `json:"agentAddr"`
	TenantID   string    `json:"tenantId,omitempty"`
	MaxTotal   string    `json:"maxTotal"`
	TotalSpent string    `json:"totalSpent"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expiresAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// ReconciliationReport summarizes the results of a cross-subsystem reconciliation run.
type ReconciliationReport struct {
	LedgerMismatches int           `json:"ledgerMismatches"`
	StuckEscrows     int           `json:"stuckEscrows"`
	StaleStreams     int           `json:"staleStreams"`
	OrphanedHolds    int           `json:"orphanedHolds"`
	Healthy          bool          `json:"healthy"`
	Duration         time.Duration `json:"durationMs"`
	Timestamp        time.Time     `json:"timestamp"`
}

// DenialExportRecord is a serializable denial record for ML training data export.
type DenialExportRecord struct {
	ID              int64     `json:"id"`
	AgentAddr       string    `json:"agentAddr"`
	RuleName        string    `json:"ruleName"`
	Reason          string    `json:"reason"`
	Amount          string    `json:"amount"`
	OpType          string    `json:"opType"`
	Tier            string    `json:"tier"`
	Counterparty    string    `json:"counterparty"`
	HourlyTotal     string    `json:"hourlyTotal"`
	BaselineMean    string    `json:"baselineMean"`
	BaselineStddev  string    `json:"baselineStddev"`
	OverrideAllowed bool      `json:"overrideAllowed"`
	CreatedAt       time.Time `json:"createdAt"`
}
