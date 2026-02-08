package predictions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory implementation of Store for testing
type MemoryStore struct {
	mu          sync.RWMutex
	predictions map[string]*Prediction     // id -> prediction
	votes       map[string]map[string]bool // predictionID -> agentAddr -> agrees
	stats       map[string]*PredictorStats // authorAddr -> stats
}

// NewMemoryStore creates a new in-memory predictions store
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		predictions: make(map[string]*Prediction),
		votes:       make(map[string]map[string]bool),
		stats:       make(map[string]*PredictorStats),
	}
}

// Compile-time interface check
var _ Store = (*MemoryStore)(nil)

// Create stores a new prediction
func (m *MemoryStore) Create(ctx context.Context, pred *Prediction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if pred.ID == "" {
		pred.ID = m.generateID("pred_")
	}
	if pred.CreatedAt.IsZero() {
		pred.CreatedAt = time.Now()
	}
	pred.AuthorAddr = strings.ToLower(pred.AuthorAddr)
	m.predictions[pred.ID] = pred

	// Update stats
	m.ensureStats(pred.AuthorAddr)
	m.stats[pred.AuthorAddr].TotalPredictions++
	m.stats[pred.AuthorAddr].Pending++

	return nil
}

// Get retrieves a prediction by ID
func (m *MemoryStore) Get(ctx context.Context, id string) (*Prediction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pred, ok := m.predictions[id]
	if !ok {
		return nil, ErrPredictionNotFound
	}
	copy := *pred
	return &copy, nil
}

// List returns predictions with filters
func (m *MemoryStore) List(ctx context.Context, opts ListOptions) ([]*Prediction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var predictions []*Prediction
	for _, pred := range m.predictions {
		// Apply filters
		if opts.Status != "" && pred.Status != opts.Status {
			continue
		}
		if opts.Type != "" && pred.Type != opts.Type {
			continue
		}
		if opts.AuthorAddr != "" && !strings.EqualFold(pred.AuthorAddr, opts.AuthorAddr) {
			continue
		}
		if opts.TargetID != "" && pred.TargetID != opts.TargetID {
			continue
		}
		copy := *pred
		predictions = append(predictions, &copy)
	}

	// Sort by created_at descending
	sort.Slice(predictions, func(i, j int) bool {
		return predictions[i].CreatedAt.After(predictions[j].CreatedAt)
	})

	// Apply limit
	if opts.Limit > 0 && len(predictions) > opts.Limit {
		predictions = predictions[:opts.Limit]
	}

	return predictions, nil
}

// ListByAuthor returns predictions by author
func (m *MemoryStore) ListByAuthor(ctx context.Context, authorAddr string, limit int) ([]*Prediction, error) {
	return m.List(ctx, ListOptions{AuthorAddr: authorAddr, Limit: limit})
}

// ListPending returns pending predictions ready to resolve
func (m *MemoryStore) ListPending(ctx context.Context) ([]*Prediction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	var predictions []*Prediction
	for _, pred := range m.predictions {
		if pred.Status == StatusPending && !pred.ResolvesAt.After(now) {
			copy := *pred
			predictions = append(predictions, &copy)
		}
	}

	// Sort by resolves_at ascending
	sort.Slice(predictions, func(i, j int) bool {
		return predictions[i].ResolvesAt.Before(predictions[j].ResolvesAt)
	})

	return predictions, nil
}

// Update updates a prediction (mainly for resolution)
func (m *MemoryStore) Update(ctx context.Context, pred *Prediction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.predictions[pred.ID]
	if !ok {
		return ErrPredictionNotFound
	}

	// Update stats if status changed
	if existing.Status == StatusPending && pred.Status != StatusPending {
		author := strings.ToLower(pred.AuthorAddr)
		m.ensureStats(author)
		m.stats[author].Pending--

		switch pred.Status {
		case StatusCorrect:
			m.stats[author].Correct++
			if m.stats[author].Streak >= 0 {
				m.stats[author].Streak++
			} else {
				m.stats[author].Streak = 1
			}
			if m.stats[author].Streak > m.stats[author].BestStreak {
				m.stats[author].BestStreak = m.stats[author].Streak
			}
		case StatusWrong:
			m.stats[author].Wrong++
			if m.stats[author].Streak <= 0 {
				m.stats[author].Streak--
			} else {
				m.stats[author].Streak = -1
			}
		}

		// Recalculate accuracy
		total := m.stats[author].Correct + m.stats[author].Wrong
		if total > 0 {
			m.stats[author].Accuracy = float64(m.stats[author].Correct) / float64(total)
		}
	}

	m.predictions[pred.ID] = pred
	return nil
}

// RecordVote records a vote on a prediction
func (m *MemoryStore) RecordVote(ctx context.Context, predictionID, agentAddr string, agrees bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pred, ok := m.predictions[predictionID]
	if !ok {
		return ErrPredictionNotFound
	}

	if m.votes[predictionID] == nil {
		m.votes[predictionID] = make(map[string]bool)
	}

	addr := strings.ToLower(agentAddr)
	previousVote, hasVoted := m.votes[predictionID][addr]

	if hasVoted {
		// Remove previous vote
		if previousVote {
			pred.Agrees--
		} else {
			pred.Disagrees--
		}
	}

	// Record new vote
	m.votes[predictionID][addr] = agrees
	if agrees {
		pred.Agrees++
	} else {
		pred.Disagrees++
	}

	return nil
}

// GetPredictorStats returns stats for a predictor
func (m *MemoryStore) GetPredictorStats(ctx context.Context, authorAddr string) (*PredictorStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(authorAddr)
	stats, ok := m.stats[addr]
	if !ok {
		return &PredictorStats{Address: addr}, nil
	}
	copy := *stats
	return &copy, nil
}

// GetTopPredictors returns predictors by accuracy
func (m *MemoryStore) GetTopPredictors(ctx context.Context, limit int) ([]*PredictorStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var predictors []*PredictorStats
	for _, stats := range m.stats {
		if stats.TotalPredictions >= 5 { // Minimum threshold
			copy := *stats
			predictors = append(predictors, &copy)
		}
	}

	// Sort by reputation/accuracy descending
	sort.Slice(predictors, func(i, j int) bool {
		if predictors[i].ReputationScore != predictors[j].ReputationScore {
			return predictors[i].ReputationScore > predictors[j].ReputationScore
		}
		return predictors[i].Correct > predictors[j].Correct
	})

	if limit > 0 && len(predictors) > limit {
		predictors = predictors[:limit]
	}

	return predictors, nil
}

func (m *MemoryStore) ensureStats(authorAddr string) {
	if m.stats[authorAddr] == nil {
		m.stats[authorAddr] = &PredictorStats{
			Address:         authorAddr,
			ReputationScore: 50.0, // Default reputation
		}
	}
}

func (m *MemoryStore) generateID(prefix string) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}
