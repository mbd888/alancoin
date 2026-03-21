package intelligence

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// Handler coverage
// ---------------------------------------------------------------------------

func setupRouter(store Store) *gin.Engine {
	h := NewHandler(store)
	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	return r
}

func seedIntelStore() *MemoryStore {
	store := NewMemoryStore()
	ctx := context.Background()
	_ = store.SaveProfile(ctx, &AgentProfile{
		Address:        "0xalice",
		CreditScore:    85,
		RiskScore:      15,
		CompositeScore: 88,
		Tier:           TierDiamond,
		ComputeRunID:   "run_1",
		ComputedAt:     time.Now(),
		Credit:         CreditFactors{TraceRankInput: 90, ReputationInput: 80, TxSuccessRate: 0.95},
		Risk:           RiskFactors{AnomalyCount30d: 0, ForensicScore: 95},
	})
	_ = store.SaveBenchmarks(ctx, &NetworkBenchmarks{
		TotalAgents:    1,
		AvgCreditScore: 85,
		ComputedAt:     time.Now(),
	})
	_ = store.SaveScoreHistory(ctx, []*ScoreHistoryPoint{
		{Address: "0xalice", CreditScore: 80, RiskScore: 20, CompositeScore: 82, Tier: TierPlatinum, CreatedAt: time.Now().Add(-24 * time.Hour)},
		{Address: "0xalice", CreditScore: 85, RiskScore: 15, CompositeScore: 88, Tier: TierDiamond, CreatedAt: time.Now()},
	})
	return store
}

func TestHandler_GetProfile_Found(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/0xAlice", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["address"].(string) != "0xalice" {
		t.Errorf("address = %v", body["address"])
	}
}

func TestHandler_GetProfile_NotFound(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/0xunknown", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_GetCreditScore_Found(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/0xAlice/credit", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["creditScore"].(float64) != 85 {
		t.Errorf("creditScore = %v", body["creditScore"])
	}
}

