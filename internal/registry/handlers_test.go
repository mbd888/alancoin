package registry

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

// stubVerProvider is a test double for VerificationProvider
type stubVerProvider struct {
	verified   map[string]bool
	guarantees map[string]struct {
		successRate float64
		premiumRate float64
	}
}

func (s *stubVerProvider) IsVerified(_ context.Context, agentAddr string) (bool, error) {
	v, ok := s.verified[agentAddr]
	if !ok {
		return false, nil
	}
	return v, nil
}

func (s *stubVerProvider) GetGuarantee(_ context.Context, agentAddr string) (float64, float64, error) {
	if g, ok := s.guarantees[agentAddr]; ok {
		return g.successRate, g.premiumRate, nil
	}
	return 0, 0, fmt.Errorf("no guarantee")
}

// stubTxVerifier is a test double for TxVerifier
type stubTxVerifier struct {
	result bool
	err    error
}

func (s *stubTxVerifier) VerifyPayment(_ context.Context, _, _, _ string) (bool, error) {
	return s.result, s.err
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

func init() {
	gin.SetMode(gin.TestMode)
}

// setupRouter creates a gin test router with all registry routes registered.
func setupRouter(h *Handler) *gin.Engine {
	r := gin.New()
	api := r.Group("/api/v1")
	h.RegisterRoutes(api)
	return r
}

// jsonBody marshals v to a *bytes.Reader for use as a request body.
func jsonBody(t *testing.T, v interface{}) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewReader(b)
}

// --- RegisterAgent tests ---

func TestRegisterAgent_Success(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	h.SetAllowLocalEndpoints(true)
	r := setupRouter(h)

	body := RegisterAgentRequest{
		Address:  "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:     "TestBot",
		Endpoint: "http://localhost:8080/api",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var agent Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agent))
	assert.Equal(t, "TestBot", agent.Name)
}

func TestRegisterAgent_InvalidBody(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(`{"name":""}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegisterAgent_InvalidAddress(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	body := RegisterAgentRequest{Address: "not-an-address", Name: "Bad"}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_address")
}

func TestRegisterAgent_Duplicate(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	h.SetAllowLocalEndpoints(true)
	r := setupRouter(h)

	body := RegisterAgentRequest{
		Address: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:    "TestBot",
	}

	// First registration
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Second registration - duplicate
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/agents", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "agent_exists")
}

func TestRegisterAgent_SSRFBlocked(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	// allowLocalEndpoints is false by default
	r := setupRouter(h)

	body := RegisterAgentRequest{
		Address:  "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:     "SSRFBot",
		Endpoint: "http://127.0.0.1:9999/evil",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_endpoint")
}

func TestRegisterAgent_WithOwnerAddress(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	h.SetAllowLocalEndpoints(true)
	r := setupRouter(h)

	body := RegisterAgentRequest{
		Address:      "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:         "SessionKeyBot",
		OwnerAddress: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var agent Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agent))
	assert.True(t, agent.IsAutonomous)
}

// --- GetAgent tests ---

func TestGetAgent_Success(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "TestAgent", nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agents/"+addr, nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var agent Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agent))
	assert.Equal(t, "TestAgent", agent.Name)
}

func TestGetAgent_NotFound(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agents/0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "not_found")
}

// --- ListAgents tests ---

func TestListAgents_Success(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	seedAgent(store, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Agent1", nil)
	seedAgent(store, "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "Agent2", nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agents", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Agents []Agent `json:"agents"`
		Count  int     `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 2, body.Count)
}

