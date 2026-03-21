package intelligence

import (
	"context"
	"time"
)

// Store persists intelligence profiles, score history, and network benchmarks.
type Store interface {
	// SaveProfile upserts the current intelligence profile for an agent.
	SaveProfile(ctx context.Context, profile *AgentProfile) error

	// SaveProfileBatch upserts profiles for multiple agents in one transaction.
	SaveProfileBatch(ctx context.Context, profiles []*AgentProfile) error

	// GetProfile returns the latest profile for an agent.
	GetProfile(ctx context.Context, address string) (*AgentProfile, error)

	// GetProfiles returns profiles for multiple agents (batch lookup).
	GetProfiles(ctx context.Context, addresses []string) (map[string]*AgentProfile, error)

	// GetTopProfiles returns agents ranked by composite score.
	GetTopProfiles(ctx context.Context, limit int) ([]*AgentProfile, error)

	// SaveScoreHistory appends snapshots for trend computation.
	SaveScoreHistory(ctx context.Context, points []*ScoreHistoryPoint) error

	// GetScoreHistory returns historical scores for an agent within a time range.
	GetScoreHistory(ctx context.Context, address string, from, to time.Time, limit int) ([]*ScoreHistoryPoint, error)

	// DeleteScoreHistoryBefore removes history entries older than the given time.
	DeleteScoreHistoryBefore(ctx context.Context, before time.Time) (int64, error)

	// SaveBenchmarks persists network-wide benchmark data.
	SaveBenchmarks(ctx context.Context, b *NetworkBenchmarks) error

	// GetLatestBenchmarks returns the most recent network benchmarks.
	GetLatestBenchmarks(ctx context.Context) (*NetworkBenchmarks, error)
}
