package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRACER_HighConfidence_NoExpansion(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.2
	racer := NewRACER(config, testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "p1", ReputationScore: 90, TraceRankScore: 10},
		{AgentAddress: "p2", ReputationScore: 20, TraceRankScore: 5},
	}

	if racer.ShouldExpand(candidates) {
		t.Error("expected no expansion with high confidence gap")
	}

	result, err := racer.Race(context.Background(), candidates, func(ctx context.Context, c ServiceCandidate) error {
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Expanded {
		t.Error("expected Expanded=false for high confidence")
	}
	if result.SetSize != 1 {
		t.Errorf("expected SetSize=1, got %d", result.SetSize)
	}
	if result.Candidate.AgentAddress != "p1" {
		t.Errorf("expected p1, got %q", result.Candidate.AgentAddress)
	}
}

func TestRACER_LowConfidence_Expansion(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.5 // Need 50% gap — similar scores will expand.
	config.MaxCandidates = 3
	racer := NewRACER(config, testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "p1", ReputationScore: 50, TraceRankScore: 10},
		{AgentAddress: "p2", ReputationScore: 48, TraceRankScore: 10},
		{AgentAddress: "p3", ReputationScore: 47, TraceRankScore: 10},
	}

	if !racer.ShouldExpand(candidates) {
		t.Error("expected expansion with low confidence gap")
	}

	var called atomic.Int32
	result, err := racer.Race(context.Background(), candidates, func(ctx context.Context, c ServiceCandidate) error {
		called.Add(1)
		time.Sleep(10 * time.Millisecond)
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Expanded {
		t.Error("expected Expanded=true for low confidence")
	}
	if result.SetSize != 3 {
		t.Errorf("expected SetSize=3, got %d", result.SetSize)
	}

	// All 3 should be started (though some may be cancelled).
	if called.Load() < 1 {
		t.Error("expected at least 1 candidate to be called")
	}
}

func TestRACER_ExpansionLimitedByMaxCandidates(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.9 // Almost always expand.
	config.MaxCandidates = 2
	racer := NewRACER(config, testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "p1", ReputationScore: 50, TraceRankScore: 10},
		{AgentAddress: "p2", ReputationScore: 49, TraceRankScore: 10},
		{AgentAddress: "p3", ReputationScore: 48, TraceRankScore: 10},
		{AgentAddress: "p4", ReputationScore: 47, TraceRankScore: 10},
	}

	set := racer.SelectCandidateSet(candidates)
	if len(set) != 2 {
		t.Errorf("expected 2 candidates (MaxCandidates), got %d", len(set))
	}
}

func TestRACER_FirstSuccessWins(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.9
	config.MaxCandidates = 3
	config.RaceTimeout = 5 * time.Second
	racer := NewRACER(config, testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "slow", ReputationScore: 50, TraceRankScore: 10},
		{AgentAddress: "fast", ReputationScore: 49, TraceRankScore: 10},
		{AgentAddress: "medium", ReputationScore: 48, TraceRankScore: 10},
	}

	result, err := racer.Race(context.Background(), candidates, func(ctx context.Context, c ServiceCandidate) error {
		switch c.AgentAddress {
		case "slow":
			select {
			case <-time.After(500 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		case "fast":
			return nil // Returns immediately.
		case "medium":
			select {
			case <-time.After(100 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Candidate.AgentAddress != "fast" {
		t.Errorf("expected 'fast' to win the race, got %q", result.Candidate.AgentAddress)
	}
}

func TestRACER_AllFail(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.9
	config.MaxCandidates = 2
	config.RaceTimeout = 1 * time.Second
	racer := NewRACER(config, testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "p1", ReputationScore: 50, TraceRankScore: 10},
		{AgentAddress: "p2", ReputationScore: 49, TraceRankScore: 10},
	}

	_, err := racer.Race(context.Background(), candidates, func(ctx context.Context, c ServiceCandidate) error {
		return errors.New("service unavailable")
	})

	if err == nil {
		t.Fatal("expected error when all candidates fail")
	}
}

func TestRACER_EmptyCandidates(t *testing.T) {
	racer := NewRACER(DefaultRACERConfig(), testLogger())

	_, err := racer.Race(context.Background(), nil, func(ctx context.Context, c ServiceCandidate) error {
		return nil
	})

	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestRACER_SingleCandidate_NoExpansion(t *testing.T) {
	racer := NewRACER(DefaultRACERConfig(), testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "only", ReputationScore: 50, TraceRankScore: 10},
	}

	if racer.ShouldExpand(candidates) {
		t.Error("expected no expansion for single candidate")
	}
}

func TestRACER_ExpansionCount(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.9 // Almost always expand.
	config.MaxCandidates = 2
	racer := NewRACER(config, testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "p1", ReputationScore: 50, TraceRankScore: 10},
		{AgentAddress: "p2", ReputationScore: 49, TraceRankScore: 10},
	}

	for i := 0; i < 5; i++ {
		_, _ = racer.Race(context.Background(), candidates, func(ctx context.Context, c ServiceCandidate) error {
			return nil
		})
	}

	if racer.ExpansionCount() != 5 {
		t.Errorf("expected 5 expansions, got %d", racer.ExpansionCount())
	}
	if racer.TotalRequests() != 5 {
		t.Errorf("expected 5 total requests, got %d", racer.TotalRequests())
	}
}

func TestRACER_ExpansionRate(t *testing.T) {
	racer := NewRACER(DefaultRACERConfig(), testLogger())

	// No requests yet.
	if racer.ExpansionRate() != 0 {
		t.Errorf("expected 0 expansion rate, got %f", racer.ExpansionRate())
	}
}

func TestRACER_OnExpansionCallback(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.9
	config.MaxCandidates = 2
	racer := NewRACER(config, testLogger())

	var expansionSizes []int
	racer.OnExpansion(func(setSize int) {
		expansionSizes = append(expansionSizes, setSize)
	})

	candidates := []ServiceCandidate{
		{AgentAddress: "p1", ReputationScore: 50, TraceRankScore: 10},
		{AgentAddress: "p2", ReputationScore: 49, TraceRankScore: 10},
	}

	_, _ = racer.Race(context.Background(), candidates, func(ctx context.Context, c ServiceCandidate) error {
		return nil
	})

	if len(expansionSizes) != 1 {
		t.Fatalf("expected 1 expansion callback, got %d", len(expansionSizes))
	}
	if expansionSizes[0] != 2 {
		t.Errorf("expected expansion size 2, got %d", expansionSizes[0])
	}
}

func TestRACER_Timeout(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.9
	config.MaxCandidates = 2
	config.RaceTimeout = 50 * time.Millisecond
	racer := NewRACER(config, testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "p1", ReputationScore: 50, TraceRankScore: 10},
		{AgentAddress: "p2", ReputationScore: 49, TraceRankScore: 10},
	}

	_, err := racer.Race(context.Background(), candidates, func(ctx context.Context, c ServiceCandidate) error {
		select {
		case <-time.After(5 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	if err == nil {
		t.Fatal("expected error on timeout")
	}
}

func TestRACER_SelectCandidateSet_HighConfidence(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.1
	racer := NewRACER(config, testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "top", ReputationScore: 90, TraceRankScore: 10},
		{AgentAddress: "second", ReputationScore: 20, TraceRankScore: 5},
	}

	set := racer.SelectCandidateSet(candidates)
	if len(set) != 1 {
		t.Errorf("expected 1 candidate for high confidence, got %d", len(set))
	}
	if set[0].AgentAddress != "top" {
		t.Errorf("expected 'top', got %q", set[0].AgentAddress)
	}
}

func TestRACER_SelectCandidateSet_Empty(t *testing.T) {
	racer := NewRACER(DefaultRACERConfig(), testLogger())
	set := racer.SelectCandidateSet(nil)
	if set != nil {
		t.Errorf("expected nil for empty candidates, got %v", set)
	}
}

func TestRACER_HighConfidence_TopCandidateFails(t *testing.T) {
	config := DefaultRACERConfig()
	config.ConfidenceThreshold = 0.1 // High confidence — no expansion.
	racer := NewRACER(config, testLogger())

	candidates := []ServiceCandidate{
		{AgentAddress: "top", ReputationScore: 90, TraceRankScore: 50},
		{AgentAddress: "second", ReputationScore: 10, TraceRankScore: 5},
	}

	_, err := racer.Race(context.Background(), candidates, func(ctx context.Context, c ServiceCandidate) error {
		return errors.New("top candidate failed")
	})

	if err == nil {
		t.Fatal("expected error when high-confidence top candidate fails")
	}
}
