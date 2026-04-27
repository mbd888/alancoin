package intelligence

import "context"

// CreditGate implements gateway.IntelligenceProvider using the intelligence store.
type CreditGate struct {
	store Store
}

func NewCreditGate(store Store) *CreditGate {
	return &CreditGate{store: store}
}

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

// FeeDiscountBPS returns the discount applied to the platform take rate
// (in basis points) for the given tier. Unknown tiers return 0.
func (g *CreditGate) FeeDiscountBPS(tier string) int {
	switch Tier(tier) {
	case TierDiamond:
		return 50
	case TierPlatinum:
		return 30
	case TierGold:
		return 15
	default:
		return 0
	}
}

// EscrowThresholdUSDC returns the transaction amount below which escrow
// can be skipped for the given tier. Unknown tiers return 0 (escrow always
// required).
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