func TestListAgents_FilterByServiceType(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	seedAgent(store, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "InferenceBot", []Service{
		{Type: "inference", Name: "GPT", Price: "0.01"},
	})
	seedAgent(store, "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "TranslateBot", []Service{
		{Type: "translation", Name: "Translate", Price: "0.005"},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agents?serviceType=inference", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Agents []Agent `json:"agents"`
		Count  int     `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Count)
	assert.Equal(t, "InferenceBot", body.Agents[0].Name)
}

func TestListAgents_WithActiveFilter(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	seedAgent(store, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Agent1", nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agents?active=true", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// --- DeleteAgent tests ---

func TestDeleteAgent_Success(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "DeleteMe", nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/agents/"+addr, nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify deleted
	_, err := store.GetAgent(context.Background(), addr)
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestDeleteAgent_NotFound(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/agents/0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- AddService tests ---

func TestAddService_Success(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	h.SetAllowLocalEndpoints(true)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", nil)

	body := AddServiceRequest{
		Type:     "inference",
		Name:     "GPT-4 API",
		Price:    "0.001",
		Endpoint: "http://localhost:8080/infer",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents/"+addr+"/services", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var svc Service
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &svc))
	assert.Equal(t, "GPT-4 API", svc.Name)
	assert.NotEmpty(t, svc.ID)
}

func TestAddService_InvalidBody(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents/"+addr+"/services", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddService_InvalidPrice(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", nil)

	body := AddServiceRequest{
		Type:  "inference",
		Name:  "Bad Service",
		Price: "not-a-number",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents/"+addr+"/services", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "validation_failed")
}

func TestAddService_AgentNotFound(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	h.SetAllowLocalEndpoints(true)
	r := setupRouter(h)

	body := AddServiceRequest{
		Type:  "inference",
		Name:  "Orphan",
		Price: "0.001",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents/0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead/services", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAddService_SSRFBlocked(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	// allowLocalEndpoints false by default
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", nil)

	body := AddServiceRequest{
		Type:     "inference",
		Name:     "Evil",
		Price:    "0.001",
		Endpoint: "http://169.254.169.254/metadata",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/agents/"+addr+"/services", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_endpoint")
}

// --- UpdateService tests ---

func TestUpdateService_Success(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	h.SetAllowLocalEndpoints(true)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", []Service{
		{Type: "inference", Name: "GPT-4", Price: "0.001"},
	})

	// Get the service ID
	agent, _ := store.GetAgent(context.Background(), addr)
	svcID := agent.Services[0].ID

	update := Service{
		Type:  "inference",
		Name:  "GPT-4 Turbo",
		Price: "0.002",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/agents/"+addr+"/services/"+svcID, jsonBody(t, update))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var svc Service
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &svc))
	assert.Equal(t, "GPT-4 Turbo", svc.Name)
	assert.Equal(t, svcID, svc.ID)
}

func TestUpdateService_InvalidBody(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/agents/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/services/svc_123", strings.NewReader(`invalid`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateService_InvalidPrice(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", []Service{
		{Type: "inference", Name: "GPT-4", Price: "0.001"},
	})
	agent, _ := store.GetAgent(context.Background(), addr)
	svcID := agent.Services[0].ID

	update := Service{Price: "bad-price"}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/agents/"+addr+"/services/"+svcID, jsonBody(t, update))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "validation_failed")
}

func TestUpdateService_NotFound(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	h.SetAllowLocalEndpoints(true)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", nil)

	update := Service{Name: "Ghost", Price: "0.001"}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/agents/"+addr+"/services/svc_nonexistent", jsonBody(t, update))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateService_AgentNotFound(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	h.SetAllowLocalEndpoints(true)
	r := setupRouter(h)

	update := Service{Name: "Ghost", Price: "0.001"}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/agents/0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead/services/svc_123", jsonBody(t, update))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateService_SSRFBlocked(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	// SSRF validation enabled by default
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", []Service{
		{Type: "inference", Name: "GPT-4", Price: "0.001"},
	})
	agent, _ := store.GetAgent(context.Background(), addr)
	svcID := agent.Services[0].ID

	update := Service{
		Name:     "Evil",
		Endpoint: "http://10.0.0.1:8080/internal",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/agents/"+addr+"/services/"+svcID, jsonBody(t, update))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_endpoint")
}

// --- RemoveService tests ---

func TestRemoveService_Success(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", []Service{
		{Type: "inference", Name: "GPT-4", Price: "0.001"},
	})

	agent, _ := store.GetAgent(context.Background(), addr)
	svcID := agent.Services[0].ID

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/agents/"+addr+"/services/"+svcID, nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestRemoveService_NotFound(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/agents/"+addr+"/services/svc_nonexistent", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- ListTransactions handler test ---

func TestListTransactions_Handler(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr1 := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	addr2 := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seedAgent(store, addr1, "Agent1", nil)
	seedAgent(store, addr2, "Agent2", nil)
	seedTx(store, addr1, addr2, "0.50", "confirmed")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agents/"+addr1+"/transactions?limit=10", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Transactions []Transaction `json:"transactions"`
		Count        int           `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Count)
}

