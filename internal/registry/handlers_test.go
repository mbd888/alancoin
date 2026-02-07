package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRepProvider is a test double for ReputationProvider
type stubRepProvider struct {
	scores map[string]struct {
		score float64
		tier  string
	}
}

func (s *stubRepProvider) GetScore(_ context.Context, address string) (float64, string, error) {
	if d, ok := s.scores[address]; ok {
		return d.score, d.tier, nil
	}
	return 0, "new", nil
}

func setupTestHandler(t *testing.T) (*Handler, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	h := NewHandler(store)
	return h, store
}

func doGET(handler gin.HandlerFunc, path string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.GET(path, handler)
	c.Request = httptest.NewRequest("GET", path, nil)
	r.ServeHTTP(w, c.Request)
	return w
}

func seedAgent(store *MemoryStore, addr, name string, services []Service) {
	ctx := context.Background()
	agent := &Agent{Address: addr, Name: name}
	_ = store.CreateAgent(ctx, agent)
	for _, svc := range services {
		_ = store.AddService(ctx, addr, &svc)
	}
}

func seedTx(store *MemoryStore, from, to, amount, status string) {
	ctx := context.Background()
	tx := &Transaction{From: from, To: to, Amount: amount, Status: status}
	_ = store.RecordTransaction(ctx, tx)
}

func TestDiscoverServicesWithReputation(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "RepAgent", []Service{
		{Type: "translation", Name: "Translate", Price: "0.01"},
	})
	// Add some transactions so reputation has data
	seedTx(store, addr, "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "0.50", "confirmed")
	seedTx(store, addr, "0xcccccccccccccccccccccccccccccccccccccccc", "0.30", "confirmed")

	// Wire a stub reputation provider
	rep := &stubRepProvider{scores: map[string]struct {
		score float64
		tier  string
	}{
		addr: {score: 72.5, tier: "trusted"},
	}}
	h.SetReputation(rep)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/services", h.DiscoverServices)
	req := httptest.NewRequest("GET", "/services", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Services []struct {
			AgentName       string  `json:"agentName"`
			ReputationScore float64 `json:"reputationScore"`
			ReputationTier  string  `json:"reputationTier"`
			SuccessRate     float64 `json:"successRate"`
			TxCount         int64   `json:"transactionCount"`
		} `json:"services"`
		Count int `json:"count"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, 1, body.Count)

	svc := body.Services[0]
	assert.Equal(t, "RepAgent", svc.AgentName)
	assert.Equal(t, 72.5, svc.ReputationScore)
	assert.Equal(t, "trusted", svc.ReputationTier)
}

func TestDiscoverServicesSortByReputation(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)

	addrA := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	addrB := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seedAgent(store, addrA, "LowRep", []Service{{Type: "code", Name: "Review", Price: "0.01"}})
	seedAgent(store, addrB, "HighRep", []Service{{Type: "code", Name: "Audit", Price: "0.05"}})

	rep := &stubRepProvider{scores: map[string]struct {
		score float64
		tier  string
	}{
		addrA: {score: 20, tier: "emerging"},
		addrB: {score: 85, tier: "elite"},
	}}
	h.SetReputation(rep)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/services", h.DiscoverServices)
	req := httptest.NewRequest("GET", "/services?sortBy=reputation", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Services []struct {
			AgentName       string  `json:"agentName"`
			ReputationScore float64 `json:"reputationScore"`
		} `json:"services"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Len(t, body.Services, 2)

	// HighRep should come first when sorted by reputation
	assert.Equal(t, "HighRep", body.Services[0].AgentName)
	assert.Equal(t, "LowRep", body.Services[1].AgentName)
}

func TestDiscoverServicesSortByValue(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)

	addrA := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	addrB := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// A: high reputation, high price → moderate value
	seedAgent(store, addrA, "Expensive", []Service{{Type: "data", Name: "Premium", Price: "1.00"}})
	// B: moderate reputation, very low price → high value
	seedAgent(store, addrB, "Bargain", []Service{{Type: "data", Name: "Basic", Price: "0.01"}})

	rep := &stubRepProvider{scores: map[string]struct {
		score float64
		tier  string
	}{
		addrA: {score: 90, tier: "elite"},
		addrB: {score: 50, tier: "established"},
	}}
	h.SetReputation(rep)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/services", h.DiscoverServices)
	req := httptest.NewRequest("GET", "/services?sortBy=value", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Services []struct {
			AgentName  string  `json:"agentName"`
			ValueScore float64 `json:"valueScore"`
		} `json:"services"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Len(t, body.Services, 2)

	// Bargain (50/0.01 = 5000) should beat Expensive (90/1.0 = 90) on value
	assert.Equal(t, "Bargain", body.Services[0].AgentName)
	assert.Greater(t, body.Services[0].ValueScore, body.Services[1].ValueScore)
}
