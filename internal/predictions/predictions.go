// Package predictions allows verbal agents to make verifiable predictions.
//
// This is the "skin in the game" layer. Verbal agents don't just comment -
// they make predictions that are tracked and scored:
//
// - "AgentX will hit 1000 transactions this week" → verifiable
// - "Translation prices will drop 20% in 7 days" → verifiable
// - "This new agent will have >95% success rate" → verifiable
//
// Prediction accuracy becomes part of the verbal agent's reputation.
// Good predictors get followed. Bad predictors get ignored.
package predictions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrPredictionNotFound = errors.New("prediction not found")
	ErrAlreadyResolved    = errors.New("prediction already resolved")
	ErrNotYetResolvable   = errors.New("prediction cannot be resolved yet")
	ErrInvalidPrediction  = errors.New("invalid prediction")
)

// PredictionType categorizes predictions
type PredictionType string

const (
	TypeAgentMetric   PredictionType = "agent_metric"   // Agent will hit X transactions
	TypePriceMovement PredictionType = "price_movement" // Service price will change
	TypeMarketTrend   PredictionType = "market_trend"   // Overall market direction
	TypeAgentBehavior PredictionType = "agent_behavior" // Agent will do X
)

// PredictionStatus tracks prediction state
type PredictionStatus string

const (
	StatusPending PredictionStatus = "pending" // Waiting for resolution time
	StatusCorrect PredictionStatus = "correct" // Prediction was right
	StatusWrong   PredictionStatus = "wrong"   // Prediction was wrong
	StatusVoid    PredictionStatus = "void"    // Cannot be determined
)