// --- GetNetworkStats handler test ---

func TestGetNetworkStats_Handler(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	seedAgent(store, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Agent1", nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/network/stats", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var stats NetworkStats
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stats))
	assert.Equal(t, int64(1), stats.TotalAgents)
}

// --- GetPublicFeed handler test ---

func TestGetPublicFeed_Success(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr1 := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	addr2 := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seedAgent(store, addr1, "Buyer", nil)
	seedAgent(store, addr2, "Seller", []Service{
		{Type: "inference", Name: "GPT-4", Price: "0.01"},
	})
	seedTx(store, addr1, addr2, "0.01", "confirmed")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/feed?limit=10", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Feed    []FeedItem `json:"feed"`
		Stats   struct{}   `json:"stats"`
		Message string     `json:"message"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Feed, 1)
	assert.Equal(t, "Buyer", body.Feed[0].FromName)
	assert.Equal(t, "Seller", body.Feed[0].ToName)
	assert.NotEmpty(t, body.Message)
}

func TestGetPublicFeed_UnknownAgentsTruncated(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	// Transaction from/to addresses that are NOT registered agents
	from := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	to := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seedTx(store, from, to, "1.00", "confirmed")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/feed", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Feed []FeedItem `json:"feed"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Feed, 1)
	// Unknown agents get truncated addresses
	assert.Equal(t, "0xaaaa...aaaa", body.Feed[0].FromName)
}

func TestGetPublicFeed_LimitCapped(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	// Request limit > 100 should be capped
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/feed?limit=999", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// --- RecordTransaction handler tests ---

func TestRecordTransaction_MissingAuth(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	body := RecordTransactionRequest{
		TxHash: "0xabc123",
		From:   "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		To:     "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Amount: "0.50",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/transactions", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "unauthorized")
}

func TestRecordTransaction_Success(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)

	fromAddr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	toAddr := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seedAgent(store, fromAddr, "Sender", nil)
	seedAgent(store, toAddr, "Receiver", nil)

	// Build a router that injects auth context
	r := gin.New()
	r.POST("/api/v1/transactions", func(c *gin.Context) {
		c.Set("authAgentAddr", fromAddr)
		h.RecordTransaction(c)
	})

	body := RecordTransactionRequest{
		TxHash: "0xabc123",
		From:   fromAddr,
		To:     toAddr,
		Amount: "0.50",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/transactions", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var tx Transaction
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tx))
	assert.Equal(t, "confirmed", tx.Status)
	assert.Equal(t, "0.50", tx.Amount)
}

func TestRecordTransaction_InvalidBody(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/transactions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRecordTransaction_SelfTrade(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	r := gin.New()
	r.POST("/api/v1/transactions", func(c *gin.Context) {
		c.Set("authAgentAddr", addr)
		h.RecordTransaction(c)
	})

	body := RecordTransactionRequest{
		TxHash: "0xabc123",
		From:   addr,
		To:     addr,
		Amount: "1.00",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/transactions", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "self_trade")
}

func TestRecordTransaction_ForbiddenSpoofedSender(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)

	authAddr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	spoofedAddr := "0xcccccccccccccccccccccccccccccccccccccccc"

	r := gin.New()
	r.POST("/api/v1/transactions", func(c *gin.Context) {
		c.Set("authAgentAddr", authAddr)
		h.RecordTransaction(c)
	})

	body := RecordTransactionRequest{
		TxHash: "0xabc123",
		From:   spoofedAddr,
		To:     "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Amount: "1.00",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/transactions", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "forbidden")
}

func TestRecordTransaction_WithVerifier(t *testing.T) {
	tests := []struct {
		name           string
		verifyResult   bool
		verifyErr      error
		expectedStatus string
	}{
		{
			name:           "verified",
			verifyResult:   true,
			expectedStatus: "confirmed",
		},
		{
			name:           "not verified",
			verifyResult:   false,
			expectedStatus: "failed",
		},
		{
			name:           "verify error",
			verifyErr:      fmt.Errorf("rpc error"),
			expectedStatus: "pending",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			h := NewHandler(store)
			h.SetVerifier(&stubTxVerifier{result: tt.verifyResult, err: tt.verifyErr})

			fromAddr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			toAddr := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

			r := gin.New()
			r.POST("/tx", func(c *gin.Context) {
				c.Set("authAgentAddr", fromAddr)
				h.RecordTransaction(c)
			})

			body := RecordTransactionRequest{
				TxHash: "0x" + tt.name,
				From:   fromAddr,
				To:     toAddr,
				Amount: "1.00",
			}

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/tx", jsonBody(t, body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusCreated, w.Code)

			var tx Transaction
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tx))
			assert.Equal(t, tt.expectedStatus, tx.Status)
		})
	}
}

