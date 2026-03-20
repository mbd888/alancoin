package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newForensicsServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/forensics/agents/0xAgent1/baseline", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"baseline": map[string]any{
				"agentAddr":    "0xAgent1",
				"txCount":      50,
				"meanAmount":   12.5,
				"stdDevAmount": 3.2,
			},
		})
	})

	mux.HandleFunc("GET /v1/forensics/agents/0xAgent1/alerts", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"alerts": []map[string]any{
				{
					"id":       "alrt_1",
					"severity": "warning",
					"type":     "amount_anomaly",
					"message":  "Transaction above baseline",
					"score":    65.0,
				},
			},
			"count": 1,
		})
	})

	mux.HandleFunc("POST /v1/forensics/alerts/alrt_1/acknowledge", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "acknowledged"})
	})

	return httptest.NewServer(mux)
}

func TestForensicsGetBaseline(t *testing.T) {
	srv := newForensicsServer(t)
	defer srv.Close()
	c := NewClient(srv.URL, WithAPIKey("test"))

	baseline, err := c.ForensicsGetBaseline(context.Background(), "0xAgent1")
	if err != nil {
		t.Fatalf("GetBaseline: %v", err)
	}
	if baseline.TxCount != 50 {
		t.Errorf("txCount = %d, want 50", baseline.TxCount)
	}
	if baseline.MeanAmount != 12.5 {
		t.Errorf("meanAmount = %f, want 12.5", baseline.MeanAmount)
	}
}

func TestForensicsListAlerts(t *testing.T) {
	srv := newForensicsServer(t)
	defer srv.Close()
	c := NewClient(srv.URL, WithAPIKey("test"))

	alerts, err := c.ForensicsListAlerts(context.Background(), "0xAgent1", 10)
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("len = %d, want 1", len(alerts))
	}
	if alerts[0].Severity != "warning" {
		t.Errorf("severity = %q", alerts[0].Severity)
	}
}

func TestForensicsAcknowledgeAlert(t *testing.T) {
	srv := newForensicsServer(t)
	defer srv.Close()
	c := NewClient(srv.URL, WithAPIKey("test"))

	if err := c.ForensicsAcknowledgeAlert(context.Background(), "alrt_1"); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
}
