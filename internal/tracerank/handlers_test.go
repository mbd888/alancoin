package tracerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTRRouter(h *Handler) *gin.Engine {
	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	return r
}

func doTRReq(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, nil)
	r.ServeHTTP(w, req)
	return w
}

func parseTRBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	return body
}

func seedStore() *MemoryStore {
	store := NewMemoryStore()
	scores := []*AgentScore{
		{Address: "0xalice", GraphScore: 95.0, RawRank: 0.1, SeedSignal: 0.8, InDegree: 5, Iterations: 42, ComputedAt: time.Now()},
		{Address: "0xbob", GraphScore: 72.5, RawRank: 0.07, SeedSignal: 0.5, InDegree: 3, Iterations: 42, ComputedAt: time.Now()},
		{Address: "0xcharlie", GraphScore: 30.0, RawRank: 0.03, SeedSignal: 0.2, InDegree: 1, Iterations: 42, ComputedAt: time.Now()},
	}
	_ = store.SaveScores(context.Background(), scores, "run_001")
	return store
}

func TestGetScore_Found(t *testing.T) {
	store := seedStore()
	h := NewHandler(store)
	r := setupTRRouter(h)

	w := doTRReq(r, "GET", "/v1/tracerank/0xAlice")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseTRBody(t, w)
	if body["graphScore"].(float64) != 95.0 {
		t.Errorf("expected graphScore 95, got %v", body["graphScore"])
	}
	if body["address"].(string) != "0xalice" {
		t.Errorf("expected lowercase address, got %v", body["address"])
	}
}

func TestGetScore_NotFound(t *testing.T) {
	store := seedStore()
	h := NewHandler(store)
	r := setupTRRouter(h)

	w := doTRReq(r, "GET", "/v1/tracerank/0xunknown")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetScore_CaseInsensitive(t *testing.T) {
	store := seedStore()
	h := NewHandler(store)
	r := setupTRRouter(h)

	w := doTRReq(r, "GET", "/v1/tracerank/0xBOB")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseTRBody(t, w)
	if body["graphScore"].(float64) != 72.5 {
		t.Errorf("expected 72.5, got %v", body["graphScore"])
	}
}

func TestGetLeaderboard(t *testing.T) {
	store := seedStore()
	h := NewHandler(store)
	r := setupTRRouter(h)

	w := doTRReq(r, "GET", "/v1/tracerank/leaderboard")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseTRBody(t, w)
	if body["count"].(float64) != 3 {
		t.Errorf("expected 3 agents, got %v", body["count"])
	}
	agents := body["agents"].([]interface{})
	first := agents[0].(map[string]interface{})
	if first["graphScore"].(float64) != 95.0 {
		t.Errorf("expected top score 95, got %v", first["graphScore"])
	}
}

func TestGetLeaderboard_WithLimit(t *testing.T) {
	store := seedStore()
	h := NewHandler(store)
	r := setupTRRouter(h)

	w := doTRReq(r, "GET", "/v1/tracerank/leaderboard?limit=1")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseTRBody(t, w)
	if body["count"].(float64) != 1 {
		t.Errorf("expected 1 agent, got %v", body["count"])
	}
}

func TestGetLeaderboard_MaxLimit(t *testing.T) {
	store := seedStore()
	h := NewHandler(store)
	r := setupTRRouter(h)

	w := doTRReq(r, "GET", "/v1/tracerank/leaderboard?limit=999")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Should be capped at 200, but we only have 3 agents
	body := parseTRBody(t, w)
	if body["count"].(float64) != 3 {
		t.Errorf("expected 3, got %v", body["count"])
	}
}

func TestGetLeaderboard_Empty(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupTRRouter(h)

	w := doTRReq(r, "GET", "/v1/tracerank/leaderboard")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseTRBody(t, w)
	if body["count"].(float64) != 0 {
		t.Errorf("expected 0, got %v", body["count"])
	}
}

func TestGetRunHistory(t *testing.T) {
	store := seedStore()
	h := NewHandler(store)
	r := setupTRRouter(h)

	w := doTRReq(r, "GET", "/v1/tracerank/runs")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseTRBody(t, w)
	if body["count"].(float64) != 1 {
		t.Errorf("expected 1 run, got %v", body["count"])
	}
	runs := body["runs"].([]interface{})
	run := runs[0].(map[string]interface{})
	if run["runId"].(string) != "run_001" {
		t.Errorf("expected run_001, got %v", run["runId"])
	}
}

func TestGetRunHistory_Empty(t *testing.T) {
	store := NewMemoryStore()
	h := NewHandler(store)
	r := setupTRRouter(h)

	w := doTRReq(r, "GET", "/v1/tracerank/runs")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseTRBody(t, w)
	if body["count"].(float64) != 0 {
		t.Errorf("expected 0, got %v", body["count"])
	}
}
