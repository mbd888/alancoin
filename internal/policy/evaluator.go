package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/gateway"
	"github.com/mbd888/alancoin/internal/usdc"
)

// spendVelocityGracePeriod is the minimum session age before spend velocity
// enforcement begins. Too short and a single expensive request triggers a
// false positive; too long and it becomes a bypass window.
const spendVelocityGracePeriod = 60 * time.Second

// Evaluator implements gateway.PolicyEvaluator using tenant-scoped spend policies.
type Evaluator struct {
	store Store
}

// NewEvaluator creates a new policy evaluator.
func NewEvaluator(store Store) *Evaluator {
	return &Evaluator{store: store}
}

// EvaluateProxy checks whether a proxy request should be allowed.
// Caller MUST hold the per-session lock before calling this method.
func (e *Evaluator) EvaluateProxy(ctx context.Context, session *gateway.Session, serviceType string) (*gateway.PolicyDecision, error) {
	start := time.Now()

	if session.TenantID == "" {
		return &gateway.PolicyDecision{Allowed: true, LatencyUs: time.Since(start).Microseconds()}, nil
	}

	policies, err := e.store.List(ctx, session.TenantID)
	if err != nil {
		return nil, fmt.Errorf("policy check failed: %w", err) // fail closed
	}

	sort.Slice(policies, func(i, j int) bool {
		if policies[i].Priority != policies[j].Priority {
			return policies[i].Priority < policies[j].Priority
		}
		return policies[i].CreatedAt.Before(policies[j].CreatedAt)
	})

	evaluated := 0
	for _, pol := range policies {
		if !pol.Enabled {
			continue
		}
		evaluated++
		for _, rule := range pol.Rules {
			if err := evaluateRule(rule, session, serviceType); err != nil {
				decision := &gateway.PolicyDecision{
					Evaluated:  evaluated,
					Allowed:    false,
					DeniedBy:   pol.Name,
					DeniedRule: rule.Type,
					Reason:     err.Error(),
					LatencyUs:  time.Since(start).Microseconds(),
				}
				// Shadow mode: if policy is in shadow mode and not expired,
				// return the denial decision with Shadow=true and nil error
				// so the caller logs it but doesn't block the request.
				if pol.EnforcementMode == "shadow" && (pol.ShadowExpiresAt.IsZero() || time.Now().Before(pol.ShadowExpiresAt)) {
					decision.Shadow = true
					return decision, nil
				}
				return decision, fmt.Errorf("denied by policy %q rule %s: %w", pol.Name, rule.Type, err)
			}
		}
	}

	return &gateway.PolicyDecision{
		Evaluated: evaluated,
		Allowed:   true,
		LatencyUs: time.Since(start).Microseconds(),
	}, nil
}

func evaluateRule(rule Rule, session *gateway.Session, serviceType string) error {
	switch rule.Type {
	case "time_window":
		return evalTimeWindow(rule, time.Now())
	case "rate_limit":
		return evalRateLimit(rule, session)
	case "service_allowlist":
		return evalServiceAllowlist(rule, serviceType)
	case "service_blocklist":
		return evalServiceBlocklist(rule, serviceType)
	case "max_requests":
		return evalMaxRequests(rule, session)
	case "spend_velocity":
		return evalSpendVelocity(rule, session)
	default:
		return nil // unknown types skipped at evaluation for forward compatibility
	}
}

func evalTimeWindow(rule Rule, now time.Time) error {
	var p TimeWindowParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return fmt.Errorf("time_window: malformed params: %w", err)
	}

	tz := "UTC"
	if p.Timezone != "" {
		tz = p.Timezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return fmt.Errorf("time_window: invalid timezone %q: %w", tz, err)
	}
	localNow := now.In(loc)

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
			return fmt.Errorf("outside allowed days")
		}
	}

	hour := localNow.Hour()
	if p.StartHour <= p.EndHour {
		if hour < p.StartHour || hour >= p.EndHour {
			return fmt.Errorf("outside allowed hours (%d-%d)", p.StartHour, p.EndHour)
		}
	} else {
		if hour < p.StartHour && hour >= p.EndHour {
			return fmt.Errorf("outside allowed hours (%d-%d)", p.StartHour, p.EndHour)
		}
	}

	return nil
}

