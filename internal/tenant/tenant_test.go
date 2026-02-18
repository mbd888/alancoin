package tenant

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore_CRUD(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	tenant := &Tenant{
		ID:        "t_1",
		Name:      "Acme Corp",
		Slug:      "acme",
		Plan:      PlanStarter,
		Status:    StatusActive,
		Settings:  DefaultSettingsForPlan(PlanStarter),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Create
	err := store.Create(ctx, tenant)
	require.NoError(t, err)

	// Get by ID
	got, err := store.Get(ctx, "t_1")
	require.NoError(t, err)
	assert.Equal(t, "Acme Corp", got.Name)
	assert.Equal(t, PlanStarter, got.Plan)

	// Get by slug
	got, err = store.GetBySlug(ctx, "acme")
	require.NoError(t, err)
	assert.Equal(t, "t_1", got.ID)

	// Update
	got.Name = "Acme Inc"
	err = store.Update(ctx, got)
	require.NoError(t, err)

	got2, _ := store.Get(ctx, "t_1")
	assert.Equal(t, "Acme Inc", got2.Name)
}

func TestMemoryStore_NotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	_, err := store.Get(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrTenantNotFound)

	_, err = store.GetBySlug(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrTenantNotFound)

	err = store.Update(ctx, &Tenant{ID: "nonexistent"})
	assert.ErrorIs(t, err, ErrTenantNotFound)
}

func TestMemoryStore_DuplicateSlug(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	_ = store.Create(ctx, &Tenant{ID: "t_1", Slug: "acme"})
	err := store.Create(ctx, &Tenant{ID: "t_2", Slug: "acme"})
	assert.ErrorIs(t, err, ErrSlugTaken)
}

func TestMemoryStore_AgentBinding(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	_ = store.Create(ctx, &Tenant{ID: "t_1", Slug: "acme"})

	store.BindAgent("0xAgent1", "t_1")
	store.BindAgent("0xAgent2", "t_1")

	agents, err := store.ListAgents(ctx, "t_1")
	require.NoError(t, err)
	assert.Len(t, agents, 2)

	count, err := store.CountAgents(ctx, "t_1")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// No agents for unknown tenant
	count, _ = store.CountAgents(ctx, "t_unknown")
	assert.Equal(t, 0, count)
}

func TestDefaultSettingsForPlan(t *testing.T) {
	s := DefaultSettingsForPlan(PlanEnterprise)
	assert.Equal(t, 5000, s.RateLimitRPM)
	assert.Equal(t, 0, s.MaxAgents) // unlimited
	assert.Equal(t, 25, s.TakeRateBPS)

	// Unknown plan falls back to free
	s = DefaultSettingsForPlan(Plan("unknown"))
	assert.Equal(t, 60, s.RateLimitRPM)
}

func TestValidPlan(t *testing.T) {
	assert.True(t, ValidPlan(PlanFree))
	assert.True(t, ValidPlan(PlanStarter))
	assert.True(t, ValidPlan(PlanGrowth))
	assert.True(t, ValidPlan(PlanEnterprise))
	assert.False(t, ValidPlan(Plan("premium")))
}
