package reputation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func newTestHandlerFull(agents map[string]*Metrics, store SnapshotStore, signer *Signer) *Handler {
	provider := &stubMetricsProvider{agents: agents}
	return NewHandlerFull(provider, store, signer)
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

func TestGetBatchReputation(t *testing.T) {
	agents := map[string]*Metrics{
		"0xaaaa": {
			TotalTransactions:    50,
			TotalVolumeUSD:       500.0,
			SuccessfulTxns:       48,
			FailedTxns:           2,
			UniqueCounterparties: 10,
			DaysOnNetwork:        30,
		},
		"0xbbbb": {
			TotalTransactions:    10,
			TotalVolumeUSD:       50.0,
			SuccessfulTxns:       9,
			FailedTxns:           1,
			UniqueCounterparties: 3,
			DaysOnNetwork:        7,
		},
	}
	h := newTestHandler(agents)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/reputation/batch", h.GetBatchReputation)

	body, _ := json.Marshal(BatchRequest{Addresses: []string{"0xaaaa", "0xbbbb", "0xunknown"}})
	req := httptest.NewRequest("POST", "/reputation/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp BatchResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	require.Len(t, resp.Scores, 3)

	// Known agents should have positive scores
	assert.Greater(t, resp.Scores[0].Reputation.Score, 0.0)
	assert.Greater(t, resp.Scores[1].Reputation.Score, 0.0)

	// Unknown agent should have zero score and "new" tier
	assert.Equal(t, 0.0, resp.Scores[2].Reputation.Score)
	assert.Equal(t, TierNew, resp.Scores[2].Reputation.Tier)

	// No signing without a signer
	assert.Empty(t, resp.Signature)
}

func TestGetBatchReputationEmpty(t *testing.T) {
	h := newTestHandler(map[string]*Metrics{})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/reputation/batch", h.GetBatchReputation)

	body, _ := json.Marshal(BatchRequest{Addresses: []string{}})
	req := httptest.NewRequest("POST", "/reputation/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetBatchReputationTooMany(t *testing.T) {
	h := newTestHandler(map[string]*Metrics{})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/reputation/batch", h.GetBatchReputation)

	addrs := make([]string, 101)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("0x%04d", i)
	}
	body, _ := json.Marshal(BatchRequest{Addresses: addrs})
	req := httptest.NewRequest("POST", "/reputation/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetReputationHistory(t *testing.T) {
	agents := map[string]*Metrics{
		"0xaaaa": {
			TotalTransactions: 50,
			TotalVolumeUSD:    500.0,
			SuccessfulTxns:    48,
			FailedTxns:        2,
			DaysOnNetwork:     30,
		},
	}
	store := NewMemorySnapshotStore()

	// Seed store with snapshots at different times
	now := time.Now()
	for i := 0; i < 5; i++ {
		snap := &Snapshot{
			Address:   "0xaaaa",
			Score:     50.0 + float64(i),
			Tier:      TierEstablished,
			CreatedAt: now.Add(-time.Duration(5-i) * time.Hour),
		}
		err := store.Save(context.Background(), snap)
		require.NoError(t, err)
	}

	h := newTestHandlerFull(agents, store, nil)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/reputation/:address/history", h.GetReputationHistory)

	req := httptest.NewRequest("GET", "/reputation/0xaaaa/history?limit=3", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Address   string      `json:"address"`
		Snapshots []*Snapshot `json:"snapshots"`
		Count     int         `json:"count"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "0xaaaa", resp.Address)
	assert.Equal(t, 3, resp.Count)
	require.Len(t, resp.Snapshots, 3)

	// Should be sorted by time descending (most recent first)
	assert.Greater(t, resp.Snapshots[0].Score, resp.Snapshots[1].Score)
}

func TestGetReputationHistoryNoStore(t *testing.T) {
	agents := map[string]*Metrics{
		"0xaaaa": {TotalTransactions: 10},
	}
	h := newTestHandlerFull(agents, nil, nil)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/reputation/:address/history", h.GetReputationHistory)

	req := httptest.NewRequest("GET", "/reputation/0xaaaa/history", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestGetReputationWithSigning(t *testing.T) {
	agents := map[string]*Metrics{
		"0xaaaa": {
			TotalTransactions:    50,
			TotalVolumeUSD:       500.0,
			SuccessfulTxns:       48,
			FailedTxns:           2,
			UniqueCounterparties: 10,
			DaysOnNetwork:        30,
		},
	}
	signer := NewSigner("test-hmac-secret")
	h := newTestHandlerFull(agents, nil, signer)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/reputation/:address", h.GetReputation)

	req := httptest.NewRequest("GET", "/reputation/0xaaaa", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Reputation *Score `json:"reputation"`
		Signature  string `json:"signature"`
		IssuedAt   string `json:"issuedAt"`
		ExpiresAt  string `json:"expiresAt"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotEmpty(t, resp.Signature)
	assert.NotEmpty(t, resp.IssuedAt)
	assert.NotEmpty(t, resp.ExpiresAt)

	// Verify issuedAt is before expiresAt
	issued, err := time.Parse(time.RFC3339, resp.IssuedAt)
	require.NoError(t, err)
	expires, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	require.NoError(t, err)
	assert.True(t, expires.After(issued))
	assert.Equal(t, 5*time.Minute, expires.Sub(issued))

	// Verify signature is valid
	assert.True(t, signer.Verify(resp.Reputation, resp.Signature))
}

func TestGetBatchReputationWithSigning(t *testing.T) {
	agents := map[string]*Metrics{
		"0xaaaa": {
			TotalTransactions: 50,
			TotalVolumeUSD:    500.0,
			SuccessfulTxns:    48,
			FailedTxns:        2,
			DaysOnNetwork:     30,
		},
	}
	signer := NewSigner("test-hmac-secret")
	h := newTestHandlerFull(agents, nil, signer)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/reputation/batch", h.GetBatchReputation)

	body, _ := json.Marshal(BatchRequest{Addresses: []string{"0xaaaa"}})
	req := httptest.NewRequest("POST", "/reputation/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp BatchResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// Batch-level signature
	assert.NotEmpty(t, resp.Signature)
	assert.NotEmpty(t, resp.IssuedAt)

	// Individual score signature
	require.Len(t, resp.Scores, 1)
	assert.NotEmpty(t, resp.Scores[0].Signature)
}

func TestGetReputationHistoryWithTimeRange(t *testing.T) {
	agents := map[string]*Metrics{
		"0xaaaa": {TotalTransactions: 10},
	}
	store := NewMemorySnapshotStore()

	now := time.Now()
	// Create 3 snapshots: 3h ago, 2h ago, 1h ago
	for i := 3; i >= 1; i-- {
		snap := &Snapshot{
			Address:   "0xaaaa",
			Score:     float64(40 + i),
			Tier:      TierEstablished,
			CreatedAt: now.Add(-time.Duration(i) * time.Hour),
		}
		_ = store.Save(context.Background(), snap)
	}

	h := newTestHandlerFull(agents, store, nil)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/reputation/:address/history", h.GetReputationHistory)

	// Query only the middle snapshot (from 2.5h ago to 1.5h ago)
	from := now.Add(-150 * time.Minute).Format(time.RFC3339)
	to := now.Add(-90 * time.Minute).Format(time.RFC3339)
	req := httptest.NewRequest("GET", "/reputation/0xaaaa/history?from="+from+"&to="+to, nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Snapshots []*Snapshot `json:"snapshots"`
		Count     int         `json:"count"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, 1, resp.Count)
	require.Len(t, resp.Snapshots, 1)
	assert.Equal(t, 42.0, resp.Snapshots[0].Score)
}

func TestGetReputationAddressNormalization(t *testing.T) {
	agents := map[string]*Metrics{
		"0xaaaa": {
			TotalTransactions: 10,
			TotalVolumeUSD:    100.0,
			SuccessfulTxns:    10,
			DaysOnNetwork:     5,
		},
	}
	h := newTestHandler(agents)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/reputation/:address", h.GetReputation)

	// Request with uppercase address should still work (normalized to lowercase)
	req := httptest.NewRequest("GET", "/reputation/"+strings.ToUpper("0xaaaa"), nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}
