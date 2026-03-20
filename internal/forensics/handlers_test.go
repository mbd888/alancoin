package forensics

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

func setupForensicsRouter() (*gin.Engine, *Service) {
	svc := NewService(NewMemoryStore(), DefaultConfig(), slog.Default())

	r := gin.New()
	h := NewHandler(svc)

	authed := r.Group("/v1", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xCaller")
		c.Set("authTenantID", "ten_1")
		c.Next()
	})
	h.RegisterRoutes(authed)
	h.RegisterProtectedRoutes(authed)

	return r, svc
}

func TestHandlerIngestEvent(t *testing.T) {
	r, _ := setupForensicsRouter()

	body, _ := json.Marshal(SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xVendor",
		Amount:       10.0,
		ServiceType:  "inference",
		Timestamp:    time.Now(),
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/forensics/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		AlertCount int `json:"alertCount"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	// First event — no alerts (building baseline)
	if resp.AlertCount != 0 {
		t.Errorf("alertCount = %d, want 0 (baseline building)", resp.AlertCount)
	}
}

func TestHandlerIngestAnomalousEvent(t *testing.T) {
	r, svc := setupForensicsRouter()

	// Build baseline with varied amounts for nonzero stddev
	for i := 0; i < 15; i++ {
		svc.Ingest(context.Background(), SpendEvent{
			AgentAddr:    "0xAgent1",
			Counterparty: "0xVendor",
			Amount:       10.0 + float64(i%5),
			ServiceType:  "inference",
			Timestamp:    time.Now().Add(-time.Duration(15-i) * time.Minute),
		})
	}

	// Send anomalous event via HTTP handler
	body, _ := json.Marshal(SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xVendor",
		Amount:       500.0,
		ServiceType:  "inference",
		Timestamp:    time.Now(),
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/forensics/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		AlertCount int      `json:"alertCount"`
		Alerts     []*Alert `json:"alerts"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.AlertCount == 0 {
		t.Error("expected at least one alert for anomalous amount")
	}
}

func TestHandlerGetBaseline(t *testing.T) {
	r, svc := setupForensicsRouter()

	// Ingest some events to create baseline
	for i := 0; i < 5; i++ {
		svc.Ingest(context.Background(), SpendEvent{
			AgentAddr:    "0xAgent1",
			Counterparty: "0xVendor",
			Amount:       10.0,
			ServiceType:  "inference",
			Timestamp:    time.Now(),
		})
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/forensics/agents/0xAgent1/baseline", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Baseline Baseline `json:"baseline"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Baseline.TxCount != 5 {
		t.Errorf("txCount = %d, want 5", resp.Baseline.TxCount)
	}
}

func TestHandlerGetBaselineNotFound(t *testing.T) {
	r, _ := setupForensicsRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/forensics/agents/0xUnknown/baseline", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandlerListAlerts(t *testing.T) {
	r, svc := setupForensicsRouter()

	// Build baseline then trigger alert
	for i := 0; i < 15; i++ {
		svc.Ingest(context.Background(), SpendEvent{
			AgentAddr: "0xAgent1", Counterparty: "0xV", Amount: 10.0 + float64(i%5),
			ServiceType: "inference", Timestamp: time.Now().Add(-time.Duration(15-i) * time.Minute),
		})
	}
	svc.Ingest(context.Background(), SpendEvent{
		AgentAddr: "0xAgent1", Counterparty: "0xV", Amount: 1000.0,
		ServiceType: "inference", Timestamp: time.Now(),
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/forensics/agents/0xAgent1/alerts?limit=10", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Alerts []*Alert `json:"alerts"`
		Count  int      `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count == 0 {
		t.Error("expected alerts")
	}
}

func TestHandlerAcknowledgeAlert(t *testing.T) {
	r, svc := setupForensicsRouter()

	// Build baseline + trigger
	for i := 0; i < 15; i++ {
		svc.Ingest(context.Background(), SpendEvent{
			AgentAddr: "0xAgent1", Counterparty: "0xV", Amount: 10.0 + float64(i%5),
			ServiceType: "inference", Timestamp: time.Now().Add(-time.Duration(15-i) * time.Minute),
		})
	}
	alerts, _ := svc.Ingest(context.Background(), SpendEvent{
		AgentAddr: "0xAgent1", Counterparty: "0xV", Amount: 1000.0,
		ServiceType: "inference", Timestamp: time.Now(),
	})

	if len(alerts) == 0 {
		t.Fatal("need at least one alert")
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/forensics/alerts/"+alerts[0].ID+"/acknowledge", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerListAllAlerts(t *testing.T) {
	r, _ := setupForensicsRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/forensics/alerts?severity=critical&limit=50", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
