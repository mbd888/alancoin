package gateway

import (
	"context"
	"time"
)

// Store persists gateway session data.
type Store interface {
	CreateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	UpdateSession(ctx context.Context, session *Session) error
	ListSessions(ctx context.Context, agentAddr string, limit int) ([]*Session, error)
	ListSessionsByTenant(ctx context.Context, tenantID string, limit int) ([]*Session, error)
	ListExpired(ctx context.Context, before time.Time, limit int) ([]*Session, error)

	CreateLog(ctx context.Context, log *RequestLog) error
	ListLogs(ctx context.Context, sessionID string, limit int) ([]*RequestLog, error)

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
