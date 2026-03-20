package forensics

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func newTestService() *Service {
	return NewService(NewMemoryStore(), DefaultConfig(), slog.Default())
}

// buildBaseline ingests N normal events to establish a baseline.
func buildBaseline(t *testing.T, svc *Service, agent string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		_, err := svc.Ingest(ctx, SpendEvent{
			AgentAddr:    agent,
			Counterparty: "0xVendorA",
			Amount:       10.0 + float64(i%5), // 10-14 range
			ServiceType:  "inference",
			Timestamp:    time.Now().Add(-time.Duration(n-i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("buildBaseline ingest %d: %v", i, err)
		}
	}
}

func TestBaselineBuildsWithoutAlerts(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	// First 10 events should not produce alerts (building baseline)
	for i := 0; i < 10; i++ {
		alerts, err := svc.Ingest(ctx, SpendEvent{
			AgentAddr:    "0xAgent1",
			Counterparty: "0xVendor",
			Amount:       10.0,
			ServiceType:  "inference",
			Timestamp:    time.Now(),
		})
		if err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
		if len(alerts) > 0 {
			t.Errorf("ingest %d: got %d alerts during baseline building, want 0", i, len(alerts))
		}
	}

	// Verify baseline exists
	baseline, err := svc.GetBaseline(ctx, "0xAgent1")
	if err != nil {
		t.Fatalf("GetBaseline: %v", err)
	}
	if baseline.TxCount != 10 {
		t.Errorf("txCount = %d, want 10", baseline.TxCount)
	}
	if baseline.MeanAmount < 9 || baseline.MeanAmount > 11 {
		t.Errorf("meanAmount = %f, want ~10", baseline.MeanAmount)
	}
}

func TestAmountAnomaly(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	buildBaseline(t, svc, "0xAgent1", 20)

	// Send a transaction 10x the normal amount
	alerts, err := svc.Ingest(ctx, SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xVendorA",
		Amount:       500.0, // Way above ~12 baseline
		ServiceType:  "inference",
		Timestamp:    time.Now(),
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	found := false
	for _, a := range alerts {
		if a.Type == AlertAmountAnomaly {
			found = true
			if a.Sigma < 3 {
				t.Errorf("sigma = %f, expected > 3", a.Sigma)
			}
		}
	}
	if !found {
		t.Error("expected AlertAmountAnomaly, got none")
	}
}

func TestNewCounterpartyAlert(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	// Build baseline with only one counterparty (concentrated)
	for i := 0; i < 15; i++ {
		svc.Ingest(ctx, SpendEvent{
			AgentAddr:    "0xAgent1",
			Counterparty: "0xVendorA",
			Amount:       10.0,
			ServiceType:  "inference",
			Timestamp:    time.Now(),
		})
	}

	// New counterparty should trigger alert
	alerts, _ := svc.Ingest(ctx, SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xSuspiciousNew",
		Amount:       10.0,
		ServiceType:  "inference",
		Timestamp:    time.Now(),
	})

	found := false
	for _, a := range alerts {
		if a.Type == AlertNewCounterparty {
			found = true
		}
	}
	if !found {
		t.Error("expected AlertNewCounterparty for concentrated agent")
	}
}

func TestServiceDeviationAlert(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	buildBaseline(t, svc, "0xAgent1", 15)

	// Use a completely new service type
	alerts, _ := svc.Ingest(ctx, SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xVendorA",
		Amount:       10.0,
		ServiceType:  "crypto_mining", // never seen before
		Timestamp:    time.Now(),
	})

	found := false
	for _, a := range alerts {
		if a.Type == AlertServiceDeviation {
			found = true
		}
	}
	if !found {
		t.Error("expected AlertServiceDeviation")
	}
}

func TestNormalTransactionNoAlerts(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	buildBaseline(t, svc, "0xAgent1", 20)

	// Normal transaction — same counterparty, same service, normal amount
	alerts, _ := svc.Ingest(ctx, SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xVendorA",
		Amount:       12.0, // within normal range
		ServiceType:  "inference",
		Timestamp:    time.Now(),
	})

	if len(alerts) > 0 {
		t.Errorf("expected 0 alerts for normal tx, got %d: %+v", len(alerts), alerts)
	}
}

func TestAcknowledgeAlert(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	buildBaseline(t, svc, "0xAgent1", 15)

	// Trigger an alert
	alerts, _ := svc.Ingest(ctx, SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xVendorA",
		Amount:       1000.0,
		ServiceType:  "inference",
		Timestamp:    time.Now(),
	})

	if len(alerts) == 0 {
		t.Fatal("expected at least one alert")
	}

	err := svc.AcknowledgeAlert(ctx, alerts[0].ID)
	if err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	// Verify
	allAlerts, _ := svc.ListAlerts(ctx, "0xAgent1", 100)
	for _, a := range allAlerts {
		if a.ID == alerts[0].ID && !a.Acknowledged {
			t.Error("alert should be acknowledged")
		}
	}
}

func TestBaselineTracksCounterparties(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	vendors := []string{"0xA", "0xB", "0xC", "0xD"}
	for i := 0; i < 20; i++ {
		svc.Ingest(ctx, SpendEvent{
			AgentAddr:    "0xAgent1",
			Counterparty: vendors[i%len(vendors)],
			Amount:       10.0,
			ServiceType:  "inference",
			Timestamp:    time.Now(),
		})
	}

	b, _ := svc.GetBaseline(ctx, "0xAgent1")
	if len(b.KnownCounterparties) != 4 {
		t.Errorf("counterparties = %d, want 4", len(b.KnownCounterparties))
	}
}

func TestSeverityFromScore(t *testing.T) {
	tests := []struct {
		score float64
		want  AlertSeverity
	}{
		{10, SeverityInfo},
		{49, SeverityInfo},
		{50, SeverityWarning},
		{79, SeverityWarning},
		{80, SeverityCritical},
		{100, SeverityCritical},
	}
	for _, tt := range tests {
		got := severityFromScore(tt.score)
		if got != tt.want {
			t.Errorf("severityFromScore(%f) = %q, want %q", tt.score, got, tt.want)
		}
	}
}