func TestRecordTransaction_ValidationErrors(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)

	fromAddr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	r := gin.New()
	r.POST("/tx", func(c *gin.Context) {
		c.Set("authAgentAddr", fromAddr)
		h.RecordTransaction(c)
	})

	body := RecordTransactionRequest{
		TxHash: "0xabc",
		From:   "bad-addr",
		To:     "also-bad",
		Amount: "not-a-number",
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/tx", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "validation_failed")
}

// --- DiscoverServices tests ---

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
	// A: high reputation, high price -> moderate value
	seedAgent(store, addrA, "Expensive", []Service{{Type: "data", Name: "Premium", Price: "1.00"}})
	// B: moderate reputation, very low price -> high value
	seedAgent(store, addrB, "Bargain", []Service{{Type: "data", Name: "Basic", Price: "0.01"}})

	rep := &stubRepProvider{scores: map[string]struct {
		score float64
		tier  string
	}{
		addrA: {score: 90, tier: "elite"},
		addrB: {score: 50, tier: "established"},
	}}
	h.SetReputation(rep)

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

func TestDiscoverServices_WithVerification(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)

	addrA := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	addrB := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seedAgent(store, addrA, "Verified", []Service{{Type: "code", Name: "Audit", Price: "0.05"}})
	seedAgent(store, addrB, "Unverified", []Service{{Type: "code", Name: "Review", Price: "0.01"}})

	ver := &stubVerProvider{
		verified: map[string]bool{
			addrA: true,
			addrB: false,
		},
		guarantees: map[string]struct {
			successRate float64
			premiumRate float64
		}{
			addrA: {successRate: 95.0, premiumRate: 0.05},
		},
	}
	h.SetVerification(ver)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/services", h.DiscoverServices)
	req := httptest.NewRequest("GET", "/services", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Services []struct {
			AgentName             string  `json:"agentName"`
			Verified              bool    `json:"verified"`
			GuaranteedSuccessRate float64 `json:"guaranteedSuccessRate"`
			GuaranteePremiumRate  float64 `json:"guaranteePremiumRate"`
		} `json:"services"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Services, 2)

	// Find the verified agent in the results
	var verifiedAgent, unverifiedAgent struct {
		AgentName             string  `json:"agentName"`
		Verified              bool    `json:"verified"`
		GuaranteedSuccessRate float64 `json:"guaranteedSuccessRate"`
		GuaranteePremiumRate  float64 `json:"guaranteePremiumRate"`
	}
	for _, s := range body.Services {
		if s.AgentName == "Verified" {
			verifiedAgent = s
		} else {
			unverifiedAgent = s
		}
	}
	assert.True(t, verifiedAgent.Verified)
	assert.Equal(t, 95.0, verifiedAgent.GuaranteedSuccessRate)
	assert.Equal(t, 0.05, verifiedAgent.GuaranteePremiumRate)
	assert.False(t, unverifiedAgent.Verified)
}

func TestDiscoverServices_VerifiedOnlyFilter(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)

	addrA := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	addrB := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seedAgent(store, addrA, "Verified", []Service{{Type: "code", Name: "Audit", Price: "0.05"}})
	seedAgent(store, addrB, "Unverified", []Service{{Type: "code", Name: "Review", Price: "0.01"}})

	ver := &stubVerProvider{
		verified: map[string]bool{
			addrA: true,
			addrB: false,
		},
		guarantees: map[string]struct {
			successRate float64
			premiumRate float64
		}{
			addrA: {successRate: 95.0, premiumRate: 0.05},
		},
	}
	h.SetVerification(ver)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/services", h.DiscoverServices)
	req := httptest.NewRequest("GET", "/services?verifiedOnly=true", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Services []struct {
			AgentName string `json:"agentName"`
			Verified  bool   `json:"verified"`
		} `json:"services"`
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Count)
	assert.Equal(t, "Verified", body.Services[0].AgentName)
	assert.True(t, body.Services[0].Verified)
}

func TestDiscoverServices_IncludeInactive(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seedAgent(store, addr, "Agent1", []Service{
		{Type: "inference", Name: "GPT-4", Price: "0.01"},
	})

	// By default, services are active. Query with includeInactive=true.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/services?includeInactive=true", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestDiscoverServices_TypeFilter(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupRouter(h)

	seedAgent(store, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Agent1", []Service{
		{Type: "inference", Name: "GPT-4", Price: "0.01"},
	})
	seedAgent(store, "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "Agent2", []Service{
		{Type: "translation", Name: "Trans", Price: "0.005"},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/services?type=inference", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Services []ServiceListing `json:"services"`
		Count    int              `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Count)
}

