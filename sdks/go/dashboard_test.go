package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newDashboardServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/tenants/ten_1/dashboard/overview", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DashboardOverview{
			Tenant: map[string]any{"id": "ten_1", "name": "Test Corp"},
			Billing: TenantBilling{
				TotalRequests: 1500,
				SettledVolume: "450.25",
			},
			ActiveSessions: 3,
			AgentCount:     5,
		})
	})

	mux.HandleFunc("GET /v1/tenants/ten_1/dashboard/usage", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DashboardUsage{
			Interval: "day",
			From:     now.Add(-24 * time.Hour),
			To:       now,
			Points: []UsagePoint{
				{Timestamp: now.Add(-12 * time.Hour), Requests: 100, Volume: "50.00"},
				{Timestamp: now, Requests: 200, Volume: "100.00"},
			},
			Count: 2,
		})
	})

	mux.HandleFunc("GET /v1/tenants/ten_1/dashboard/top-services", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"services": []TopService{
				{ServiceID: "svc_1", ServiceName: "GPT-4", ServiceType: "inference", Requests: 500, Volume: "250.00"},
				{ServiceID: "svc_2", ServiceName: "Embed-v3", ServiceType: "embedding", Requests: 300, Volume: "30.00"},
			},
			"count": 2,
		})
	})

	mux.HandleFunc("GET /v1/tenants/ten_1/dashboard/denials", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"denials": []DashboardDenial{
				{SessionID: "sess_1", ServiceType: "inference", PolicyName: "daily-limit", Reason: "exceeded daily spend", CreatedAt: now},
			},
			"count": 1,
		})
	})

	mux.HandleFunc("GET /v1/tenants/ten_1/dashboard/sessions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"sessions": []GatewaySessionInfo{
				{ID: "sess_1", Status: "active", TotalSpent: "25.00"},
				{ID: "sess_2", Status: "closed", TotalSpent: "50.00"},
			},
			"count": 2,
		})
	})

	return httptest.NewServer(mux)
}

func TestDashboardOverview(t *testing.T) {
	srv := newDashboardServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	overview, err := c.DashboardOverviewGet(context.Background(), "ten_1")
	if err != nil {
		t.Fatal(err)
	}
	if overview.ActiveSessions != 3 {
		t.Errorf("ActiveSessions = %d", overview.ActiveSessions)
	}
	if overview.AgentCount != 5 {
		t.Errorf("AgentCount = %d", overview.AgentCount)
	}
}

func TestDashboardUsage(t *testing.T) {
	srv := newDashboardServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	usage, err := c.DashboardUsageGet(context.Background(), "ten_1", DashboardUsageOptions{
		Interval: "day",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.Points) != 2 {
		t.Errorf("Points = %d", len(usage.Points))
	}
	if usage.Interval != "day" {
		t.Errorf("Interval = %q", usage.Interval)
	}
}

func TestDashboardTopServices(t *testing.T) {
	srv := newDashboardServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	services, err := c.DashboardTopServices(context.Background(), "ten_1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Errorf("len = %d", len(services))
	}
	if services[0].ServiceName != "GPT-4" {
		t.Errorf("ServiceName = %q", services[0].ServiceName)
	}
}

func TestDashboardDenials(t *testing.T) {
	srv := newDashboardServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	denials, err := c.DashboardDenials(context.Background(), "ten_1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(denials) != 1 {
		t.Errorf("len = %d", len(denials))
	}
	if denials[0].PolicyName != "daily-limit" {
		t.Errorf("PolicyName = %q", denials[0].PolicyName)
	}
}

func TestDashboardSessions(t *testing.T) {
	srv := newDashboardServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	sessions, err := c.DashboardSessions(context.Background(), "ten_1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Errorf("len = %d", len(sessions))
	}
}
