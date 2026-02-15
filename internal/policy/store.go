package policy

import "context"

// Store persists spend policies.
type Store interface {
	Create(ctx context.Context, p *SpendPolicy) error
	Get(ctx context.Context, id string) (*SpendPolicy, error)
	List(ctx context.Context, tenantID string) ([]*SpendPolicy, error)
	Update(ctx context.Context, p *SpendPolicy) error
	Delete(ctx context.Context, id string) error
}
