// Package recovery provides centralized panic recovery for background goroutines.
// It logs panics with stack traces and increments a Prometheus counter so that
// repeated panics trigger alerts rather than being silently swallowed.
package recovery

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/prometheus/client_golang/prometheus"
)

// PanicRecoveriesTotal counts recovered panics by component name.
var PanicRecoveriesTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "panic_recoveries_total",
		Help:      "Total panics recovered by component.",
	},
	[]string{"component"},
)

func init() {
	prometheus.MustRegister(PanicRecoveriesTotal)
}

// Safe wraps fn with panic recovery. If fn panics, the panic is logged at ERROR
// level with a stack trace and the component-scoped Prometheus counter is
// incremented. The panic is NOT re-raised — the goroutine survives.
//
// Usage:
//
//	go recovery.Safe(logger, "escrow_timer", func() {
//	    releaseExpired(ctx)
//	})
//
// Or as a deferred wrapper inside an existing goroutine:
//
//	defer recovery.LogPanic(logger, "webhook_dispatch")
func Safe(logger *slog.Logger, component string, fn func()) {
	defer LogPanic(logger, component)
	fn()
}

// LogPanic is intended to be called as `defer recovery.LogPanic(logger, "name")`.
// It recovers from panics, logs them, and increments the counter.
func LogPanic(logger *slog.Logger, component string) {
	if r := recover(); r != nil {
		PanicRecoveriesTotal.WithLabelValues(component).Inc()
		logger.Error("recovered panic in background goroutine",
			"component", component,
			"panic", fmt.Sprint(r),
			"stack", string(debug.Stack()),
		)
	}
}
