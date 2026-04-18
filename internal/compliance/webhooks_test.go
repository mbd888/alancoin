package compliance

import (
	"context"
	"sync"
	"testing"
)

type spyWebhooks struct {
	mu    sync.Mutex
	calls []spyCall
}

type spyCall struct {
	AgentAddr, IncidentID, Source, Kind, Title, Scope string
}

func (s *spyWebhooks) EmitComplianceIncidentCritical(agentAddr, incidentID, source, kind, title, scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, spyCall{agentAddr, incidentID, source, kind, title, scope})
}

func (s *spyWebhooks) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func TestWebhooks_FireOnCriticalOnly(t *testing.T) {
	spy := &spyWebhooks{}
	svc, _ := newTestService(nil)
	svc.WithWebhooks(spy)

	ctx := context.Background()
	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityInfo, AgentAddr: "0xa", Title: "info"})
	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityWarning, AgentAddr: "0xb", Title: "warn"})
	if spy.count() != 0 {
		t.Errorf("webhook fired on non-critical; calls=%d", spy.count())
	}

	_, _ = svc.RecordFromAlert(ctx, IncidentInput{Scope: "tenant_a", Source: "forensics", Kind: "velocity_spike", Severity: SeverityCritical, AgentAddr: "0xc", Title: "burn"})
	if spy.count() != 1 {
		t.Fatalf("expected 1 webhook call for critical, got %d", spy.count())
	}
	got := spy.calls[0]
	if got.Scope != "tenant_a" || got.Source != "forensics" || got.Kind != "velocity_spike" {
		t.Errorf("webhook payload wrong: %+v", got)
	}
}

func TestWebhooks_NilNotifierIsSafe(t *testing.T) {
	svc, _ := newTestService(nil)
	svc.WithWebhooks(nil) // explicit nil

	_, err := svc.RecordFromAlert(context.Background(), IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityCritical, AgentAddr: "0xa", Title: "boom"})
	if err != nil {
		t.Errorf("RecordFromAlert with nil webhooks returned err=%v", err)
	}
}

func TestWebhooks_DetachViaWithWebhooksNil(t *testing.T) {
	spy := &spyWebhooks{}
	svc, _ := newTestService(nil)
	svc.WithWebhooks(spy)
	svc.WithWebhooks(nil) // detach

	_, _ = svc.RecordFromAlert(context.Background(), IncidentInput{Scope: "tenant_a", Source: "forensics", Severity: SeverityCritical, AgentAddr: "0xa", Title: "boom"})
	if spy.count() != 0 {
		t.Errorf("webhook still fires after detach; count=%d", spy.count())
	}
}
