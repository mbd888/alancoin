package gateway

import "context"

// PolicyEvaluator evaluates spending policies for gateway sessions.
type PolicyEvaluator interface {
	// EvaluateProxy checks whether a proxy request should be allowed.
	// Returns a decision and nil error to allow, non-nil error to deny.
	EvaluateProxy(ctx context.Context, session *Session, serviceType string) (*PolicyDecision, error)
}

// PolicyDecision records the outcome of a policy evaluation.
type PolicyDecision struct {
	Evaluated  int    `json:"evaluated"` // number of policies checked
	Allowed    bool   `json:"allowed"`
	DeniedBy   string `json:"deniedBy,omitempty"`   // policy name that denied, if any
	DeniedRule string `json:"deniedRule,omitempty"` // rule type that denied
	Reason     string `json:"reason,omitempty"`
	Shadow     bool   `json:"shadow,omitempty"` // true = logged only, not enforced
	LatencyUs  int64  `json:"latencyUs"`
}

// Nil-safe accessors for logging.

func (d *PolicyDecision) GetDeniedBy() string {
	if d == nil {
		return ""
	}
	return d.DeniedBy
}

func (d *PolicyDecision) GetDeniedRule() string {
	if d == nil {
		return ""
	}
	return d.DeniedRule
}

func (d *PolicyDecision) GetReason() string {
	if d == nil {
		return ""
	}
	return d.Reason
}
