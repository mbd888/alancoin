package forensics

import (
	"context"
	"sync"
	"testing"
	"time"
)

type capturingSink struct {
	mu     sync.Mutex
	alerts []*Alert
}

func (c *capturingSink) RecordForensicsAlert(_ context.Context, a *Alert) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *a
	c.alerts = append(c.alerts, &cp)
}

func (c *capturingSink) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.alerts)
}

func TestIncidentSink_ReceivesAlerts(t *testing.T) {
	svc := newTestService()
	sink := &capturingSink{}
	svc.WithIncidentSink(sink)

	ctx := context.Background()
	buildBaseline(t, svc, "0xAgent1", 20)

	// Anomalous transaction should fire an alert and route it to the sink.
	_, err := svc.Ingest(ctx, SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xVendorA",
		Amount:       500.0,
		ServiceType:  "inference",
		Timestamp:    time.Now(),
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if sink.count() == 0 {
		t.Fatal("sink received zero alerts; expected at least one from the anomaly")
	}
}

func TestIncidentSink_NilSafe(t *testing.T) {
	svc := newTestService().WithIncidentSink(nil)

	ctx := context.Background()
	buildBaseline(t, svc, "0xAgent1", 20)
	// Must not panic with nil sink.
	_, err := svc.Ingest(ctx, SpendEvent{
		AgentAddr:    "0xAgent1",
		Counterparty: "0xVendorA",
		Amount:       500.0,
		ServiceType:  "inference",
		Timestamp:    time.Now(),
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
}