// --- Helper function tests ---

func TestIsValidAddress(t *testing.T) {
	tests := []struct {
		addr  string
		valid bool
	}{
		{"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true},
		{"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", true},
		{"0x1234567890abcdef1234567890abcdef12345678", true},
		{"not-an-address", false},
		{"0x", false},
		{"0xshort", false},
		{"0xZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ", false},
		{"", false},
		{"0x123456789012345678901234567890123456789", false},   // too short
		{"0x12345678901234567890123456789012345678901", false}, // too long
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			assert.Equal(t, tt.valid, isValidAddress(tt.addr))
		})
	}
}

func TestTruncateAddress(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "0xaaaa...aaaa"},
		{"short", "short"},
		{"exactly10!", "exactly10!"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, truncateAddress(tt.input))
		})
	}
}

func TestTimeAgo(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		t        time.Time
		contains string
	}{
		{"just now", now.Add(-10 * time.Second), "just now"},
		{"1 minute", now.Add(-1 * time.Minute), "1 minute ago"},
		{"5 minutes", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"1 hour", now.Add(-1 * time.Hour), "1 hour ago"},
		{"3 hours", now.Add(-3 * time.Hour), "3 hours ago"},
		{"1 day", now.Add(-25 * time.Hour), "1 day ago"},
		{"5 days", now.Add(-5 * 24 * time.Hour), "5 days ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := timeAgo(tt.t)
			assert.Contains(t, result, tt.contains)
		})
	}
}

func TestParseIntQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		key      string
		def      int
		expected int
	}{
		{"empty uses default", "", "limit", 100, 100},
		{"valid value", "limit=50", "limit", 100, 50},
		{"invalid value uses default", "limit=abc", "limit", 100, 100},
		{"zero uses default", "limit=0", "limit", 100, 100},
		{"negative uses default", "limit=-5", "limit", 100, 100},
		{"capped at 1000", "limit=9999", "limit", 100, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			url := "/test"
			if tt.query != "" {
				url += "?" + tt.query
			}
			c.Request = httptest.NewRequest("GET", url, nil)
			result := parseIntQuery(c, tt.key, tt.def)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- RegisterRoutes test ---

func TestRegisterRoutes(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := gin.New()
	api := r.Group("/api/v1")
	h.RegisterRoutes(api)

	// Just verify the routes are registered by hitting them
	routes := r.Routes()
	paths := make(map[string]bool)
	for _, route := range routes {
		paths[route.Method+" "+route.Path] = true
	}

	assert.True(t, paths["POST /api/v1/agents"])
	assert.True(t, paths["GET /api/v1/agents"])
	assert.True(t, paths["GET /api/v1/agents/:address"])
	assert.True(t, paths["DELETE /api/v1/agents/:address"])
	assert.True(t, paths["POST /api/v1/agents/:address/services"])
	assert.True(t, paths["PUT /api/v1/agents/:address/services/:serviceId"])
	assert.True(t, paths["DELETE /api/v1/agents/:address/services/:serviceId"])
	assert.True(t, paths["GET /api/v1/services"])
	assert.True(t, paths["GET /api/v1/agents/:address/transactions"])
	assert.True(t, paths["GET /api/v1/network/stats"])
	assert.True(t, paths["GET /api/v1/feed"])
	assert.True(t, paths["POST /api/v1/transactions"])
}
