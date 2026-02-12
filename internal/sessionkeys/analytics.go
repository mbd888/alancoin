package sessionkeys

import (
	"context"
	"math/big"

	"github.com/mbd888/alancoin/internal/usdc"
)

// KeyAnalytics provides computed analytics for a single session key.
type KeyAnalytics struct {
	KeyID             string   `json:"keyId"`
	TotalSpent        string   `json:"totalSpent"`
	TransactionCount  int      `json:"transactionCount"`
	AvgTransaction    string   `json:"avgTransaction"`
	BudgetUtilization float64  `json:"budgetUtilization"` // 0-100 percent
	Scopes            []string `json:"scopes"`
	DelegationDepth   int      `json:"delegationDepth"`
	Active            bool     `json:"active"`
}

// OwnerAnalytics provides aggregate analytics across all session keys for an owner.
type OwnerAnalytics struct {
	OwnerAddr          string          `json:"ownerAddr"`
	ActiveKeys         int             `json:"activeKeys"`
	TotalKeys          int             `json:"totalKeys"`
	TotalSpentAll      string          `json:"totalSpentAll"`
	TotalTransactions  int             `json:"totalTransactions"`
	MaxDelegationDepth int             `json:"maxDelegationDepth"`
	TotalDelegatedKeys int             `json:"totalDelegatedKeys"`
	PerKey             []*KeyAnalytics `json:"perKey"`
}

// AnalyticsService computes analytics from existing session key data.
type AnalyticsService struct {
	store Store
}

// NewAnalyticsService creates an analytics service backed by the given store.
func NewAnalyticsService(store Store) *AnalyticsService {
	return &AnalyticsService{store: store}
}

// GetKeyAnalytics computes analytics for a single session key.
func (a *AnalyticsService) GetKeyAnalytics(ctx context.Context, keyID string) (*KeyAnalytics, error) {
	key, err := a.store.Get(ctx, keyID)
	if err != nil {
		return nil, err
	}
	return computeKeyAnalytics(key), nil
}

// GetOwnerAnalytics computes aggregate analytics across all keys for an owner.
func (a *AnalyticsService) GetOwnerAnalytics(ctx context.Context, ownerAddr string) (*OwnerAnalytics, error) {
	keys, err := a.store.GetByOwner(ctx, ownerAddr)
	if err != nil {
		return nil, err
	}

	result := &OwnerAnalytics{
		OwnerAddr: ownerAddr,
		PerKey:    make([]*KeyAnalytics, 0, len(keys)),
	}

	totalSpent := new(big.Int)
	result.TotalKeys = len(keys)

	for _, key := range keys {
		ka := computeKeyAnalytics(key)
		result.PerKey = append(result.PerKey, ka)

		if spent, ok := usdc.Parse(key.Usage.TotalSpent); ok {
			totalSpent.Add(totalSpent, spent)
		}
		result.TotalTransactions += key.Usage.TransactionCount

		if key.IsActive() {
			result.ActiveKeys++
		}
		if key.Depth > result.MaxDelegationDepth {
			result.MaxDelegationDepth = key.Depth
		}
		if key.ParentKeyID != "" {
			result.TotalDelegatedKeys++
		}
	}

	result.TotalSpentAll = usdc.Format(totalSpent)
	return result, nil
}

func computeKeyAnalytics(key *SessionKey) *KeyAnalytics {
	ka := &KeyAnalytics{
		KeyID:            key.ID,
		TotalSpent:       key.Usage.TotalSpent,
		TransactionCount: key.Usage.TransactionCount,
		DelegationDepth:  key.Depth,
		Active:           key.IsActive(),
		Scopes:           key.Permission.Scopes,
	}
	if len(ka.Scopes) == 0 {
		ka.Scopes = DefaultScopes
	}

	// Compute average transaction
	if key.Usage.TransactionCount > 0 {
		spent, ok := usdc.Parse(key.Usage.TotalSpent)
		if ok && spent.Sign() > 0 {
			// avg = totalSpent / txCount (integer division with USDC precision)
			count := big.NewInt(int64(key.Usage.TransactionCount))
			avg := new(big.Int).Div(spent, count)
			ka.AvgTransaction = usdc.Format(avg)
		} else {
			ka.AvgTransaction = "0"
		}
	} else {
		ka.AvgTransaction = "0"
	}

	// Compute budget utilization
	if key.Permission.MaxTotal != "" {
		maxTotal, ok := usdc.Parse(key.Permission.MaxTotal)
		if ok && maxTotal.Sign() > 0 {
			spent, _ := usdc.Parse(key.Usage.TotalSpent)
			// utilization = (spent / maxTotal) * 100
			// Use float for percentage display
			spentF := new(big.Float).SetInt(spent)
			maxF := new(big.Float).SetInt(maxTotal)
			ratio := new(big.Float).Quo(spentF, maxF)
			pct, _ := ratio.Float64()
			ka.BudgetUtilization = pct * 100
		}
	}

	return ka
}
