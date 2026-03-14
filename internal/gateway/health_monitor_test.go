package gateway

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestHealthMonitor_InitialStateUnknown(t *testing.T) {
	hm := NewHealthMonitor(DefaultHealthMonitorConfig())
	status := hm.GetHealth("provider-1")
	if status.State != HealthUnknown {
		t.Errorf("expected HealthUnknown, got %s", status.State.String())
	}
}

func TestHealthMonitor_HealthyAfterSuccesses(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 3
	hm := NewHealthMonitor(config)

	for i := 0; i < 5; i++ {
		hm.RecordSuccess("provider-1", 100*time.Millisecond)
	}

	status := hm.GetHealth("provider-1")
	if status.State != HealthHealthy {
		t.Errorf("expected HealthHealthy, got %s", status.State.String())
	}
	if status.SuccessRate != 1.0 {
		t.Errorf("expected success rate 1.0, got %f", status.SuccessRate)
	}
	if status.ErrorRate != 0.0 {
		t.Errorf("expected error rate 0.0, got %f", status.ErrorRate)
	}
	if status.TotalCalls != 5 {
		t.Errorf("expected 5 total calls, got %d", status.TotalCalls)
	}
}

func TestHealthMonitor_DegradedOnHighErrorRate(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 5
	config.DegradedErrorRate = 0.1
	hm := NewHealthMonitor(config)

	// 8 successes, 2 failures = 20% error rate > 10% threshold
	for i := 0; i < 8; i++ {
		hm.RecordSuccess("provider-1", 50*time.Millisecond)
	}
	for i := 0; i < 2; i++ {
		hm.RecordFailure("provider-1", errors.New("timeout"))
	}

	status := hm.GetHealth("provider-1")
	if status.State != HealthDegraded {
		t.Errorf("expected HealthDegraded, got %s (error rate: %f)", status.State.String(), status.ErrorRate)
	}
}

func TestHealthMonitor_UnhealthyOnCriticalErrorRate(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 5
	config.UnhealthyErrorRate = 0.5
	hm := NewHealthMonitor(config)

	// 3 successes, 7 failures = 70% error rate > 50% threshold
	for i := 0; i < 3; i++ {
		hm.RecordSuccess("provider-1", 50*time.Millisecond)
	}
	for i := 0; i < 7; i++ {
		hm.RecordFailure("provider-1", errors.New("server error"))
	}

	status := hm.GetHealth("provider-1")
	if status.State != HealthUnhealthy {
		t.Errorf("expected HealthUnhealthy, got %s (error rate: %f)", status.State.String(), status.ErrorRate)
	}
}

func TestHealthMonitor_DegradedOnHighLatency(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 5
	config.DegradedP95Latency = 2 * time.Second
	config.DegradedErrorRate = 0.5 // Set high so error rate doesn't trigger degraded
	hm := NewHealthMonitor(config)

	// All successes but with very high latency.
	for i := 0; i < 10; i++ {
		hm.RecordSuccess("provider-1", 3*time.Second)
	}

	status := hm.GetHealth("provider-1")
	if status.State != HealthDegraded {
		t.Errorf("expected HealthDegraded due to high P95 latency, got %s (P95: %v)", status.State.String(), status.P95Latency)
	}
	if status.P95Latency <= 2*time.Second {
		t.Errorf("expected P95 > 2s, got %v", status.P95Latency)
	}
}

func TestHealthMonitor_SlidingWindow(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.Window = 100 * time.Millisecond
	config.MinSamplesForDecision = 3
	hm := NewHealthMonitor(config)

	now := time.Now()
	hm.nowFunc = func() time.Time { return now }

	// Record 5 failures.
	for i := 0; i < 5; i++ {
		hm.RecordFailure("provider-1", errors.New("fail"))
	}

	status := hm.GetHealth("provider-1")
	if status.State != HealthUnhealthy {
		t.Errorf("expected HealthUnhealthy, got %s", status.State.String())
	}

	// Advance time past the window.
	now = now.Add(200 * time.Millisecond)

	// Record successes — the old failures should be expired.
	for i := 0; i < 5; i++ {
		hm.RecordSuccess("provider-1", 10*time.Millisecond)
	}

	status = hm.GetHealth("provider-1")
	if status.State != HealthHealthy {
		t.Errorf("expected HealthHealthy after window expiry, got %s", status.State.String())
	}
}

