// Package policy provides tenant-scoped spend policies for the gateway.
//
// Policies are collections of rules that control when gateway proxy requests
// are allowed. They replace the old sessionkeys-based gateway policy adapter
// with a proper tenant-scoped, self-serve system.
package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors
var (
	ErrPolicyNotFound = errors.New("policy: not found")
	ErrNameTaken      = errors.New("policy: name already exists for this tenant")
)

// SpendPolicy is a named, ordered set of rules enforced on gateway proxy calls.
type SpendPolicy struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenantId"`
	Name            string    `json:"name"`
	Rules           []Rule    `json:"rules"`
	Priority        int       `json:"priority"` // lower = evaluated first
	Enabled         bool      `json:"enabled"`
	EnforcementMode string    `json:"enforcementMode"`           // "enforce" (default) or "shadow"
	ShadowExpiresAt time.Time `json:"shadowExpiresAt,omitempty"` // auto-flip deadline (30-day max)
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// Rule is a single constraint within a policy.
type Rule struct {
	Type   string          `json:"type"`
	Params json.RawMessage `json:"params"`
}

// TimeWindowParams restricts requests to specific hours/days.
type TimeWindowParams struct {
	StartHour int      `json:"startHour"`
	EndHour   int      `json:"endHour"`
	Days      []string `json:"days,omitempty"`
	Timezone  string   `json:"timezone,omitempty"`
}

// RateLimitParams limits requests per time window.
type RateLimitParams struct {
	MaxRequests   int `json:"maxRequests"`
	WindowSeconds int `json:"windowSeconds"`
}

// ServiceListParams is used by both allowlist and blocklist rules.
type ServiceListParams struct {
	Services []string `json:"services"`
}

// MaxRequestsParams limits total requests per session.
type MaxRequestsParams struct {
	MaxCount int `json:"maxCount"`
}

// SpendVelocityParams limits spend rate per hour.
type SpendVelocityParams struct {
	MaxPerHour string `json:"maxPerHour"`
}

// ValidateRules checks that all rules have valid types and params.
func ValidateRules(rules []Rule) error {
	for i, r := range rules {
		switch r.Type {
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
		case "rate_limit":
			var p RateLimitParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return fmt.Errorf("rule[%d] rate_limit: invalid params: %w", i, err)
			}
			if p.MaxRequests <= 0 {
				return fmt.Errorf("rule[%d] rate_limit: maxRequests must be positive", i)
			}
			if p.WindowSeconds <= 0 {
				return fmt.Errorf("rule[%d] rate_limit: windowSeconds must be positive", i)
			}
		case "service_allowlist", "service_blocklist":
			var p ServiceListParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return fmt.Errorf("rule[%d] %s: invalid params: %w", i, r.Type, err)
			}
			if len(p.Services) == 0 {
				return fmt.Errorf("rule[%d] %s: services list must not be empty", i, r.Type)
			}
		case "max_requests":
			var p MaxRequestsParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return fmt.Errorf("rule[%d] max_requests: invalid params: %w", i, err)
			}
			if p.MaxCount <= 0 {
				return fmt.Errorf("rule[%d] max_requests: maxCount must be positive", i)
			}
		case "spend_velocity":
			var p SpendVelocityParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return fmt.Errorf("rule[%d] spend_velocity: invalid params: %w", i, err)
			}
			if p.MaxPerHour == "" {
				return fmt.Errorf("rule[%d] spend_velocity: maxPerHour is required", i)
			}
		default:
			return fmt.Errorf("rule[%d]: unknown rule type %q", i, r.Type)
		}
	}
	return nil
}

func isValidDay(d string) bool {
	switch strings.ToLower(d) {
	case "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday":
		return true
	}
	return false
}
