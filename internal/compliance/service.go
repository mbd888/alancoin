package compliance

import (
	"context"
	"fmt"
	"time"
)

// WebhookNotifier fires a webhook on every critical-severity compliance
// incident. Optional; typically backed by the platform's webhook emitter.
type WebhookNotifier interface {
	EmitComplianceIncidentCritical(agentAddr, incidentID, source, kind, title, scope string)
}

// Service is the compliance aggregation layer. It depends on:
//   - Store (required): persists incidents and control state.
//   - ChainHeadProvider (optional): reports receipt chain head in readiness.
//   - WebhookNotifier (optional): fan-out for critical incidents.
//
// When optional dependencies are nil, the corresponding features simply
// become no-ops — never errors.
type Service struct {
	store    Store
	chain    ChainHeadProvider
	webhooks WebhookNotifier
}

// NewService builds a Service. chain may be nil.
func NewService(store Store, chain ChainHeadProvider) *Service {
	return &Service{store: store, chain: chain}
}

// WithWebhooks wires the critical-incident webhook notifier.
// Safe to call with nil to detach.
func (s *Service) WithWebhooks(w WebhookNotifier) *Service {
	if s == nil {
		return nil
	}
	s.webhooks = w
	return s
}

// RecordFromAlert persists an incident from an external alert (typically
// forensics, policy, or supervisor subsystems).
//
// Side effect: when the incident's severity is Critical and a webhook
// notifier is wired, fires EmitComplianceIncidentCritical asynchronously
// from the notifier's own fan-out path (the notifier is responsible for
// concurrency; this method only invokes it).
func (s *Service) RecordFromAlert(ctx context.Context, input IncidentInput) (*Incident, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("compliance: service not initialized")
	}
	inc, err := s.store.RecordIncident(ctx, input)
	if err != nil {
		return nil, err
	}
	if inc != nil && inc.Severity == SeverityCritical && s.webhooks != nil {
		s.webhooks.EmitComplianceIncidentCritical(inc.AgentAddr, inc.ID, inc.Source, inc.Kind, inc.Title, inc.Scope)
	}
	return inc, nil
}

// GetIncident returns a single incident by ID.
func (s *Service) GetIncident(ctx context.Context, id string) (*Incident, error) {
	return s.store.GetIncident(ctx, id)
}

// ListIncidents returns incidents matching the filter.
func (s *Service) ListIncidents(ctx context.Context, filter IncidentFilter) ([]*Incident, error) {
	return s.store.ListIncidents(ctx, filter)
}

// AcknowledgeIncident marks an incident as acknowledged by actor.
func (s *Service) AcknowledgeIncident(ctx context.Context, id, actor, note string) error {
	return s.store.AcknowledgeIncident(ctx, id, actor, note)
}

// RegisterControl upserts a control declaration.
// Typically called at startup for static controls, then again by the
// relevant subsystem whenever LastChecked / Status changes.
func (s *Service) RegisterControl(ctx context.Context, c Control) error {
	return s.store.UpsertControl(ctx, c)
}

// ListControls returns all registered controls ordered by Group then ID.
func (s *Service) ListControls(ctx context.Context) ([]Control, error) {
	return s.store.ListControls(ctx)
}

// Readiness builds a posture report for the given scope.
// Safe to call even if chain provider is nil or returns an error — chain
// fields are left zero in that case and the rest of the report is filled.
func (s *Service) Readiness(ctx context.Context, scope string) (*ReadinessReport, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("compliance: service not initialized")
	}

	controls, err := s.store.ListControls(ctx)
	if err != nil {
		return nil, fmt.Errorf("list controls: %w", err)
	}
	counts, err := s.store.CountBySeverity(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("count incidents: %w", err)
	}
	oldest, err := s.store.OldestOpen(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("oldest open incident: %w", err)
	}

	report := &ReadinessReport{
		Scope:          scopeOrDefault(scope),
		GeneratedAt:    time.Now().UTC(),
		Controls:       controls,
		Incidents:      counts,
		ChainHeadIndex: -1,
		OldestOpen:     oldest,
	}
	for _, c := range controls {
		switch c.Status {
		case StatusEnabled:
			report.EnabledCount++
		case StatusDegraded:
			report.DegradedCount++
		case StatusDisabled:
			report.DisabledCount++
		}
	}

	if s.chain != nil {
		if head, err := s.chain.GetChainHead(ctx, scope); err == nil && head != nil {
			report.ChainHeadHash = head.HeadHash
			report.ChainHeadIndex = head.HeadIndex
			if head.HeadIndex >= 0 {
				report.ChainReceipts = head.HeadIndex + 1
			}
		}
	}

	return report, nil
}
