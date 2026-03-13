package billing

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/mbd888/alancoin/internal/tenant"
)

// mockProvider records all calls to ReportUsage for assertions.
type mockProvider struct {
	mu       sync.Mutex
	calls    []usageCall
	failNext bool
}

type usageCall struct {
	customerID string
	requests   int64
	volume     int64
}

func (m *mockProvider) CreateCustomer(_ context.Context, _, _, _ string) (string, error) {
	return "cus_mock", nil
}
func (m *mockProvider) CreateSubscription(_ context.Context, _ string, _ tenant.Plan) (string, error) {
	return "sub_mock", nil
}
func (m *mockProvider) UpdateSubscription(_ context.Context, _ string, _ tenant.Plan) error {
	return nil
}
func (m *mockProvider) CancelSubscription(_ context.Context, _ string) error { return nil }
func (m *mockProvider) GetSubscription(_ context.Context, _ string) (*Subscription, error) {
	return &Subscription{Status: "active"}, nil
}

func (m *mockProvider) ReportUsage(_ context.Context, customerID string, requests, volume int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext {
		m.failNext = false
		return errors.New("billing unavailable")
	}
	m.calls = append(m.calls, usageCall{customerID, requests, volume})
	return nil
}

func (m *mockProvider) getCalls() []usageCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]usageCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func newTestMeter(p *mockProvider) *Meter {
	return NewMeter(p, slog.Default())
}

func TestMeter_RecordAndFlush(t *testing.T) {
	p := &mockProvider{}
	m := newTestMeter(p)

	m.RecordRequest("t1", "cus_1")
	m.RecordRequest("t1", "cus_1")
	m.RecordRequest("t1", "cus_1")
	m.RecordVolume("t1", "cus_1", 5_000_000)

	m.Flush(context.Background())

	calls := p.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 flush call, got %d", len(calls))
	}
	if calls[0].customerID != "cus_1" {
		t.Errorf("expected customer cus_1, got %s", calls[0].customerID)
	}
	if calls[0].requests != 3 {
		t.Errorf("expected 3 requests, got %d", calls[0].requests)
	}
	if calls[0].volume != 5_000_000 {
		t.Errorf("expected volume 5000000, got %d", calls[0].volume)
	}
}

func TestMeter_FlushResetsCounters(t *testing.T) {
	p := &mockProvider{}
	m := newTestMeter(p)

	m.RecordRequest("t1", "cus_1")
	m.Flush(context.Background())

	// Second flush should have nothing to report
	m.Flush(context.Background())

	calls := p.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 flush call (second should be skipped), got %d", len(calls))
	}
}

func TestMeter_MultipleTenants(t *testing.T) {
	p := &mockProvider{}
	m := newTestMeter(p)

	m.RecordRequest("t1", "cus_1")
	m.RecordRequest("t2", "cus_2")
	m.RecordVolume("t2", "cus_2", 1_000_000)

	m.Flush(context.Background())

	calls := p.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 flush calls, got %d", len(calls))
	}

	// Find each tenant's call
	byCustomer := make(map[string]usageCall)
	for _, c := range calls {
		byCustomer[c.customerID] = c
	}

	c1 := byCustomer["cus_1"]
	if c1.requests != 1 || c1.volume != 0 {
		t.Errorf("tenant1: expected 1 req / 0 vol, got %d / %d", c1.requests, c1.volume)
	}
	c2 := byCustomer["cus_2"]
	if c2.requests != 1 || c2.volume != 1_000_000 {
		t.Errorf("tenant2: expected 1 req / 1000000 vol, got %d / %d", c2.requests, c2.volume)
	}
}

func TestMeter_FlushRetryOnFailure(t *testing.T) {
	p := &mockProvider{failNext: true}
	m := newTestMeter(p)

	m.RecordRequest("t1", "cus_1")
	m.RecordRequest("t1", "cus_1")

	// First flush fails — counters should be restored
	m.Flush(context.Background())
	if len(p.getCalls()) != 0 {
		t.Fatal("expected no successful calls after failure")
	}

	// Second flush should retry with restored counters
	m.Flush(context.Background())
	calls := p.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 successful call, got %d", len(calls))
	}
	if calls[0].requests != 2 {
		t.Errorf("expected 2 retried requests, got %d", calls[0].requests)
	}
}

func TestMeter_IgnoresEmptyTenantOrCustomer(t *testing.T) {
	p := &mockProvider{}
	m := newTestMeter(p)

	m.RecordRequest("", "cus_1")
	m.RecordRequest("t1", "")
	m.RecordVolume("", "cus_1", 100)
	m.RecordVolume("t1", "", 100)

	m.Flush(context.Background())

	if len(p.getCalls()) != 0 {
		t.Error("expected no flush calls for empty tenant/customer")
	}
}

func TestMeter_IgnoresNonPositiveVolume(t *testing.T) {
	p := &mockProvider{}
	m := newTestMeter(p)

	m.RecordVolume("t1", "cus_1", 0)
	m.RecordVolume("t1", "cus_1", -100)

	m.Flush(context.Background())

	if len(p.getCalls()) != 0 {
		t.Error("expected no flush calls for zero/negative volume")
	}
}

func TestMeter_ConcurrentRecordAndFlush(t *testing.T) {
	p := &mockProvider{}
	m := newTestMeter(p)

	var wg sync.WaitGroup
	// 100 goroutines recording concurrently
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.RecordRequest("t1", "cus_1")
			m.RecordVolume("t1", "cus_1", 1000)
		}()
	}
	wg.Wait()

	m.Flush(context.Background())

	calls := p.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].requests != 100 {
		t.Errorf("expected 100 requests, got %d", calls[0].requests)
	}
	if calls[0].volume != 100_000 {
		t.Errorf("expected volume 100000, got %d", calls[0].volume)
	}
}

func TestNoopProvider_Satisfies(t *testing.T) {
	var _ Provider = (*NoopProvider)(nil)
}
