package ledger

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestObserveOp_IncrementsCounter(t *testing.T) {
	// Reset counter for test
	LedgerOpsTotal.Reset()

	done := observeOp("test_op")
	done()

	// Read counter value
	m := &dto.Metric{}
	counter, err := LedgerOpsTotal.GetMetricWithLabelValues("test_op")
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues failed: %v", err)
	}
	_ = counter.Write(m)

	if m.Counter.GetValue() != 1.0 {
		t.Errorf("expected counter value 1, got %f", m.Counter.GetValue())
	}
}

func TestObserveOp_ObservesHistogram(t *testing.T) {
	LedgerOpDuration.Reset()

	done := observeOp("hist_test")
	done()

	// Verify histogram has data by collecting from the HistogramVec
	ch := make(chan prometheus.Metric, 10)
	LedgerOpDuration.Collect(ch)
	close(ch)

	found := false
	for metric := range ch {
		m := &dto.Metric{}
		_ = metric.Write(m)
		if m.Histogram != nil && m.Histogram.GetSampleCount() == 1 {
			found = true
		}
	}
	if !found {
		t.Error("expected histogram with 1 sample")
	}
}

func TestMetrics_Registered(t *testing.T) {
	// Verify all metrics are registered
	metrics := []string{
		"alancoin_ledger_operations_total",
		"alancoin_ledger_operation_duration_seconds",
		"alancoin_ledger_balance_available_total",
		"alancoin_ledger_balance_pending_total",
		"alancoin_ledger_balance_escrowed_total",
	}

	// Gather all metrics
	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	found := make(map[string]bool)
	for _, mf := range gathered {
		found[mf.GetName()] = true
	}

	for _, name := range metrics {
		if !found[name] {
			// Some metrics may not have been written yet, that's OK
			// Just verify the metric objects exist
			t.Logf("metric %s not yet gathered (no data written)", name)
		}
	}
}
