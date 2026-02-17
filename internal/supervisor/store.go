package supervisor

import (
	"context"
	"math/big"
	"time"
)

// AgentBaseline holds the learned hourly spending profile for a single agent.
type AgentBaseline struct {
	AgentAddr    string
	HourlyMean   *big.Int // 6-decimal USDC units
	HourlyStddev *big.Int // 6-decimal USDC units
	SampleHours  int
	LastUpdated  time.Time
}

// SpendEventRecord is a single spend observation persisted to the store.
type SpendEventRecord struct {
	ID           int64
	AgentAddr    string
	Counterparty string
	Amount       *big.Int // 6-decimal USDC units
	CreatedAt    time.Time
}

// DenialRecord is a feature vector written on every Deny verdict.
type DenialRecord struct {
	ID              int64
	AgentAddr       string
	RuleName        string
	Reason          string
	Amount          *big.Int
	OpType          string
	Tier            string
	Counterparty    string
	HourlyTotal     *big.Int
	BaselineMean    *big.Int
	BaselineStddev  *big.Int
	OverrideAllowed bool // set true when an operator overrides this denial (feedback loop)
	CreatedAt       time.Time
}

// BaselineStore persists spend events, learned baselines, and denial logs.
type BaselineStore interface {
	// Baselines
	SaveBaselineBatch(ctx context.Context, baselines []*AgentBaseline) error
	GetAllBaselines(ctx context.Context) ([]*AgentBaseline, error)

	// Spend events
	AppendSpendEvent(ctx context.Context, ev *SpendEventRecord) error
	AppendSpendEventBatch(ctx context.Context, events []*SpendEventRecord) error
	GetRecentSpendEvents(ctx context.Context, since time.Time) ([]*SpendEventRecord, error)
	GetAllAgentsWithEvents(ctx context.Context, since time.Time) ([]string, error)
	GetHourlyTotals(ctx context.Context, agentAddr string, since time.Time) (map[time.Time]*big.Int, error)

	// Denials
	LogDenial(ctx context.Context, rec *DenialRecord) error
	ListDenials(ctx context.Context, since time.Time, limit int) ([]*DenialRecord, error)

	// Maintenance
	PruneOldEvents(ctx context.Context, before time.Time) (int64, error)
}
