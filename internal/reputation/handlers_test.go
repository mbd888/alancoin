package reputation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubMetricsProvider is a test double for MetricsProvider
type stubMetricsProvider struct {
	agents map[string]*Metrics
}

func (s *stubMetricsProvider) GetAgentMetrics(_ context.Context, address string) (*Metrics, error) {
	m, ok := s.agents[address]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", address)
	}
	return m, nil
}

func (s *stubMetricsProvider) GetAllAgentMetrics(_ context.Context) (map[string]*Metrics, error) {
	return s.agents, nil
}

func newTestHandler(agents map[string]*Metrics) *Handler {
	provider := &stubMetricsProvider{agents: agents}
	return NewHandler(provider)
}

func TestGetReputation(t *testing.T) {
	agents := map[string]*Metrics{
		"0xaaaa": {
			TotalTransactions:    50,
			TotalVolumeUSD:       500.0,
			SuccessfulTxns:       48,
			FailedTxns:           2,
			UniqueCounterparties: 10,
			DaysOnNetwork:        30,
			FirstSeen:            time.Now().Add(-30 * 24 * time.Hour),
			LastActive:           time.Now(),
		},
	}
	h := newTestHandler(agents)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/reputation/:address", h.GetReputation)
	req := httptest.NewRequest("GET", "/reputation/0xaaaa", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Reputation struct {
			Address    string  `json:"address"`
			Score      float64 `json:"score"`
			Tier       string  `json:"tier"`
			Components struct {
				VolumeScore    float64 `json:"volumeScore"`
				ActivityScore  float64 `json:"activityScore"`
				SuccessScore   float64 `json:"successScore"`
				AgeScore       float64 `json:"ageScore"`
				DiversityScore float64 `json:"diversityScore"`
			} `json:"components"`
			Metrics struct {
				TotalTransactions    int     `json:"totalTransactions"`
				TotalVolumeUSD       float64 `json:"totalVolumeUsd"`
				UniqueCounterparties int     `json:"uniqueCounterparties"`
				DaysOnNetwork        int     `json:"daysOnNetwork"`
			} `json:"metrics"`
		} `json:"reputation"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	rep := body.Reputation
	assert.Equal(t, "0xaaaa", rep.Address)
	assert.Greater(t, rep.Score, 0.0)
	assert.NotEmpty(t, rep.Tier)

	// With 50 txns, $500 volume, 96% success, 30 days, 10 counterparties
	// score should be in the "established" or "trusted" range
	assert.GreaterOrEqual(t, rep.Score, 40.0, "score should be at least established tier")

	// Components should all be > 0 with these metrics
	assert.Greater(t, rep.Components.VolumeScore, 0.0)
	assert.Greater(t, rep.Components.ActivityScore, 0.0)
	assert.Greater(t, rep.Components.SuccessScore, 0.0)
	assert.Greater(t, rep.Components.AgeScore, 0.0)
	assert.Greater(t, rep.Components.DiversityScore, 0.0)

	// Metrics should be passed through
	assert.Equal(t, 50, rep.Metrics.TotalTransactions)
	assert.Equal(t, 500.0, rep.Metrics.TotalVolumeUSD)
	assert.Equal(t, 10, rep.Metrics.UniqueCounterparties)
	assert.Equal(t, 30, rep.Metrics.DaysOnNetwork)
}

func TestGetReputationNotFound(t *testing.T) {
	h := newTestHandler(map[string]*Metrics{})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/reputation/:address", h.GetReputation)
	req := httptest.NewRequest("GET", "/reputation/0xunknown", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetLeaderboard(t *testing.T) {
	agents := map[string]*Metrics{
		"0xhigh": {
			TotalTransactions:    200,
			TotalVolumeUSD:       5000.0,
			SuccessfulTxns:       195,
			FailedTxns:           5,
			UniqueCounterparties: 25,
			DaysOnNetwork:        90,
			FirstSeen:            time.Now().Add(-90 * 24 * time.Hour),
			LastActive:           time.Now(),
		},
		"0xmed": {
			TotalTransactions:    30,
			TotalVolumeUSD:       200.0,
			SuccessfulTxns:       28,
			FailedTxns:           2,
			UniqueCounterparties: 5,
			DaysOnNetwork:        14,
			FirstSeen:            time.Now().Add(-14 * 24 * time.Hour),
			LastActive:           time.Now(),
		},
		"0xlow": {
			TotalTransactions:    3,
			TotalVolumeUSD:       5.0,
			SuccessfulTxns:       3,
			FailedTxns:           0,
			UniqueCounterparties: 2,
			DaysOnNetwork:        2,
			FirstSeen:            time.Now().Add(-2 * 24 * time.Hour),
			LastActive:           time.Now(),
		},
	}
	h := newTestHandler(agents)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/reputation", h.GetLeaderboard)
	req := httptest.NewRequest("GET", "/reputation?limit=10", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Leaderboard []struct {
			Rank      int     `json:"rank"`
			Address   string  `json:"address"`
			Score     float64 `json:"score"`
			Tier      string  `json:"tier"`
			TotalTxns int     `json:"totalTransactions"`
		} `json:"leaderboard"`
		Total int `json:"total"`
		Tiers struct {
			New         int `json:"new"`
			Emerging    int `json:"emerging"`
			Established int `json:"established"`
			Trusted     int `json:"trusted"`
			Elite       int `json:"elite"`
		} `json:"tiers"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	// Should have all 3 agents
	require.Len(t, body.Leaderboard, 3)
	assert.Equal(t, 3, body.Total)

	// Should be sorted by score descending
	assert.Equal(t, "0xhigh", body.Leaderboard[0].Address)
	assert.Greater(t, body.Leaderboard[0].Score, body.Leaderboard[1].Score)
	assert.Greater(t, body.Leaderboard[1].Score, body.Leaderboard[2].Score)

	// Ranks should be 1, 2, 3
	assert.Equal(t, 1, body.Leaderboard[0].Rank)
	assert.Equal(t, 2, body.Leaderboard[1].Rank)
	assert.Equal(t, 3, body.Leaderboard[2].Rank)

	// Tier distribution should sum to total agents
	tierSum := body.Tiers.New + body.Tiers.Emerging + body.Tiers.Established + body.Tiers.Trusted + body.Tiers.Elite
	assert.Equal(t, 3, tierSum)

	// High agent should be in a good tier
	assert.NotEqual(t, "new", body.Leaderboard[0].Tier)
}

func TestGetLeaderboardTierFilter(t *testing.T) {
	agents := map[string]*Metrics{
		"0xhigh": {
			TotalTransactions:    200,
			TotalVolumeUSD:       5000.0,
			SuccessfulTxns:       195,
			FailedTxns:           5,
			UniqueCounterparties: 25,
			DaysOnNetwork:        90,
		},
		"0xlow": {
			TotalTransactions:    3,
			TotalVolumeUSD:       5.0,
			SuccessfulTxns:       3,
			FailedTxns:           0,
			UniqueCounterparties: 2,
			DaysOnNetwork:        2,
		},
	}
	h := newTestHandler(agents)

	// Calculate the high agent's tier to filter by it
	calc := NewCalculator()
	highScore := calc.Calculate("0xhigh", *agents["0xhigh"])

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/reputation", h.GetLeaderboard)
	req := httptest.NewRequest("GET", "/reputation?tier="+string(highScore.Tier), nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Leaderboard []struct {
			Address string `json:"address"`
			Tier    string `json:"tier"`
		} `json:"leaderboard"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	// Only the high agent should match the tier filter
	require.Len(t, body.Leaderboard, 1)
	assert.Equal(t, "0xhigh", body.Leaderboard[0].Address)
	assert.Equal(t, string(highScore.Tier), body.Leaderboard[0].Tier)
}
