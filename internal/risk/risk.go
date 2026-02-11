// Package risk implements real-time transaction risk scoring for session keys.
//
// Every session key transaction is evaluated against 4 weighted factors:
// velocity, recipient novelty, time-of-day deviation, and burn rate projection.
// Scores range from 0.0 (safe) to 1.0 (high risk). Transactions above the
// block threshold are rejected before funds move.
package risk

import (
	"context"
	"time"
)

// Decision represents the risk engine's verdict on a transaction.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionWarn  Decision = "warn"
	DecisionBlock Decision = "block"
)

// Default thresholds for risk decisions.
const (
	DefaultBlockThreshold = 0.8
	DefaultWarnThreshold  = 0.5
)

// RiskAssessment is the result of evaluating a single transaction.
type RiskAssessment struct {
	ID          string             `json:"id"`
	KeyID       string             `json:"keyId"`
	Score       float64            `json:"score"`
	Factors     map[string]float64 `json:"factors"`
	Decision    Decision           `json:"decision"`
	EvaluatedAt time.Time          `json:"evaluatedAt"`
}

// TransactionContext carries the data needed to score a transaction.
// Populated from SessionKey + SignedTransactRequest — no extra DB queries.
type TransactionContext struct {
	KeyID      string
	OwnerAddr  string
	To         string
	Amount     string  // USDC decimal string
	AmountUSDC float64 // pre-parsed for math
	MaxTotal   string
	TotalSpent string
	Nonce      uint64
	Timestamp  int64
}

// Store persists risk assessments for audit trail.
type Store interface {
	Record(ctx context.Context, assessment *RiskAssessment) error
	ListByKey(ctx context.Context, keyID string, limit int) ([]*RiskAssessment, error)
}
