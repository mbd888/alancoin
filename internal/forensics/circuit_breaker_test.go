package forensics

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type mockPauser struct {
	mu     sync.Mutex
	paused []string
}

func (m *mockPauser) PauseAgent(_ context.Context, addr, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = append(m.paused, addr)
	return nil
}

type mockWebhookNotifier struct {
	mu     sync.Mutex
	alerts []string
}

func (m *mockWebhookNotifier) EmitForensicsCriticalAlert(agentAddr, alertID, _ string, _ float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, agentAddr+":"+alertID)
}

func TestCriticalAlertPausesAgent(t *testing.T) {
	pauser := &mockPauser{}
	webhooks := &mockWebhookNotifier{}
	svc := NewService(NewMemoryStore(), DefaultConfig(), slog.Default())
	svc.WithAgentPauser(pauser)
	svc.WithWebhooks(webhooks)

	ctx := context.Background()

	// Build baseline with varied amounts
	for i := 0; i < 20; i++ {
		svc.Ingest(ctx, SpendEvent{
			AgentAddr:    "0xAgent1",
			Counterparty: "0xVendor",
			Amount:       10.0 + float64(i%5),
			ServiceType:  "inference",
			Timestamp:    time.Now().Add(-time.Duration(20-i) * time.Minute),
		})
	}

	// Send a burst of events to trigger critical alert (burst pattern)
	cfg := svc.config
	for i := 0; i < cfg.BurstThreshold+5; i++ {
		svc.Ingest(ctx, SpendEvent{
			AgentAddr:    "0xAgent1",
			Counterparty: "0xVendor",
			Amount:       10.0,
			ServiceType:  "inference",
			Timestamp:    time.Now(),
		})
	}

	pauser.mu.Lock()
	pauseCount := len(pauser.paused)
	pauser.mu.Unlock()

	if pauseCount == 0 {
		t.Error("expected agent to be auto-paused after critical burst alert")
	}

	webhooks.mu.Lock()
	webhookCount := len(webhooks.alerts)
	webhooks.mu.Unlock()

	if webhookCount == 0 {
		t.Error("expected webhook notification for critical alert")
	}
}

func TestNonCriticalAlertDoesNotPause(t *testing.T) {
	pauser := &mockPauser{}
	svc := NewService(NewMemoryStore(), DefaultConfig(), slog.Default())
	svc.WithAgentPauser(pauser)

	ctx := context.Background()

	// Build baseline
	for i := 0; i < 15; i++ {
		svc.Ingest(ctx, SpendEvent{
			AgentAddr:    "0xAgent1",
			Counterparty: "0xVendor",
			Amount:       10.0 + float64(i%5),
			ServiceType:  "inference",
			Timestamp:    time.Now().Add(-time.Duration(15-i) * time.Minute),
		})
	}

	// Send mildly anomalous event — new service type triggers info-level alert only
	svc.Ingest(ctx, SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xVendor",
		Amount:       12.0,          // Within normal range
		ServiceType:  "translation", // New service type → info alert
		Timestamp:    time.Now(),
	})

	pauser.mu.Lock()
	pauseCount := len(pauser.paused)
	pauser.mu.Unlock()

	if pauseCount != 0 {
		t.Errorf("expected no pause for non-critical alert, got %d pauses", pauseCount)
	}
}
