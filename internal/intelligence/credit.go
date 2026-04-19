package intelligence

import "context"

// CreditGate implements gateway.IntelligenceProvider using the intelligence store.
// It provides credit-gated escrow thresholds and dynamic fee discounts based on
// the agent's intelligence tier.
type CreditGate struct {
	store Store
}

// NewCreditGate creates a new credit gate backed by the intelligence store.
func NewCreditGate(store Store) *CreditGate {
	return &CreditGate{store: store}
}

// GetCreditTier returns the intelligence tier and credit score for an agent.
func (g *CreditGate) GetCreditTier(ctx context.Context, agentAddr string) (string, float64, error) {
	profile, err := g.store.GetProfile(ctx, agentAddr)
	if err != nil {
		return "", 0, err
	}
	if profile == nil {
		return "", 0, nil
	}
	return string(profile.Tier), profile.CreditScore, nil
}

// FeeDiscountBPS returns the fee discount in basis points for the given tier.
// Diamond agents get 50bps off, Platinum 30bps, Gold 15bps.
// This creates a direct financial incentive to build intelligence score.
func (g *CreditGate) FeeDiscountBPS(tier string) int {
	switch Tier(tier) {
	case TierDiamond:
		return 50 // 0.50% discount
	case TierPlatinum:
		return 30 // 0.30% discount
	case TierGold:
		return 15 // 0.15% discount
	default:
		return 0
	}
}

// EscrowThresholdUSDC returns the transaction amount (in USDC) below which
// escrow can be skipped for the given tier. This reduces latency and DB ops
// for trusted agents — a tangible reward for building reputation.
//
// Diamond: transactions under $10 skip escrow
// Platinum: transactions under $5 skip escrow
// Gold: transactions under $1 skip escrow
// Others: escrow always required
func (g *CreditGate) EscrowThresholdUSDC(tier string) float64 {
	switch Tier(tier) {
	case TierDiamond:
		return 10.0
	case TierPlatinum:
		return 5.0
	case TierGold:
		return 1.0
	default:
		return 0
	}
}
