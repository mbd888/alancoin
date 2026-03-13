package tracerank

import "context"

// Store persists TraceRank computation results.
type Store interface {
	// SaveScores persists a batch of scores from a single computation run.
	SaveScores(ctx context.Context, scores []*AgentScore, runID string) error

	// GetScore returns the latest score for an agent.
	GetScore(ctx context.Context, address string) (*AgentScore, error)

	// GetScores returns scores for multiple agents.
	GetScores(ctx context.Context, addresses []string) (map[string]*AgentScore, error)

	// GetTopScores returns the top N agents by GraphScore.
	GetTopScores(ctx context.Context, limit int) ([]*AgentScore, error)

	// GetRunHistory returns metadata for recent computation runs.
	GetRunHistory(ctx context.Context, limit int) ([]*RunMetadata, error)
}

// RunMetadata records information about a TraceRank computation run.
type RunMetadata struct {
	RunID      string  `json:"runId"`
	NodeCount  int     `json:"nodeCount"`
	EdgeCount  int     `json:"edgeCount"`
	Iterations int     `json:"iterations"`
	Converged  bool    `json:"converged"`
	DurationMs int64   `json:"durationMs"`
	MaxScore   float64 `json:"maxScore"`
	MeanScore  float64 `json:"meanScore"`
	ComputedAt string  `json:"computedAt"`
}
