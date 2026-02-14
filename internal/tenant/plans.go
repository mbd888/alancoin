package tenant

// PlanConfig defines limits for a pricing tier.
type PlanConfig struct {
	Plan             Plan
	RateLimitRPM     int
	MaxAgents        int    // 0 = unlimited
	MaxSessionBudget string // "0" = unlimited
	TakeRateBPS      int    // basis-point fee on each settled hold (0 = free)
}

// Plans is the hardcoded plan catalogue.
var Plans = map[Plan]PlanConfig{
	PlanFree: {
		Plan:             PlanFree,
		RateLimitRPM:     60,
		MaxAgents:        3,
		MaxSessionBudget: "10.000000",
		TakeRateBPS:      0,
	},
	PlanStarter: {
		Plan:             PlanStarter,
		RateLimitRPM:     300,
		MaxAgents:        10,
		MaxSessionBudget: "100.000000",
		TakeRateBPS:      50,
	},
	PlanGrowth: {
		Plan:             PlanGrowth,
		RateLimitRPM:     1000,
		MaxAgents:        50,
		MaxSessionBudget: "1000.000000",
		TakeRateBPS:      35,
	},
	PlanEnterprise: {
		Plan:             PlanEnterprise,
		RateLimitRPM:     5000,
		MaxAgents:        0,
		MaxSessionBudget: "0",
		TakeRateBPS:      25,
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
		TakeRateBPS:      cfg.TakeRateBPS,
	}
}

// ValidPlan returns true if the plan name is recognised.
func ValidPlan(p Plan) bool {
	_, ok := Plans[p]
	return ok
}
