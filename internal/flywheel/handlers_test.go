package flywheel

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

func setupHandlerRouter(h *Handler) *gin.Engine {
	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	return r
}

func doReq(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, nil)
	r.ServeHTTP(w, req)
	return w
}

func parseJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	return body
}

// preloadEngine creates an engine with pre-set state for handler tests.
func preloadEngine() *Engine {
	e := &Engine{
		history: make([]*State, 0),
	}
	state := &State{
		HealthScore:         65.3,
		HealthTier:          TierAccelerating,
		VelocityScore:       70,
		GrowthScore:         60,
		DensityScore:        55,
		EffectivenessScore:  80,
		RetentionScore:      45,
		TransactionsPerHour: 42,
		TotalAgents:         150,
		ComputedAt:          time.Now(),
	}
	e.latest = state
	e.history = append(e.history, state)
	return e
}

func TestGetHealth_WithData(t *testing.T) {
	engine := preloadEngine()
	h := NewHandler(engine, NewIncentiveEngine())
	r := setupHandlerRouter(h)

	w := doReq(r, "GET", "/v1/flywheel/health")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseJSON(t, w)
	if body["healthScore"].(float64) != 65.3 {
		t.Errorf("expected healthScore 65.3, got %v", body["healthScore"])
	}
	if body["healthTier"].(string) != TierAccelerating {
		t.Errorf("expected tier accelerating, got %v", body["healthTier"])
	}
	subScores, ok := body["subScores"].(map[string]interface{})
	if !ok {
		t.Fatal("expected subScores map")
	}
	if subScores["velocity"].(float64) != 70 {
		t.Errorf("expected velocity 70, got %v", subScores["velocity"])
	}
}

func TestGetHealth_NoData(t *testing.T) {
	engine := &Engine{history: make([]*State, 0)}
	h := NewHandler(engine, NewIncentiveEngine())
	r := setupHandlerRouter(h)

	w := doReq(r, "GET", "/v1/flywheel/health")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseJSON(t, w)
	if body["healthScore"].(float64) != 0 {
		t.Errorf("expected 0 for no data, got %v", body["healthScore"])
	}
	if body["healthTier"].(string) != TierCold {
		t.Errorf("expected cold tier, got %v", body["healthTier"])
	}
}

func TestGetState_WithData(t *testing.T) {
	engine := preloadEngine()
	h := NewHandler(engine, nil)
	r := setupHandlerRouter(h)

	w := doReq(r, "GET", "/v1/flywheel/state")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseJSON(t, w)
	state, ok := body["state"].(map[string]interface{})
	if !ok {
		t.Fatal("expected state object")
	}
	if state["healthScore"].(float64) != 65.3 {
		t.Errorf("expected 65.3, got %v", state["healthScore"])
	}
}

func TestGetState_NoData(t *testing.T) {
	engine := &Engine{history: make([]*State, 0)}
	h := NewHandler(engine, nil)
	r := setupHandlerRouter(h)

	w := doReq(r, "GET", "/v1/flywheel/state")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseJSON(t, w)
	if body["state"] != nil {
		t.Errorf("expected nil state, got %v", body["state"])
	}
}

func TestGetHistory_FromEngine(t *testing.T) {
	engine := preloadEngine()
	h := NewHandler(engine, nil)
	r := setupHandlerRouter(h)

	w := doReq(r, "GET", "/v1/flywheel/history")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseJSON(t, w)
	if body["count"].(float64) != 1 {
		t.Errorf("expected 1 history point, got %v", body["count"])
	}
	history := body["history"].([]interface{})
	point := history[0].(map[string]interface{})
	if point["healthScore"].(float64) != 65.3 {
		t.Errorf("expected 65.3, got %v", point["healthScore"])
	}
}

func TestGetHistory_FromStore(t *testing.T) {
	engine := &Engine{history: make([]*State, 0)} // empty engine
	store := NewMemoryStore()
	_ = store.Save(context.Background(), &State{
		HealthScore: 50,
		HealthTier:  TierSpinning,
		ComputedAt:  time.Now(),
	})
	_ = store.Save(context.Background(), &State{
		HealthScore: 60,
		HealthTier:  TierAccelerating,
		ComputedAt:  time.Now(),
	})

	h := NewHandler(engine, nil).WithStore(store)
	r := setupHandlerRouter(h)

	w := doReq(r, "GET", "/v1/flywheel/history")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseJSON(t, w)
	if body["count"].(float64) != 2 {
		t.Errorf("expected 2 history points, got %v", body["count"])
	}
}

func TestGetIncentives_Configured(t *testing.T) {
	engine := preloadEngine()
	h := NewHandler(engine, NewIncentiveEngine())
	r := setupHandlerRouter(h)

	w := doReq(r, "GET", "/v1/flywheel/incentives")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseJSON(t, w)
	schedule, ok := body["schedule"].([]interface{})
	if !ok {
		t.Fatal("expected schedule array")
	}
	if len(schedule) != 5 {
		t.Errorf("expected 5 tiers, got %d", len(schedule))
	}
}

func TestGetIncentives_NotConfigured(t *testing.T) {
	engine := preloadEngine()
	h := NewHandler(engine, nil)
	r := setupHandlerRouter(h)

	w := doReq(r, "GET", "/v1/flywheel/incentives")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseJSON(t, w)
	if _, ok := body["message"]; !ok {
		t.Error("expected message when not configured")
	}
}
