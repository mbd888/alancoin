package billing

import (
	"context"
	"log/slog"
	"testing"

	"github.com/mbd888/alancoin/internal/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopProvider_CreateCustomer(t *testing.T) {
	p := NewNoopProvider(slog.Default())
	id, err := p.CreateCustomer(context.Background(), "ten_1", "Acme", "acme@example.com")
	require.NoError(t, err)
	assert.Equal(t, "cus_noop_ten_1", id)
}

func TestNoopProvider_CreateSubscription(t *testing.T) {
	p := NewNoopProvider(slog.Default())
	id, err := p.CreateSubscription(context.Background(), "cus_noop_ten_1", tenant.PlanStarter)
	require.NoError(t, err)
	assert.Equal(t, "sub_noop_cus_noop_ten_1", id)
}

func TestNoopProvider_UpdateSubscription(t *testing.T) {
	p := NewNoopProvider(slog.Default())
	err := p.UpdateSubscription(context.Background(), "sub_1", tenant.PlanGrowth)
	assert.NoError(t, err)
}

func TestNoopProvider_CancelSubscription(t *testing.T) {
	p := NewNoopProvider(slog.Default())
	err := p.CancelSubscription(context.Background(), "sub_1")
	assert.NoError(t, err)
}

func TestNoopProvider_GetSubscription(t *testing.T) {
	p := NewNoopProvider(slog.Default())
	sub, err := p.GetSubscription(context.Background(), "sub_1")
	require.NoError(t, err)
	assert.Equal(t, "sub_1", sub.ID)
	assert.Equal(t, "active", sub.Status)
	assert.False(t, sub.CurrentPeriodStart.IsZero())
	assert.False(t, sub.CurrentPeriodEnd.IsZero())
}

func TestNoopProvider_ReportUsage(t *testing.T) {
	p := NewNoopProvider(slog.Default())
	err := p.ReportUsage(context.Background(), "cus_1", 100, 5_000_000)
	assert.NoError(t, err)
}
