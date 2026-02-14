package tenant

import "context"

// Store persists tenant data.
type Store interface {
	Create(ctx context.Context, t *Tenant) error
	Get(ctx context.Context, id string) (*Tenant, error)
	GetBySlug(ctx context.Context, slug string) (*Tenant, error)
	Update(ctx context.Context, t *Tenant) error
	ListAgents(ctx context.Context, tenantID string) ([]string, error) // returns agent addresses
	CountAgents(ctx context.Context, tenantID string) (int, error)
}