func TestHealthMonitor_StateTransitionCallback(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 3
	config.UnhealthyErrorRate = 0.5
	hm := NewHealthMonitor(config)

	var mu sync.Mutex
	var transitions []struct{ from, to HealthState }

	hm.OnStateChange(func(providerID string, from, to HealthState) {
		mu.Lock()
		transitions = append(transitions, struct{ from, to HealthState }{from, to})
		mu.Unlock()
	})

	// Start with successes (Unknown → Healthy).
	for i := 0; i < 3; i++ {
		hm.RecordSuccess("provider-1", 10*time.Millisecond)
	}

	// Now add many failures to trigger Healthy → Unhealthy.
	for i := 0; i < 10; i++ {
		hm.RecordFailure("provider-1", errors.New("fail"))
	}

	// Give the async callback time to fire.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(transitions) < 1 {
		t.Fatalf("expected at least 1 state transition, got %d", len(transitions))
	}

	// Should have transitioned from Unknown to Healthy, then to something worse.
	foundHealthyTransition := false
	for _, tr := range transitions {
		if tr.to == HealthHealthy {
			foundHealthyTransition = true
		}
	}
	if !foundHealthyTransition {
		t.Errorf("expected a transition to Healthy state, transitions: %+v", transitions)
	}
}

func TestHealthMonitor_ConcurrentAccess(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 1
	hm := NewHealthMonitor(config)

	var wg sync.WaitGroup
	providers := []string{"p1", "p2", "p3", "p4", "p5"}

	// 50 goroutines recording successes and failures concurrently.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pid := providers[idx%len(providers)]
			for j := 0; j < 100; j++ {
				if j%3 == 0 {
					hm.RecordFailure(pid, errors.New("fail"))
				} else {
					hm.RecordSuccess(pid, time.Duration(j)*time.Millisecond)
				}
			}
		}(i)
	}

	// Also read concurrently.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				for _, pid := range providers {
					_ = hm.GetHealth(pid)
				}
			}
		}()
	}

	wg.Wait()

	// Verify all providers have data.
	all := hm.GetAllHealth()
	if len(all) != len(providers) {
		t.Errorf("expected %d providers, got %d", len(providers), len(all))
	}
}

func TestHealthMonitor_LatencyPercentiles(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 1
	hm := NewHealthMonitor(config)

	// Record latencies: 10, 20, 30, ..., 100ms
	for i := 1; i <= 10; i++ {
		hm.RecordSuccess("provider-1", time.Duration(i*10)*time.Millisecond)
	}

	status := hm.GetHealth("provider-1")

	// P50 should be around 50ms.
	if status.P50Latency < 40*time.Millisecond || status.P50Latency > 60*time.Millisecond {
		t.Errorf("expected P50 around 50ms, got %v", status.P50Latency)
	}

	// P95 should be around 95-100ms.
	if status.P95Latency < 90*time.Millisecond || status.P95Latency > 110*time.Millisecond {
		t.Errorf("expected P95 around 95-100ms, got %v", status.P95Latency)
	}

	// P99 should be around 100ms.
	if status.P99Latency < 90*time.Millisecond || status.P99Latency > 110*time.Millisecond {
		t.Errorf("expected P99 around 100ms, got %v", status.P99Latency)
	}
}

func TestHealthMonitor_MinSamplesRequired(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.MinSamplesForDecision = 10
	hm := NewHealthMonitor(config)

	// Record fewer than MinSamplesForDecision.
	for i := 0; i < 5; i++ {
		hm.RecordFailure("provider-1", errors.New("fail"))
	}

	status := hm.GetHealth("provider-1")
	if status.State != HealthUnknown {
		t.Errorf("expected HealthUnknown with insufficient samples, got %s", status.State.String())
	}
}

func TestHealthState_String(t *testing.T) {
	tests := []struct {
		state HealthState
		want  string
	}{
		{HealthUnknown, "unknown"},
		{HealthHealthy, "healthy"},
		{HealthDegraded, "degraded"},
		{HealthUnhealthy, "unhealthy"},
		{HealthState(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("HealthState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestHealthMonitor_RecoveryFromUnhealthy(t *testing.T) {
	config := DefaultHealthMonitorConfig()
	config.Window = 100 * time.Millisecond
	config.MinSamplesForDecision = 3
	config.UnhealthyErrorRate = 0.5
	hm := NewHealthMonitor(config)

	now := time.Now()
	hm.nowFunc = func() time.Time { return now }

	// Drive to unhealthy.
	for i := 0; i < 10; i++ {
		hm.RecordFailure("provider-1", errors.New("fail"))
	}

	status := hm.GetHealth("provider-1")
	if status.State != HealthUnhealthy {
		t.Fatalf("expected HealthUnhealthy, got %s", status.State.String())
	}

	// Advance past window and record only successes.
	now = now.Add(200 * time.Millisecond)
	for i := 0; i < 5; i++ {
		hm.RecordSuccess("provider-1", 10*time.Millisecond)
	}

	status = hm.GetHealth("provider-1")
	if status.State != HealthHealthy {
		t.Errorf("expected HealthHealthy after recovery, got %s", status.State.String())
	}
}