// Prediction represents a verifiable prediction
type Prediction struct {
	ID         string         `json:"id"`
	AuthorAddr string         `json:"authorAddr"`
	AuthorName string         `json:"authorName"`
	Type       PredictionType `json:"type"`

	// The prediction itself
	Statement   string  `json:"statement"`   // Human-readable prediction
	TargetType  string  `json:"targetType"`  // "agent", "service_type", "market"
	TargetID    string  `json:"targetId"`    // Address or service type
	Metric      string  `json:"metric"`      // "tx_count", "price", "success_rate"
	Operator    string  `json:"operator"`    // ">", "<", "=", "change_pct"
	TargetValue float64 `json:"targetValue"` // The predicted value

	// Timing
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvesAt time.Time  `json:"resolvesAt"` // When can this be checked
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`

	// Resolution
	Status      PredictionStatus `json:"status"`
	ActualValue *float64         `json:"actualValue,omitempty"`

	// Engagement
	Agrees    int `json:"agrees"`    // People who agree
	Disagrees int `json:"disagrees"` // People who disagree

	// Reputation stake
	ConfidenceLevel int `json:"confidenceLevel"` // 1-5, higher = more reputation at stake
}

// PredictorStats tracks a verbal agent's prediction accuracy
type PredictorStats struct {
	Address          string  `json:"address"`
	TotalPredictions int     `json:"totalPredictions"`
	Correct          int     `json:"correct"`
	Wrong            int     `json:"wrong"`
	Pending          int     `json:"pending"`
	Accuracy         float64 `json:"accuracy"` // Correct / (Correct + Wrong)
	Streak           int     `json:"streak"`   // Current streak (positive = correct, negative = wrong)
	BestStreak       int     `json:"bestStreak"`
	ReputationScore  float64 `json:"reputationScore"` // Weighted by confidence
}

// Store persists predictions
type Store interface {
	Create(ctx context.Context, pred *Prediction) error
	Get(ctx context.Context, id string) (*Prediction, error)
	List(ctx context.Context, opts ListOptions) ([]*Prediction, error)
	ListByAuthor(ctx context.Context, authorAddr string, limit int) ([]*Prediction, error)
	ListPending(ctx context.Context) ([]*Prediction, error) // Ready to resolve
	Update(ctx context.Context, pred *Prediction) error

	// Engagement
	RecordVote(ctx context.Context, predictionID, agentAddr string, agrees bool) error

	// Stats
	GetPredictorStats(ctx context.Context, authorAddr string) (*PredictorStats, error)
	GetTopPredictors(ctx context.Context, limit int) ([]*PredictorStats, error)
}

// ListOptions for filtering predictions
type ListOptions struct {
	Limit      int
	Status     PredictionStatus
	Type       PredictionType
	TargetID   string
	AuthorAddr string
}

// MetricProvider fetches actual metric values for resolution
type MetricProvider interface {
	GetAgentMetric(ctx context.Context, agentAddr, metric string) (float64, error)
	GetServiceTypeMetric(ctx context.Context, serviceType, metric string) (float64, error)
	GetMarketMetric(ctx context.Context, metric string) (float64, error)
}

// Service manages predictions
type Service struct {
	store   Store
	metrics MetricProvider
}

// NewService creates a prediction service
func NewService(store Store, metrics MetricProvider) *Service {
	return &Service{store: store, metrics: metrics}
}

// Store returns the underlying store (for listing etc.)
func (s *Service) Store() Store {
	return s.store
}

// MakePrediction creates a new prediction
func (s *Service) MakePrediction(ctx context.Context, p *Prediction) (*Prediction, error) {
	// Validate
	if p.Statement == "" || p.TargetType == "" || p.Metric == "" {
		return nil, ErrInvalidPrediction
	}
	if p.ResolvesAt.Before(time.Now()) {
		return nil, errors.New("resolution time must be in the future")
	}
	if p.ConfidenceLevel < 1 {
		p.ConfidenceLevel = 1
	}
	if p.ConfidenceLevel > 5 {
		p.ConfidenceLevel = 5
	}

	p.ID = generateID("pred_")
	p.AuthorAddr = strings.ToLower(p.AuthorAddr)
	p.Status = StatusPending
	p.CreatedAt = time.Now()

	if err := s.store.Create(ctx, p); err != nil {
		return nil, err
	}

	return p, nil
}

// ResolvePredictions checks and resolves pending predictions
func (s *Service) ResolvePredictions(ctx context.Context) (resolved int, err error) {
	pending, err := s.store.ListPending(ctx)
	if err != nil {
		return 0, err
	}

	now := time.Now()

	for _, pred := range pending {
		// Skip if not yet resolvable
		if pred.ResolvesAt.After(now) {
			continue
		}

		// Get actual value
		var actualValue float64
		var fetchErr error

		switch pred.TargetType {
		case "agent":
			actualValue, fetchErr = s.metrics.GetAgentMetric(ctx, pred.TargetID, pred.Metric)
		case "service_type":
			actualValue, fetchErr = s.metrics.GetServiceTypeMetric(ctx, pred.TargetID, pred.Metric)
		case "market":
			actualValue, fetchErr = s.metrics.GetMarketMetric(ctx, pred.Metric)
		default:
			pred.Status = StatusVoid
			s.store.Update(ctx, pred)
			continue
		}

		if fetchErr != nil {
			// Can't determine, mark void
			pred.Status = StatusVoid
			s.store.Update(ctx, pred)
			continue
		}

		// Evaluate prediction
		pred.ActualValue = &actualValue
		pred.ResolvedAt = &now

		correct := s.evaluate(pred.Operator, actualValue, pred.TargetValue)
		if correct {
			pred.Status = StatusCorrect
		} else {
			pred.Status = StatusWrong
		}

		if err := s.store.Update(ctx, pred); err != nil {
			continue
		}

		resolved++
	}

	return resolved, nil
}

func (s *Service) evaluate(operator string, actual, target float64) bool {
	switch operator {
	case ">":
		return actual > target
	case ">=":
		return actual >= target
	case "<":
		return actual < target
	case "<=":
		return actual <= target
	case "=", "==":
		// Allow 5% tolerance for equality
		tolerance := target * 0.05
		return actual >= target-tolerance && actual <= target+tolerance
	case "change_pct":
		// Target is percentage change
		// This requires baseline, which should be stored
		return false // Simplified for now
	default:
		return false
	}
}

// GetLeaderboard returns top predictors by accuracy
func (s *Service) GetLeaderboard(ctx context.Context, limit int) ([]*PredictorStats, error) {
	return s.store.GetTopPredictors(ctx, limit)
}

// Vote records agreement/disagreement with a prediction
func (s *Service) Vote(ctx context.Context, predictionID, agentAddr string, agrees bool) error {
	pred, err := s.store.Get(ctx, predictionID)
	if err != nil {
		return err
	}
	if pred.Status != StatusPending {
		return errors.New("can only vote on pending predictions")
	}

	return s.store.RecordVote(ctx, predictionID, strings.ToLower(agentAddr), agrees)
}

func generateID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
