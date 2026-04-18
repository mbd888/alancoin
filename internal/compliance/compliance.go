// Package compliance aggregates signals from forensics, receipts, policy,
// and session keys into a single auditable posture — incidents and a
// readiness report — without originating any new financial state.
//
// The service is a thin composition layer: it consumes events from other
// subsystems, persists incident records, and answers "what controls are in
// place, what incidents are open, what's the receipt chain head?"
package compliance

import (
	"context"
	"errors"
	"time"
)

var (
	ErrIncidentNotFound = errors.New("compliance: incident not found")
	ErrControlNotFound  = errors.New("compliance: control not found")
)

// ControlID is a short stable identifier for a control.
type ControlID string

// ControlStatus is the operational state of a control.
type ControlStatus string

const (
	StatusEnabled  ControlStatus = "enabled"
	StatusDisabled ControlStatus = "disabled"
	StatusDegraded ControlStatus = "degraded" // enabled but something is off (e.g. stale check)
	StatusUnknown  ControlStatus = "unknown"
)

// Control describes a single configuration item the service can report on.
// Controls are declared by the caller (typically at service wiring time)
// and then kept up-to-date via UpdateControl as subsystems check in.
type Control struct {
	ID          ControlID     `json:"id"`
	Title       string        `json:"title"`
	Group       string        `json:"group,omitempty"` // optional grouping, e.g. "audit", "oversight"
	Status      ControlStatus `json:"status"`
	Evidence    string        `json:"evidence,omitempty"` // short human-readable status snippet
	LastChecked time.Time     `json:"lastChecked,omitempty"`
}

// IncidentSeverity mirrors the forensics levels so alerts can feed through
// without a conversion table.
type IncidentSeverity string

const (
	SeverityInfo     IncidentSeverity = "info"
	SeverityWarning  IncidentSeverity = "warning"
	SeverityCritical IncidentSeverity = "critical"
)

// IsAtLeast returns true if sev is at least as severe as floor.
func (sev IncidentSeverity) IsAtLeast(floor IncidentSeverity) bool {
	return severityOrder(sev) >= severityOrder(floor)
}

func severityOrder(sev IncidentSeverity) int {
	switch sev {
	case SeverityCritical:
		return 2
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 0
	default:
		return -1
	}
}

// Incident is a durable record of a noteworthy event the service has seen.
// Source identifies the originating subsystem (e.g. "forensics", "policy").
// ReceiptRef links to a receipt chain entry when one exists so auditors can
// trace from an alert to the signed payment record.
type Incident struct {
	ID           string           `json:"id"`
	Scope        string           `json:"scope"`
	Source       string           `json:"source"`
	Severity     IncidentSeverity `json:"severity"`
	Kind         string           `json:"kind"` // free-form subtype, e.g. "velocity_spike"
	Title        string           `json:"title"`
	Detail       string           `json:"detail,omitempty"`
	AgentAddr    string           `json:"agentAddr,omitempty"`
	ReceiptRef   string           `json:"receiptRef,omitempty"`
	OccurredAt   time.Time        `json:"occurredAt"`
	Acknowledged bool             `json:"acknowledged"`
	AckBy        string           `json:"ackBy,omitempty"`
	AckAt        *time.Time       `json:"ackAt,omitempty"`
	AckNote      string           `json:"ackNote,omitempty"`
}

// IncidentInput is the data a caller provides when recording an incident.
// ID is assigned by the store, not by the caller.
type IncidentInput struct {
	Scope      string
	Source     string
	Severity   IncidentSeverity
	Kind       string
	Title      string
	Detail     string
	AgentAddr  string
	ReceiptRef string
	OccurredAt time.Time
}

// IncidentFilter narrows a List call.
// Zero values for Severity / Source are treated as "any". Since/Until
// bounds are inclusive; zero times are open.
type IncidentFilter struct {
	Scope       string
	MinSeverity IncidentSeverity
	Source      string
	AgentAddr   string
	OnlyUnacked bool
	Since       time.Time
	Until       time.Time
	Limit       int
}

// SeverityCounts is a quick rollup returned by the store.
type SeverityCounts struct {
	Info     int `json:"info"`
	Warning  int `json:"warning"`
	Critical int `json:"critical"`
	Open     int `json:"open"` // total unacked across severities
}

// ReadinessReport is a point-in-time posture summary for a scope.
// It deliberately avoids deep joins — callers that need per-record detail
// should call ListIncidents or the receipts bundle export directly.
type ReadinessReport struct {
	Scope          string         `json:"scope"`
	GeneratedAt    time.Time      `json:"generatedAt"`
	Controls       []Control      `json:"controls"`
	EnabledCount   int            `json:"enabledCount"`
	DegradedCount  int            `json:"degradedCount"`
	DisabledCount  int            `json:"disabledCount"`
	Incidents      SeverityCounts `json:"incidents"`
	ChainHeadHash  string         `json:"chainHeadHash,omitempty"`
	ChainHeadIndex int64          `json:"chainHeadIndex"`
	ChainReceipts  int64          `json:"chainReceipts"` // ChainHeadIndex + 1 when non-empty
	OldestOpen     *time.Time     `json:"oldestOpen,omitempty"`
}

// Store persists incidents and the set of registered controls.
// Implementations must be safe for concurrent use.
type Store interface {
	// Incidents
	RecordIncident(ctx context.Context, in IncidentInput) (*Incident, error)
	GetIncident(ctx context.Context, id string) (*Incident, error)
	ListIncidents(ctx context.Context, filter IncidentFilter) ([]*Incident, error)
	CountBySeverity(ctx context.Context, scope string) (SeverityCounts, error)
	OldestOpen(ctx context.Context, scope string) (*time.Time, error)
	AcknowledgeIncident(ctx context.Context, id, actor, note string) error

	// Controls
	UpsertControl(ctx context.Context, c Control) error
	ListControls(ctx context.Context) ([]Control, error)
}

// ChainHeadProvider is the minimum surface the service needs from the
// receipts package to report chain posture. It's declared here rather than
// imported directly to avoid a circular dependency and to allow tests to
// stub the receipts dependency.
type ChainHeadProvider interface {
	GetChainHead(ctx context.Context, scope string) (*ChainHeadSnapshot, error)
}

// ChainHeadSnapshot is a minimal, package-local copy of what the service
// needs to render in a ReadinessReport. Avoids leaking receipts.ChainHead
// into the compliance API surface.
type ChainHeadSnapshot struct {
	Scope     string
	HeadHash  string
	HeadIndex int64
}
