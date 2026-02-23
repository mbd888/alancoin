package billing

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/billing/meterevent"
	"github.com/stripe/stripe-go/v81/customer"
	stripeSubscription "github.com/stripe/stripe-go/v81/subscription"

	"github.com/mbd888/alancoin/internal/tenant"
)

// StripeProvider implements Provider using the Stripe API.
type StripeProvider struct {
	priceIDs map[tenant.Plan]string // plan → Stripe Price ID
}

// NewStripeProvider creates a billing provider backed by Stripe.
// secretKey is used to configure the global stripe client.
// priceIDs maps each paid plan to its Stripe Price ID.
func NewStripeProvider(secretKey string, priceIDs map[tenant.Plan]string) *StripeProvider {
	stripe.Key = secretKey
	return &StripeProvider{priceIDs: priceIDs}
}

func (s *StripeProvider) CreateCustomer(_ context.Context, tenantID, name, email string) (string, error) {
	params := &stripe.CustomerParams{
		Name:  stripe.String(name),
		Email: stripe.String(email),
	}
	params.AddMetadata("tenant_id", tenantID)

	c, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe: create customer: %w", err)
	}
	return c.ID, nil
}

func (s *StripeProvider) CreateSubscription(_ context.Context, customerID string, plan tenant.Plan) (string, error) {
	priceID, ok := s.priceIDs[plan]
	if !ok {
		return "", fmt.Errorf("stripe: no price configured for plan %q", plan)
	}

	params := &stripe.SubscriptionParams{
		Customer: stripe.String(customerID),
		Items: []*stripe.SubscriptionItemsParams{
			{Price: stripe.String(priceID)},
		},
	}

	sub, err := stripeSubscription.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe: create subscription: %w", err)
	}
	return sub.ID, nil
}

func (s *StripeProvider) UpdateSubscription(_ context.Context, subscriptionID string, newPlan tenant.Plan) error {
	priceID, ok := s.priceIDs[newPlan]
	if !ok {
		return fmt.Errorf("stripe: no price configured for plan %q", newPlan)
	}

	// Get current subscription to find the item to update.
	sub, err := stripeSubscription.Get(subscriptionID, nil)
	if err != nil {
		return fmt.Errorf("stripe: get subscription: %w", err)
	}

	if len(sub.Items.Data) == 0 {
		return fmt.Errorf("stripe: subscription has no items")
	}

	params := &stripe.SubscriptionParams{
		Items: []*stripe.SubscriptionItemsParams{
			{
				ID:    stripe.String(sub.Items.Data[0].ID),
				Price: stripe.String(priceID),
			},
		},
		ProrationBehavior: stripe.String("create_prorations"),
	}

	_, err = stripeSubscription.Update(subscriptionID, params)
	if err != nil {
		return fmt.Errorf("stripe: update subscription: %w", err)
	}
	return nil
}

func (s *StripeProvider) CancelSubscription(_ context.Context, subscriptionID string) error {
	params := &stripe.SubscriptionCancelParams{}
	_, err := stripeSubscription.Cancel(subscriptionID, params)
	if err != nil {
		return fmt.Errorf("stripe: cancel subscription: %w", err)
	}
	return nil
}

func (s *StripeProvider) GetSubscription(_ context.Context, subscriptionID string) (*Subscription, error) {
	sub, err := stripeSubscription.Get(subscriptionID, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe: get subscription: %w", err)
	}

	return &Subscription{
		ID:                 sub.ID,
		CustomerID:         sub.Customer.ID,
		Plan:               sub.Items.Data[0].Price.ID,
		Status:             string(sub.Status),
		CurrentPeriodStart: time.Unix(sub.CurrentPeriodStart, 0),
		CurrentPeriodEnd:   time.Unix(sub.CurrentPeriodEnd, 0),
		CancelAtPeriodEnd:  sub.CancelAtPeriodEnd,
	}, nil
}

func (s *StripeProvider) ReportUsage(_ context.Context, customerID string, requests int64, volumeUSDC int64) error {
	now := time.Now().Unix()

	// Report gateway request count.
	if requests > 0 {
		params := &stripe.BillingMeterEventParams{
			EventName: stripe.String("gateway_requests"),
			Payload: map[string]string{
				"stripe_customer_id": customerID,
				"value":              strconv.FormatInt(requests, 10),
			},
			Timestamp: stripe.Int64(now),
		}
		if _, err := meterevent.New(params); err != nil {
			return fmt.Errorf("stripe: report request usage: %w", err)
		}
	}

	// Report settled volume (micro-USDC).
	if volumeUSDC > 0 {
		params := &stripe.BillingMeterEventParams{
			EventName: stripe.String("settled_volume_usdc"),
			Payload: map[string]string{
				"stripe_customer_id": customerID,
				"value":              strconv.FormatInt(volumeUSDC, 10),
			},
			Timestamp: stripe.Int64(now),
		}
		if _, err := meterevent.New(params); err != nil {
			return fmt.Errorf("stripe: report volume usage: %w", err)
		}
	}

	return nil
}

var _ Provider = (*StripeProvider)(nil)
