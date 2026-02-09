package credit

import (
	"fmt"
	"math"
)

// CreditPolicy defines eligibility requirements and limits for a reputation tier.
type CreditPolicy struct {
	MinReputationScore float64
	MinDaysOnNetwork   int
	MinTransactions    int
	MinSuccessRate     float64
	MaxCreditLimit     float64 // In USD
	InterestRate       float64 // Annual rate (e.g. 0.10 = 10%)
}

// DefaultPolicies maps reputation tiers to their credit policies.
// New and emerging agents are not eligible for credit.
var DefaultPolicies = map[string]*CreditPolicy{
	"new":      nil,
	"emerging": nil,
	"established": {
		MinReputationScore: 40,
		MinDaysOnNetwork:   30,
		MinTransactions:    10,
		MinSuccessRate:     0.95,
		MaxCreditLimit:     5,
		InterestRate:       0.10,
	},
	"trusted": {
		MinReputationScore: 60,
		MinDaysOnNetwork:   30,
		MinTransactions:    10,
		MinSuccessRate:     0.95,
		MaxCreditLimit:     50,
		InterestRate:       0.07,
	},
	"elite": {
		MinReputationScore: 80,
		MinDaysOnNetwork:   30,
		MinTransactions:    10,
		MinSuccessRate:     0.95,
		MaxCreditLimit:     500,
		InterestRate:       0.05,
	},
}

// EvaluationResult is the outcome of a credit evaluation.
type EvaluationResult struct {
	Eligible        bool    `json:"eligible"`
	CreditLimit     float64 `json:"creditLimit"`
	InterestRate    float64 `json:"interestRate"`
	ReputationScore float64 `json:"reputationScore"`
	ReputationTier  string  `json:"reputationTier"`
	Reason          string  `json:"reason"`
}

// Scorer evaluates credit eligibility based on reputation and transaction history.
type Scorer struct {
	policies map[string]*CreditPolicy
}

// NewScorer creates a scorer with default policies.
func NewScorer() *Scorer {
	return &Scorer{policies: DefaultPolicies}
}

// DemoPolicies are relaxed policies for demo/development mode.
// They remove the days-on-network requirement so freshly registered agents
// can demonstrate the credit flow.
var DemoPolicies = map[string]*CreditPolicy{
	"new": nil,
	"emerging": {
		MinReputationScore: 15,
		MinDaysOnNetwork:   0,
		MinTransactions:    5,
		MinSuccessRate:     0.80,
		MaxCreditLimit:     2,
		InterestRate:       0.12,
	},
	"established": {
		MinReputationScore: 40,
		MinDaysOnNetwork:   0,
		MinTransactions:    5,
		MinSuccessRate:     0.90,
		MaxCreditLimit:     5,
		InterestRate:       0.10,
	},
	"trusted": {
		MinReputationScore: 60,
		MinDaysOnNetwork:   0,
		MinTransactions:    5,
		MinSuccessRate:     0.90,
		MaxCreditLimit:     50,
		InterestRate:       0.07,
	},
	"elite": {
		MinReputationScore: 80,
		MinDaysOnNetwork:   0,
		MinTransactions:    5,
		MinSuccessRate:     0.90,
		MaxCreditLimit:     500,
		InterestRate:       0.05,
	},
}

// NewDemoScorer creates a scorer with relaxed demo policies.
func NewDemoScorer() *Scorer {
	return &Scorer{policies: DemoPolicies}
}

// Evaluate determines credit eligibility and limit for an agent.
func (s *Scorer) Evaluate(reputationScore float64, tier string, totalTxns int, successRate float64, daysOnNetwork int, totalVolumeUSD float64) *EvaluationResult {
	result := &EvaluationResult{
		ReputationScore: reputationScore,
		ReputationTier:  tier,
	}

	policy, ok := s.policies[tier]
	if !ok || policy == nil {
		result.Reason = fmt.Sprintf("tier %q is not eligible for credit", tier)
		return result
	}

	if reputationScore < policy.MinReputationScore {
		result.Reason = fmt.Sprintf("reputation score %.1f below minimum %.1f", reputationScore, policy.MinReputationScore)
		return result
	}
	if daysOnNetwork < policy.MinDaysOnNetwork {
		result.Reason = fmt.Sprintf("%d days on network below minimum %d", daysOnNetwork, policy.MinDaysOnNetwork)
		return result
	}
	if totalTxns < policy.MinTransactions {
		result.Reason = fmt.Sprintf("%d transactions below minimum %d", totalTxns, policy.MinTransactions)
		return result
	}
	if successRate < policy.MinSuccessRate {
		result.Reason = fmt.Sprintf("success rate %.2f below minimum %.2f", successRate, policy.MinSuccessRate)
		return result
	}

	// Volume scaling: limit = maxCreditLimit * min(1.0, 0.5 + 0.5*(log10(volume+1)/4))
	volumeFactor := math.Min(1.0, 0.5+0.5*(math.Log10(totalVolumeUSD+1)/4))
	limit := policy.MaxCreditLimit * volumeFactor

	// Round to 2 decimal places
	limit = math.Round(limit*100) / 100

	result.Eligible = true
	result.CreditLimit = limit
	result.InterestRate = policy.InterestRate
	result.Reason = "approved"
	return result
}
