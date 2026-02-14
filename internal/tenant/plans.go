package tenant

// PlanConfig defines limits for a pricing tier.
type PlanConfig struct {
	Plan             Plan
	RateLimitRPM     int
	MaxAgents        int    // 0 = unlimited
	MaxSessionBudget string // "0" = unlimited
	IncludedUSDC     string
}

// Plans is the hardcoded plan catalogue.
var Plans = map[Plan]PlanConfig{
	PlanFree: {
		Plan:             PlanFree,
		RateLimitRPM:     60,
		MaxAgents:        3,
		MaxSessionBudget: "10.000000",
		IncludedUSDC:     "0.000000",
	},
	PlanStarter: {
		Plan:             PlanStarter,
		RateLimitRPM:     300,
		MaxAgents:        10,
		MaxSessionBudget: "100.000000",
		IncludedUSDC:     "100.000000",
	},
	PlanGrowth: {
		Plan:             PlanGrowth,
		RateLimitRPM:     1000,
		MaxAgents:        50,
		MaxSessionBudget: "1000.000000",
		IncludedUSDC:     "1000.000000",
	},
	PlanEnterprise: {
		Plan:             PlanEnterprise,
		RateLimitRPM:     5000,
		MaxAgents:        0,
		MaxSessionBudget: "0",
		IncludedUSDC:     "10000.000000",
	},
}

// DefaultSettingsForPlan returns the Settings populated from a plan's defaults.
func DefaultSettingsForPlan(p Plan) Settings {
	cfg, ok := Plans[p]
	if !ok {
		cfg = Plans[PlanFree]
	}
	return Settings{
		RateLimitRPM:     cfg.RateLimitRPM,
		MaxAgents:        cfg.MaxAgents,
		MaxSessionBudget: cfg.MaxSessionBudget,
	}
}

// ValidPlan returns true if the plan name is recognised.
func ValidPlan(p Plan) bool {
	_, ok := Plans[p]
	return ok
}