func evalRateLimit(rule Rule, session *gateway.Session) error {
	var p RateLimitParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return fmt.Errorf("rate_limit: malformed params: %w", err)
	}

	elapsed := time.Since(session.CreatedAt)
	window := time.Duration(p.WindowSeconds) * time.Second

	// Fixed window with carry-over: allow up to 2x burst (current + previous window)
	// but never more, regardless of session age. This prevents unlimited accumulation
	// where a long-idle session could burst massively.
	windows := int(elapsed / window)
	if windows < 1 {
		windows = 1
	}
	if windows > 2 {
		windows = 2 // Cap: at most 2x burst, prevents unlimited accumulation
	}
	allowed := p.MaxRequests * windows

	if session.RequestCount >= allowed {
		return fmt.Errorf("rate limit exceeded: %d requests (max %d per %v, %d allowed over %d windows)",
			session.RequestCount, p.MaxRequests, window, allowed, windows)
	}

	return nil
}

func evalServiceAllowlist(rule Rule, serviceType string) error {
	if serviceType == "" {
		return nil // session creation, no service type to check
	}
	var p ServiceListParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return fmt.Errorf("service_allowlist: malformed params: %w", err)
	}
	for _, s := range p.Services {
		if s == serviceType {
			return nil
		}
	}
	return fmt.Errorf("service type %q not in allowlist", serviceType)
}

func evalServiceBlocklist(rule Rule, serviceType string) error {
	if serviceType == "" {
		return nil // session creation, no service type to check
	}
	var p ServiceListParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return fmt.Errorf("service_blocklist: malformed params: %w", err)
	}
	for _, s := range p.Services {
		if s == serviceType {
			return fmt.Errorf("service type %q is blocked", serviceType)
		}
	}
	return nil
}

func evalMaxRequests(rule Rule, session *gateway.Session) error {
	var p MaxRequestsParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return fmt.Errorf("max_requests: malformed params: %w", err)
	}
	if session.RequestCount >= p.MaxCount {
		return fmt.Errorf("maximum requests reached (%d/%d)", session.RequestCount, p.MaxCount)
	}
	return nil
}

func evalSpendVelocity(rule Rule, session *gateway.Session) error {
	var p SpendVelocityParams
	if err := json.Unmarshal(rule.Params, &p); err != nil {
		return fmt.Errorf("spend_velocity: malformed params: %w", err)
	}

	elapsed := time.Since(session.CreatedAt)
	if elapsed < spendVelocityGracePeriod {
		return nil
	}

	spentBig, ok := usdc.Parse(session.TotalSpent)
	if !ok {
		spentBig = new(big.Int)
	}
	maxBig, ok := usdc.Parse(p.MaxPerHour)
	if !ok || maxBig.Sign() <= 0 {
		return nil // invalid max = skip
	}

	// Proportional cap: allowed = maxPerHour * (elapsed / 1 hour).
	// Use integer math: allowed = maxPerHour * elapsedSeconds / 3600.
	elapsedSec := int64(elapsed.Seconds())
	if elapsedSec <= 0 {
		return nil
	}
	allowedBig := new(big.Int).Mul(maxBig, big.NewInt(elapsedSec))
	allowedBig.Div(allowedBig, big.NewInt(3600))

	if spentBig.Cmp(allowedBig) > 0 {
		return fmt.Errorf("spend velocity exceeded: %s spent in %.1f hours (max %s/hour)",
			usdc.Format(spentBig), elapsed.Hours(), p.MaxPerHour)
	}

	return nil
}

// Compile-time check that Evaluator implements gateway.PolicyEvaluator.
var _ gateway.PolicyEvaluator = (*Evaluator)(nil)
