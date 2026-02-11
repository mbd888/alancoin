package sessionkeys

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

// Policy is a named, reusable set of constraint rules that can be attached
// to session keys. All attached policies must pass for a transaction to be
// approved (additive enforcement).
type Policy struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OwnerAddr string    `json:"ownerAddr"`
	Rules     []Rule    `json:"rules"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Rule is a single constraint within a policy.
type Rule struct {
	Type   string          `json:"type"`   // "rate_limit", "time_window", "cooldown", "tx_count"
	Params json.RawMessage `json:"params"` // Type-specific parameters
}

// RateLimitParams constrains the number of transactions within a sliding window.
type RateLimitParams struct {
	MaxTransactions int `json:"maxTransactions"`
	WindowSeconds   int `json:"windowSeconds"`
}

// TimeWindowParams restricts transactions to specific hours/days.
type TimeWindowParams struct {
	StartHour int      `json:"startHour"`          // 0-23
	EndHour   int      `json:"endHour"`            // 0-23
	Days      []string `json:"days,omitempty"`     // "monday", etc. Empty = all days
	Timezone  string   `json:"timezone,omitempty"` // default "UTC"
}

// CooldownParams enforces a minimum delay between transactions.
type CooldownParams struct {
	MinSeconds int `json:"minSeconds"`
}

// TxCountParams limits the total number of transactions per key.
type TxCountParams struct {
	MaxCount int `json:"maxCount"`
}

// RateLimitState tracks the sliding window for a rate_limit rule.
type RateLimitState struct {
	WindowStart time.Time `json:"windowStart"`
	Count       int       `json:"count"`
}

// PolicyAttachment records a policy attached to a session key,
// along with per-attachment mutable state (e.g. rate limit counters).
type PolicyAttachment struct {
	SessionKeyID string          `json:"sessionKeyId"`
	PolicyID     string          `json:"policyId"`
	AttachedAt   time.Time       `json:"attachedAt"`
	RuleState    json.RawMessage `json:"ruleState,omitempty"`
}

// PolicyStore persists policies and their attachments to session keys.
type PolicyStore interface {
	CreatePolicy(ctx context.Context, policy *Policy) error
	GetPolicy(ctx context.Context, id string) (*Policy, error)
	ListPolicies(ctx context.Context, ownerAddr string) ([]*Policy, error)
	UpdatePolicy(ctx context.Context, policy *Policy) error
	DeletePolicy(ctx context.Context, id string) error

	AttachPolicy(ctx context.Context, att *PolicyAttachment) error
	DetachPolicy(ctx context.Context, sessionKeyID, policyID string) error
	GetAttachments(ctx context.Context, sessionKeyID string) ([]*PolicyAttachment, error)
	UpdateAttachment(ctx context.Context, att *PolicyAttachment) error
}

// Policy validation errors
var (
	ErrPolicyNotFound      = &ValidationError{Code: "policy_not_found", Message: "Policy not found"}
	ErrRateLimitExceeded   = &ValidationError{Code: "rate_limit_exceeded", Message: "Rate limit exceeded for this session key"}
	ErrOutsideTimeWindow   = &ValidationError{Code: "outside_time_window", Message: "Transaction not allowed at this time"}
	ErrCooldownActive      = &ValidationError{Code: "cooldown_active", Message: "Cooldown period has not elapsed since last transaction"}
	ErrTxCountExceeded     = &ValidationError{Code: "tx_count_exceeded", Message: "Maximum transaction count exceeded for this session key"}
	ErrPolicyAlreadyExists = &ValidationError{Code: "policy_exists", Message: "Policy already exists"}
)

// NewPolicy creates a new Policy with a generated ID and timestamps.
func NewPolicy(name, ownerAddr string, rules []Rule) *Policy {
	now := time.Now()
	return &Policy{
		ID:        idgen.WithPrefix("pol_"),
		Name:      name,
		OwnerAddr: strings.ToLower(ownerAddr),
		Rules:     rules,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ValidateRules checks that all rules in the policy have valid types and params.
func ValidateRules(rules []Rule) error {
	for i, r := range rules {
		switch r.Type {
		case "rate_limit":
			var p RateLimitParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return fmt.Errorf("rule[%d] rate_limit: invalid params: %w", i, err)
			}
			if p.MaxTransactions <= 0 {
				return fmt.Errorf("rule[%d] rate_limit: maxTransactions must be positive", i)
			}
			if p.WindowSeconds <= 0 {
				return fmt.Errorf("rule[%d] rate_limit: windowSeconds must be positive", i)
			}
		case "time_window":
			var p TimeWindowParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return fmt.Errorf("rule[%d] time_window: invalid params: %w", i, err)
			}
			if p.StartHour < 0 || p.StartHour > 23 {
				return fmt.Errorf("rule[%d] time_window: startHour must be 0-23", i)
			}
			if p.EndHour < 0 || p.EndHour > 23 {
				return fmt.Errorf("rule[%d] time_window: endHour must be 0-23", i)
			}
			for _, d := range p.Days {
				if !isValidDay(d) {
					return fmt.Errorf("rule[%d] time_window: invalid day %q", i, d)
				}
			}
			if p.Timezone != "" {
				if _, err := time.LoadLocation(p.Timezone); err != nil {
					return fmt.Errorf("rule[%d] time_window: invalid timezone %q", i, p.Timezone)
				}
			}
		case "cooldown":
			var p CooldownParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return fmt.Errorf("rule[%d] cooldown: invalid params: %w", i, err)
			}
			if p.MinSeconds <= 0 {
				return fmt.Errorf("rule[%d] cooldown: minSeconds must be positive", i)
			}
		case "tx_count":
			var p TxCountParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return fmt.Errorf("rule[%d] tx_count: invalid params: %w", i, err)
			}
			if p.MaxCount <= 0 {
				return fmt.Errorf("rule[%d] tx_count: maxCount must be positive", i)
			}
		default:
			// Unknown rule types are silently ignored to allow forward compatibility.
		}
	}
	return nil
}

// evaluatePolicies checks all policies attached to a session key.
// Returns nil if all pass, or the first policy error encountered.
func evaluatePolicies(ctx context.Context, store PolicyStore, key *SessionKey) error {
	attachments, err := store.GetAttachments(ctx, key.ID)
	if err != nil {
		return nil // store unavailable — fail open
	}
	if len(attachments) == 0 {
		return nil
	}

	now := time.Now()

	for _, att := range attachments {
		policy, err := store.GetPolicy(ctx, att.PolicyID)
		if err != nil {
			continue // policy deleted — skip
		}

		for _, rule := range policy.Rules {
			switch rule.Type {
			case "rate_limit":
				if err := evalRateLimit(rule, att, now); err != nil {
					return err
				}
			case "time_window":
				if err := evalTimeWindow(rule, now); err != nil {
					return err
				}
			case "cooldown":
				if err := evalCooldown(rule, key, now); err != nil {
					return err
				}
			case "tx_count":
				if err := evalTxCount(rule, key); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// recordPolicyUsage updates rate limit state on all attached policies after a
// successful transaction.
func recordPolicyUsage(ctx context.Context, store PolicyStore, keyID string) {
	attachments, err := store.GetAttachments(ctx, keyID)
	if err != nil {
		return
	}

	now := time.Now()

	for _, att := range attachments {
		policy, err := store.GetPolicy(ctx, att.PolicyID)
		if err != nil {
			continue
		}

		updated := false
		state := parseRuleState(att.RuleState)

		for _, rule := range policy.Rules {
			if rule.Type != "rate_limit" {
				continue
			}
			var p RateLimitParams
			if err := json.Unmarshal(rule.Params, &p); err != nil {
				continue
			}

			rs := state["rate_limit"]
			windowEnd := rs.WindowStart.Add(time.Duration(p.WindowSeconds) * time.Second)

			if now.After(windowEnd) {
				// Start new window
				rs.WindowStart = now
				rs.Count = 1
			} else {
				rs.Count++
			}

			state["rate_limit"] = rs
			updated = true
		}

		if updated {
			raw, err := json.Marshal(state)
			if err == nil {
				att.RuleState = raw
				_ = store.UpdateAttachment(ctx, att)
			}
		}
	}
}

// --- Per-rule evaluators ---

func evalRateLimit(rule Rule, att *PolicyAttachment, now time.Time) error {
	var p RateLimitParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return nil // malformed — skip
	}

	state := parseRuleState(att.RuleState)
	rs := state["rate_limit"]

	windowEnd := rs.WindowStart.Add(time.Duration(p.WindowSeconds) * time.Second)
	if now.After(windowEnd) {
		// Window expired — would be reset on next usage recording. Allow.
		return nil
	}

	if rs.Count >= p.MaxTransactions {
		return ErrRateLimitExceeded
	}
	return nil
}

func evalTimeWindow(rule Rule, now time.Time) error {
	var p TimeWindowParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return nil
	}

	tz := "UTC"
	if p.Timezone != "" {
		tz = p.Timezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil // invalid tz — skip
	}
	localNow := now.In(loc)

	// Check day restriction
	if len(p.Days) > 0 {
		dayName := strings.ToLower(localNow.Weekday().String())
		found := false
		for _, d := range p.Days {
			if strings.ToLower(d) == dayName {
				found = true
				break
			}
		}
		if !found {
			return ErrOutsideTimeWindow
		}
	}

	// Check hour restriction
	hour := localNow.Hour()
	if p.StartHour <= p.EndHour {
		// Same-day window: e.g., 9-17
		if hour < p.StartHour || hour >= p.EndHour {
			return ErrOutsideTimeWindow
		}
	} else {
		// Overnight window: e.g., 22-6 (22,23,0,1,2,3,4,5)
		if hour < p.StartHour && hour >= p.EndHour {
			return ErrOutsideTimeWindow
		}
	}

	return nil
}

func evalCooldown(rule Rule, key *SessionKey, now time.Time) error {
	var p CooldownParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return nil
	}

	if key.Usage.LastUsed.IsZero() {
		return nil // first transaction — no cooldown
	}

	elapsed := now.Sub(key.Usage.LastUsed)
	if elapsed < time.Duration(p.MinSeconds)*time.Second {
		return ErrCooldownActive
	}
	return nil
}

func evalTxCount(rule Rule, key *SessionKey) error {
	var p TxCountParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return nil
	}

	if key.Usage.TransactionCount >= p.MaxCount {
		return ErrTxCountExceeded
	}
	return nil
}

// --- Helpers ---

func parseRuleState(raw json.RawMessage) map[string]RateLimitState {
	state := make(map[string]RateLimitState)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &state)
	}
	return state
}

func isValidDay(d string) bool {
	switch strings.ToLower(d) {
	case "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday":
		return true
	}
	return false
}