func TestHandler_GetCreditScore_NotFound(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/0xnobody/credit", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_GetRiskScore_Found(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/0xAlice/risk", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetRiskScore_NotFound(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/0xnobody/risk", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_GetTrends(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/0xAlice/trends", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["count"].(float64) < 1 {
		t.Errorf("expected at least 1 trend point, got %v", body["count"])
	}
}

func TestHandler_GetTrends_WithParams(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	now := time.Now().UTC()
	from := now.Add(-48 * time.Hour).Format(time.RFC3339)
	to := now.Add(time.Hour).Format(time.RFC3339)
	url := "/v1/intelligence/0xAlice/trends?from=" + from + "&to=" + to + "&limit=5"

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", url, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetTrends_NoData(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/0xunknown/trends", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["count"].(float64) != 0 {
		t.Errorf("expected 0 points, got %v", body["count"])
	}
}

func TestHandler_GetTrends_MaxLimit(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/0xAlice/trends?limit=9999", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetBenchmarks(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/network/benchmarks", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetBenchmarks_None(t *testing.T) {
	store := NewMemoryStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/network/benchmarks", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetLeaderboard(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/network/leaderboard", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["count"].(float64) != 1 {
		t.Errorf("expected 1 agent, got %v", body["count"])
	}
}

func TestHandler_GetLeaderboard_WithLimit(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/network/leaderboard?limit=999", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetLeaderboard_Empty(t *testing.T) {
	store := NewMemoryStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/intelligence/network/leaderboard", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_BatchLookup_Success(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	body, _ := json.Marshal(batchRequest{Addresses: []string{"0xAlice", "0xMissing"}})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/intelligence/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["count"].(float64) != 1 {
		t.Errorf("expected 1 profile, got %v", resp["count"])
	}
}

func TestHandler_BatchLookup_InvalidBody(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/intelligence/batch", bytes.NewReader([]byte(`{invalid`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_BatchLookup_EmptyAddresses(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	body, _ := json.Marshal(batchRequest{Addresses: []string{}})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/intelligence/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_BatchLookup_TooManyAddresses(t *testing.T) {
	store := seedIntelStore()
	r := setupRouter(store)

	addrs := make([]string, 101)
	for i := range addrs {
		addrs[i] = "0x" + itoa(i)
	}
	body, _ := json.Marshal(batchRequest{Addresses: addrs})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/intelligence/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Worker coverage
// ---------------------------------------------------------------------------

func TestWorker_StartAndStop(t *testing.T) {
	engine, store := newTestEngine([]string{"0xtest"})
	worker := NewWorker(engine, store, 50*time.Millisecond, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	// Profile should be saved
	profile, _ := store.GetProfile(context.Background(), "0xtest")
	if profile == nil {
		t.Error("expected profile to be saved after worker run")
	}
}

func TestWorker_StopChannel(t *testing.T) {
	engine, store := newTestEngine(nil)
	worker := NewWorker(engine, store, time.Hour, nil)

	done := make(chan struct{})
	go func() {
		worker.Start(context.Background())
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	worker.Stop()
	<-done
}

type mockNotifier struct {
	tierTransitions int
	scoreAlerts     int
}

func (m *mockNotifier) EmitTierTransition(_, _, _ string, _, _ float64) {
	m.tierTransitions++
}

func (m *mockNotifier) EmitScoreAlert(_ string, _, _ float64, _ string) {
	m.scoreAlerts++
}

func TestWorker_WithNotifier(t *testing.T) {
	engine, store := newTestEngine([]string{"0xtest"})
	notifier := &mockNotifier{}
	worker := NewWorker(engine, store, 50*time.Millisecond, slog.Default()).WithNotifier(notifier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
}

func TestWorker_DetectTransitions(t *testing.T) {
	engine, store := newTestEngine([]string{"0xagent"})
	notifier := &mockNotifier{}
	worker := NewWorker(engine, store, time.Hour, slog.Default()).WithNotifier(notifier)

	// Save old profile with different tier
	_ = store.SaveProfile(context.Background(), &AgentProfile{
		Address:        "0xagent",
		CreditScore:    90,
		CompositeScore: 92,
		Tier:           TierDiamond,
	})

	// New profiles with lower tier
	newProfiles := []*AgentProfile{
		{Address: "0xagent", CreditScore: 40, CompositeScore: 45, Tier: TierSilver},
	}
	previousProfiles := map[string]*AgentProfile{
		"0xagent": {Address: "0xagent", CreditScore: 90, CompositeScore: 92, Tier: TierDiamond},
	}

	worker.detectTransitions(newProfiles, previousProfiles)

	if notifier.tierTransitions != 1 {
		t.Errorf("expected 1 tier transition, got %d", notifier.tierTransitions)
	}
	if notifier.scoreAlerts != 1 {
		t.Errorf("expected 1 score alert (drop > 10), got %d", notifier.scoreAlerts)
	}
}

// ---------------------------------------------------------------------------
// recordComputeMetrics coverage
// ---------------------------------------------------------------------------

func TestRecordComputeMetrics(t *testing.T) {
	// Should not panic
	recordComputeMetrics(10, 500*time.Millisecond, 55.0, 20.0)
}

// ---------------------------------------------------------------------------
// MemoryStore additional coverage
// ---------------------------------------------------------------------------

func TestMemoryStore_SaveProfileBatch(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	profiles := []*AgentProfile{
		{Address: "0xA", CreditScore: 50, Tier: TierGold},
		{Address: "0xB", CreditScore: 70, Tier: TierPlatinum},
	}
	err := store.SaveProfileBatch(ctx, profiles)
	if err != nil {
		t.Fatalf("SaveProfileBatch: %v", err)
	}

	p1, _ := store.GetProfile(ctx, "0xa")
	if p1 == nil || p1.CreditScore != 50 {
		t.Error("expected profile for 0xa")
	}
	p2, _ := store.GetProfile(ctx, "0xb")
	if p2 == nil || p2.CreditScore != 70 {
		t.Error("expected profile for 0xb")
	}
}

func TestMemoryStore_DeleteBenchmarksBefore(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	deleted, err := store.DeleteBenchmarksBefore(ctx, time.Now())
	if err != nil {
		t.Fatalf("DeleteBenchmarksBefore: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted (memory store only keeps latest), got %d", deleted)
	}
}

func TestMemoryStore_GetLatestBenchmarks_Nil(t *testing.T) {
	store := NewMemoryStore()
	b, err := store.GetLatestBenchmarks(context.Background())
	if err != nil {
		t.Fatalf("GetLatestBenchmarks: %v", err)
	}
	if b != nil {
		t.Error("expected nil when no benchmarks saved")
	}
}

// ---------------------------------------------------------------------------
// roundN coverage
// ---------------------------------------------------------------------------

func TestRoundN(t *testing.T) {
	if v := roundN(3.14159, 2); v != 3.14 {
		t.Errorf("roundN(3.14159, 2) = %f, want 3.14", v)
	}
	if v := roundN(0.12345, 3); v != 0.123 {
		t.Errorf("roundN(0.12345, 3) = %f, want 0.123", v)
	}
}

// ---------------------------------------------------------------------------
// computeBenchmarks edge cases
// ---------------------------------------------------------------------------

func TestComputeBenchmarks_Empty(t *testing.T) {
	engine, _ := newTestEngine(nil)
	b := engine.computeBenchmarks(nil, "run_empty", time.Now())
	if b.TotalAgents != 0 {
		t.Errorf("totalAgents = %d", b.TotalAgents)
	}
}
