package gateway

import (
	"context"
	"time"

	"github.com/mbd888/alancoin/internal/pagination"
)

// ListOption configures optional parameters for list queries.
type ListOption func(*listOpts)

type listOpts struct {
	cursor *pagination.Cursor
}

func applyListOpts(opts []ListOption) listOpts {
	var o listOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// WithCursor filters results to items after the given cursor position.
func WithCursor(cursor string) ListOption {
	return func(o *listOpts) {
		c, err := pagination.Decode(cursor)
		if err == nil {
			o.cursor = c
		}
	}
}

// Store persists gateway session data.
type Store interface {
	CreateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	UpdateSession(ctx context.Context, session *Session) error
	ListSessions(ctx context.Context, agentAddr string, limit int, opts ...ListOption) ([]*Session, error)
	ListSessionsByTenant(ctx context.Context, tenantID string, limit int, opts ...ListOption) ([]*Session, error)
	ListExpired(ctx context.Context, before time.Time, limit int) ([]*Session, error)

	ListByStatus(ctx context.Context, status Status, limit int) ([]*Session, error)

	CreateLog(ctx context.Context, log *RequestLog) error
	ListLogs(ctx context.Context, sessionID string, limit int, opts ...ListOption) ([]*RequestLog, error)

	GetBillingSummary(ctx context.Context, tenantID string) (*BillingSummaryRow, error)

	// Analytics queries for dashboard
	GetBillingTimeSeries(ctx context.Context, tenantID, interval string, from, to time.Time) ([]BillingTimePoint, error)
	GetTopServiceTypes(ctx context.Context, tenantID string, limit int) ([]ServiceTypeUsage, error)
	GetPolicyDenials(ctx context.Context, tenantID string, limit int) ([]*RequestLog, error)
}

// BillingTimePoint represents one bucket in a time-series aggregation.
type BillingTimePoint struct {
	Bucket          time.Time `json:"bucket"`
	Requests        int64     `json:"requests"`
	SettledRequests int64     `json:"settledRequests"`
	Volume          string    `json:"volume"`
	Fees            string    `json:"fees"`
}

// ServiceTypeUsage tracks volume by service type.
type ServiceTypeUsage struct {
	ServiceType string `json:"serviceType"`
	Requests    int64  `json:"requests"`
	Volume      string `json:"volume"`
}
