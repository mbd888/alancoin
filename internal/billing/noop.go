package billing

import (
	"context"
	"log/slog"
	"time"

	"github.com/mbd888/alancoin/internal/tenant"
)

// NoopProvider is a no-op billing provider for dev/demo mode.
// All operations succeed immediately and log the call.
type NoopProvider struct {
	logger *slog.Logger
}

// NewNoopProvider creates a billing provider that does nothing.
func NewNoopProvider(logger *slog.Logger) *NoopProvider {
	return &NoopProvider{logger: logger}
}

func (n *NoopProvider) CreateCustomer(_ context.Context, tenantID, name, _ string) (string, error) {
	n.logger.Debug("noop billing: create customer", "tenant", tenantID, "name", name)
	return "cus_noop_" + tenantID, nil
}

func (n *NoopProvider) CreateSubscription(_ context.Context, customerID string, plan tenant.Plan) (string, error) {
	n.logger.Debug("noop billing: create subscription", "customer", customerID, "plan", plan)
	return "sub_noop_" + customerID, nil
}

func (n *NoopProvider) UpdateSubscription(_ context.Context, subscriptionID string, newPlan tenant.Plan) error {
	n.logger.Debug("noop billing: update subscription", "subscription", subscriptionID, "plan", newPlan)
	return nil
}

func (n *NoopProvider) CancelSubscription(_ context.Context, subscriptionID string) error {
	n.logger.Debug("noop billing: cancel subscription", "subscription", subscriptionID)
	return nil
}

func (n *NoopProvider) GetSubscription(_ context.Context, subscriptionID string) (*Subscription, error) {
	n.logger.Debug("noop billing: get subscription", "subscription", subscriptionID)
	return &Subscription{
		ID:                 subscriptionID,
		Status:             "active",
		CurrentPeriodStart: time.Now().AddDate(0, -1, 0),
		CurrentPeriodEnd:   time.Now().AddDate(0, 1, 0),
	}, nil
}

func (n *NoopProvider) ReportUsage(_ context.Context, customerID string, requests int64, volumeUSDC int64) error {
	n.logger.Debug("noop billing: report usage", "customer", customerID, "requests", requests, "volume_micro_usdc", volumeUSDC)
	return nil
}

var _ Provider = (*NoopProvider)(nil)
