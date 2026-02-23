// Package billing provides subscription management and usage metering via Stripe.
package billing

import (
	"context"
	"time"

	"github.com/mbd888/alancoin/internal/tenant"
)

// Provider abstracts subscription and usage billing so the Stripe implementation
// can be swapped for another provider (or the noop provider in dev mode).
type Provider interface {
	CreateCustomer(ctx context.Context, tenantID, name, email string) (customerID string, err error)
	CreateSubscription(ctx context.Context, customerID string, plan tenant.Plan) (subscriptionID string, err error)
	UpdateSubscription(ctx context.Context, subscriptionID string, newPlan tenant.Plan) error
	CancelSubscription(ctx context.Context, subscriptionID string) error
	GetSubscription(ctx context.Context, subscriptionID string) (*Subscription, error)
	ReportUsage(ctx context.Context, customerID string, requests int64, volumeUSDC int64) error
}

// Subscription represents the current state of a tenant's billing subscription.
type Subscription struct {
	ID                 string    `json:"id"`
	CustomerID         string    `json:"customerId"`
	Plan               string    `json:"plan"`
	Status             string    `json:"status"` // active, past_due, canceled, etc.
	CurrentPeriodStart time.Time `json:"currentPeriodStart"`
	CurrentPeriodEnd   time.Time `json:"currentPeriodEnd"`
	CancelAtPeriodEnd  bool      `json:"cancelAtPeriodEnd"`
}
