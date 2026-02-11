package sessionkeys

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// AlertEvent represents a budget or expiration alert.
type AlertEvent struct {
	KeyID       string    `json:"keyId"`
	OwnerAddr   string    `json:"ownerAddr"`
	Type        string    `json:"type"` // "budget_warning" or "expiring"
	Threshold   float64   `json:"threshold,omitempty"`
	UsedPct     float64   `json:"usedPct,omitempty"`
	TotalSpent  string    `json:"totalSpent,omitempty"`
	MaxTotal    string    `json:"maxTotal,omitempty"`
	ExpiresAt   string    `json:"expiresAt,omitempty"`
	ExpiresIn   string    `json:"expiresIn,omitempty"`
	TriggeredAt time.Time `json:"triggeredAt"`
}

// AlertNotifier is called when a budget or expiration alert fires.
type AlertNotifier interface {
	NotifyAlert(ctx context.Context, event AlertEvent) error
}

// DefaultBudgetThresholds are the budget usage percentages that trigger alerts.
var DefaultBudgetThresholds = []float64{0.50, 0.75, 0.90}

// AlertChecker evaluates session key usage against thresholds.
type AlertChecker struct {
	thresholds []float64
	notifier   AlertNotifier
	// Tracks which thresholds have already fired per key to avoid duplicates.
	// Map key: "keyID:threshold" → true
	fired sync.Map
}

// NewAlertChecker creates an alert checker with default thresholds.
func NewAlertChecker(notifier AlertNotifier) *AlertChecker {
	return &AlertChecker{
		thresholds: DefaultBudgetThresholds,
		notifier:   notifier,
	}
}

// CheckBudget evaluates whether the key's usage has crossed any threshold.
// Should be called after RecordUsage.
func (a *AlertChecker) CheckBudget(ctx context.Context, key *SessionKey) {
	if a.notifier == nil || key.Permission.MaxTotal == "" {
		return
	}

	maxTotal, ok := usdc.Parse(key.Permission.MaxTotal)
	if !ok || maxTotal.Sign() <= 0 {
		return
	}

	spent, _ := usdc.Parse(key.Usage.TotalSpent)
	// usedPct = spent / maxTotal as float64
	usedPct := float64(new(big.Int).Mul(spent, big.NewInt(10000)).Div(
		new(big.Int).Mul(spent, big.NewInt(10000)), maxTotal).Int64()) / 10000.0

	for _, threshold := range a.thresholds {
		if usedPct >= threshold {
			firedKey := fmt.Sprintf("%s:%.2f", key.ID, threshold)
			if _, loaded := a.fired.LoadOrStore(firedKey, true); loaded {
				continue // already fired
			}

			_ = a.notifier.NotifyAlert(ctx, AlertEvent{
				KeyID:       key.ID,
				OwnerAddr:   key.OwnerAddr,
				Type:        "budget_warning",
				Threshold:   threshold,
				UsedPct:     usedPct,
				TotalSpent:  key.Usage.TotalSpent,
				MaxTotal:    key.Permission.MaxTotal,
				TriggeredAt: time.Now(),
			})
		}
	}
}

// CheckExpiration checks if a key is approaching expiration.
// Should be called periodically (e.g., from a timer).
func (a *AlertChecker) CheckExpiration(ctx context.Context, key *SessionKey) {
	if a.notifier == nil || key.RevokedAt != nil {
		return
	}

	remaining := time.Until(key.Permission.ExpiresAt)
	if remaining <= 0 {
		return // already expired
	}

	windows := []struct {
		label    string
		duration time.Duration
	}{
		{"1h", 1 * time.Hour},
		{"24h", 24 * time.Hour},
	}

	for _, w := range windows {
		if remaining <= w.duration {
			firedKey := fmt.Sprintf("%s:expiring:%s", key.ID, w.label)
			if _, loaded := a.fired.LoadOrStore(firedKey, true); loaded {
				continue
			}

			_ = a.notifier.NotifyAlert(ctx, AlertEvent{
				KeyID:       key.ID,
				OwnerAddr:   key.OwnerAddr,
				Type:        "expiring",
				ExpiresAt:   key.Permission.ExpiresAt.Format(time.RFC3339),
				ExpiresIn:   remaining.Round(time.Minute).String(),
				TriggeredAt: time.Now(),
			})
		}
	}
}
