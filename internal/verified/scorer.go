package verified

import (
	"fmt"
	"math"
)

// VerificationPolicy defines eligibility requirements for a reputation tier.
type VerificationPolicy struct {
	MinReputationScore    float64
	MinDaysOnNetwork      int
	MinTransactions       int
	MinSuccessRate        float64
	MinBondAmount         float64 // Minimum bond in USDC
	MaxBondAmount         float64 // Maximum bond in USDC
	GuaranteedSuccessRate float64 // The success rate the agent guarantees (e.g. 95.0)
	SLAWindowSize         int     // Rolling window size for enforcement
	GuaranteePremiumRate  float64 // Premium charged to buyers (e.g. 0.05 = 5%)
}

// DefaultPolicies maps reputation tiers to verification policies.
// Only trusted and elite agents can become verified.
var DefaultPolicies = map[string]*VerificationPolicy{
	"new":         nil,
	"emerging":    nil,
	"established": nil,
	"trusted": {
		MinReputationScore:    60,
		MinDaysOnNetwork:      14,
		MinTransactions:       50,
		MinSuccessRate:        0.95,
		MinBondAmount:         1.0,
		MaxBondAmount:         50.0,
		GuaranteedSuccessRate: 95.0,
		SLAWindowSize:         20,
		GuaranteePremiumRate:  0.07, // 7% premium
	},
	"elite": {
		MinReputationScore:    80,
		MinDaysOnNetwork:      14,
		MinTransactions:       50,
		MinSuccessRate:        0.95,
		MinBondAmount:         5.0,
		MaxBondAmount:         500.0,
		GuaranteedSuccessRate: 97.0, // Elite agents guarantee higher rate
		SLAWindowSize:         20,
		GuaranteePremiumRate:  0.05, // Lower premium (more trusted)
	},
}

// DemoPolicies are relaxed policies for demo/development mode.
var DemoPolicies = map[string]*VerificationPolicy{
	"new": nil,
	"emerging": {
		MinReputationScore:    15,
		MinDaysOnNetwork:      0,
		MinTransactions:       3,
		MinSuccessRate:        0.80,
		MinBondAmount:         0.10,
		MaxBondAmount:         10.0,
		GuaranteedSuccessRate: 90.0,
		SLAWindowSize:         5,
		GuaranteePremiumRate:  0.10,
	},
	"established": {
		MinReputationScore:    40,
		MinDaysOnNetwork:      0,
		MinTransactions:       3,
		MinSuccessRate:        0.85,
		MinBondAmount:         0.50,
		MaxBondAmount:         50.0,
		GuaranteedSuccessRate: 92.0,
		SLAWindowSize:         10,
		GuaranteePremiumRate:  0.08,
	},
	"trusted": {
		MinReputationScore:    60,
		MinDaysOnNetwork:      0,
		MinTransactions:       3,
		MinSuccessRate:        0.90,
		MinBondAmount:         1.0,
		MaxBondAmount:         50.0,
		GuaranteedSuccessRate: 95.0,
		SLAWindowSize:         10,
		GuaranteePremiumRate:  0.07,
	},
	"elite": {
		MinReputationScore:    80,
		MinDaysOnNetwork:      0,
		MinTransactions:       3,
		MinSuccessRate:        0.90,
		MinBondAmount:         5.0,
		MaxBondAmount:         500.0,
		GuaranteedSuccessRate: 97.0,
		SLAWindowSize:         10,
		GuaranteePremiumRate:  0.05,
	},
}

// EvaluationResult is the outcome of a verification evaluation.
type EvaluationResult struct {
	Eligible              bool    `json:"eligible"`
	MinBondAmount         float64 `json:"minBondAmount"`
	MaxBondAmount         float64 `json:"maxBondAmount"`
	GuaranteedSuccessRate float64 `json:"guaranteedSuccessRate"`
	SLAWindowSize         int     `json:"slaWindowSize"`
	GuaranteePremiumRate  float64 `json:"guaranteePremiumRate"`
	ReputationScore       float64 `json:"reputationScore"`
	ReputationTier        string  `json:"reputationTier"`
	Reason                string  `json:"reason"`
}

// Scorer evaluates verification eligibility based on reputation and transaction history.
type Scorer struct {
	policies map[string]*VerificationPolicy
}

// NewScorer creates a scorer with default policies.
func NewScorer() *Scorer {
	return &Scorer{policies: DefaultPolicies}
}

// NewDemoScorer creates a scorer with relaxed demo policies.
func NewDemoScorer() *Scorer {
	return &Scorer{policies: DemoPolicies}
}

// Evaluate determines verification eligibility for an agent.
func (s *Scorer) Evaluate(reputationScore float64, tier string, totalTxns int, successRate float64, daysOnNetwork int, totalVolumeUSD float64) *EvaluationResult {
	result := &EvaluationResult{
		ReputationScore: reputationScore,
		ReputationTier:  tier,
	}

	policy, ok := s.policies[tier]
	if !ok || policy == nil {
		result.Reason = fmt.Sprintf("tier %q is not eligible for verification", tier)
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

	// Bond scaling: higher volume = higher max bond allowed
	// maxBond = policy.MaxBondAmount * min(1.0, 0.3 + 0.7*(log10(volume+1)/4))
	volumeFactor := math.Min(1.0, 0.3+0.7*(math.Log10(totalVolumeUSD+1)/4))
	if math.IsNaN(volumeFactor) || math.IsInf(volumeFactor, 0) {
		volumeFactor = 0.3
	}
	maxBond := policy.MaxBondAmount * volumeFactor
	maxBond = math.Round(maxBond*100) / 100
	if maxBond < policy.MinBondAmount {
		maxBond = policy.MinBondAmount
	}

	result.Eligible = true
	result.MinBondAmount = policy.MinBondAmount
	result.MaxBondAmount = maxBond
	result.GuaranteedSuccessRate = policy.GuaranteedSuccessRate
	result.SLAWindowSize = policy.SLAWindowSize
	result.GuaranteePremiumRate = policy.GuaranteePremiumRate
	result.Reason = "eligible"
	return result
}
