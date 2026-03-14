package gateway

import "log/slog"

// WithHealthMonitor adds a health monitor to the gateway service.
// The health monitor tracks per-provider health metrics and enables
// health-aware routing when combined with a HealthAwareRouter.
func (s *Service) WithHealthMonitor(hm *HealthMonitor) *Service {
	s.healthMonitor = hm
	return s
}

// WithHealthAwareRouter adds a health-aware router to the gateway service.
// The router wraps the existing resolver with graph-based failover logic.
func (s *Service) WithHealthAwareRouter(r *HealthAwareRouter) *Service {
	s.healthRouter = r
	return s
}

// WithRACER adds RACER calibrated routing to the gateway service.
func (s *Service) WithRACER(racer *RACER) *Service {
	s.racer = racer
	return s
}

// HealthMonitor returns the health monitor (or nil). Used for health reporting.
func (s *Service) HealthMonitor() *HealthMonitor {
	return s.healthMonitor
}

// HealthAwareRouter returns the health-aware router (or nil).
func (s *Service) HealthAwareRouter() *HealthAwareRouter {
	return s.healthRouter
}

// SetupRouting configures the full self-healing routing stack:
// health monitor + graph router + RACER. Returns the configured service.
func (s *Service) SetupRouting(logger *slog.Logger) *Service {
	config := DefaultHealthMonitorConfig()
	hm := NewHealthMonitor(config)

	// Wire health state changes to Prometheus metrics.
	hm.OnStateChange(func(providerID string, from, to HealthState) {
		updateProviderHealthMetric(providerID, to)
	})

	router := NewHealthAwareRouter(s.resolver, hm, logger)

	// Wire routing outcomes to Prometheus metrics.
	router.OnOutcome(func(outcome RequestOutcome, from, to string) {
		recordRequestOutcomeMetric(outcome)
		if outcome == OutcomeRerouted {
			recordRerouteMetric(from, to)
		}
	})

	racer := NewRACER(DefaultRACERConfig(), logger)
	racer.OnExpansion(func(_ int) {
		recordRacerExpansionMetric()
	})

	s.healthMonitor = hm
	s.healthRouter = router
	s.racer = racer
	return s
}
