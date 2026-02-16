package gateway

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory gateway store for demo/development mode.
type MemoryStore struct {
	sessions map[string]*Session
	logs     map[string][]*RequestLog // sessionID → logs
	mu       sync.RWMutex
}

// NewMemoryStore creates a new in-memory gateway store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]*Session),
		logs:     make(map[string][]*RequestLog),
	}
}

func (m *MemoryStore) CreateSession(_ context.Context, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
	return nil
}

func (m *MemoryStore) GetSession(_ context.Context, id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	cp := *session
	if session.AllowedTypes != nil {
		cp.AllowedTypes = make([]string, len(session.AllowedTypes))
		copy(cp.AllowedTypes, session.AllowedTypes)
	}
	return &cp, nil
}

func (m *MemoryStore) UpdateSession(_ context.Context, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[session.ID]; !ok {
		return ErrSessionNotFound
	}
	m.sessions[session.ID] = session
	return nil
}

func (m *MemoryStore) ListSessions(_ context.Context, agentAddr string, limit int) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	var result []*Session
	for _, s := range m.sessions {
		if s.AgentAddr == addr {
			cp := *s
			result = append(result, &cp)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemoryStore) ListSessionsByTenant(_ context.Context, tenantID string, limit int) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Session
	for _, s := range m.sessions {
		if s.TenantID == tenantID {
			cp := *s
			result = append(result, &cp)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemoryStore) ListExpired(_ context.Context, before time.Time, limit int) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Session
	for _, s := range m.sessions {
		if s.Status == StatusActive && !s.ExpiresAt.IsZero() && s.ExpiresAt.Before(before) {
			cp := *s
			if s.AllowedTypes != nil {
				cp.AllowedTypes = make([]string, len(s.AllowedTypes))
				copy(cp.AllowedTypes, s.AllowedTypes)
			}
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) CreateLog(_ context.Context, log *RequestLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs[log.SessionID] = append(m.logs[log.SessionID], log)
	return nil
}

func (m *MemoryStore) ListLogs(_ context.Context, sessionID string, limit int) ([]*RequestLog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	logs := m.logs[sessionID]
	if len(logs) > limit {
		logs = logs[len(logs)-limit:]
	}

	result := make([]*RequestLog, len(logs))
	for i, l := range logs {
		cp := *l
		result[i] = &cp
	}
	return result, nil
}

func (m *MemoryStore) GetBillingSummary(_ context.Context, tenantID string) (*BillingSummaryRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	row := &BillingSummaryRow{SettledVolume: "0", FeesCollected: "0"}
	for _, logs := range m.logs {
		for _, l := range logs {
			if l.TenantID != tenantID {
				continue
			}
			row.TotalRequests++
			if l.Status == "success" {
				row.SettledRequests++
				// Simple string-to-float addition is fine for in-memory demo.
				row.SettledVolume = addDecimalStrings(row.SettledVolume, l.Amount)
				row.FeesCollected = addDecimalStrings(row.FeesCollected, l.FeeAmount)
			}
		}
	}
	return row, nil
}

func (m *MemoryStore) GetBillingTimeSeries(_ context.Context, tenantID, interval string, from, to time.Time) ([]BillingTimePoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	buckets := make(map[time.Time]*BillingTimePoint)
	for _, logs := range m.logs {
		for _, l := range logs {
			if l.TenantID != tenantID || l.CreatedAt.Before(from) || !l.CreatedAt.Before(to) {
				continue
			}
			var bucket time.Time
			switch interval {
			case "hour":
				bucket = l.CreatedAt.Truncate(time.Hour)
			case "week":
				year, week := l.CreatedAt.ISOWeek()
				// Approximate: use Monday of the ISO week.
				bucket = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
				for bucket.Weekday() != time.Monday {
					bucket = bucket.AddDate(0, 0, 1)
				}
				bucket = bucket.AddDate(0, 0, (week-1)*7)
			default:
				bucket = time.Date(l.CreatedAt.Year(), l.CreatedAt.Month(), l.CreatedAt.Day(), 0, 0, 0, 0, time.UTC)
			}
			pt, ok := buckets[bucket]
			if !ok {
				pt = &BillingTimePoint{Bucket: bucket, Volume: "0", Fees: "0"}
				buckets[bucket] = pt
			}
			pt.Requests++
			if l.Status == "success" {
				pt.SettledRequests++
				pt.Volume = addDecimalStrings(pt.Volume, l.Amount)
				pt.Fees = addDecimalStrings(pt.Fees, l.FeeAmount)
			}
		}
	}

	result := make([]BillingTimePoint, 0, len(buckets))
	for _, pt := range buckets {
		result = append(result, *pt)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Bucket.Before(result[j].Bucket) })
	return result, nil
}

func (m *MemoryStore) GetTopServiceTypes(_ context.Context, tenantID string, limit int) ([]ServiceTypeUsage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	types := make(map[string]*ServiceTypeUsage)
	for _, logs := range m.logs {
		for _, l := range logs {
			if l.TenantID != tenantID || l.ServiceType == "" || l.Status != "success" {
				continue
			}
			u, ok := types[l.ServiceType]
			if !ok {
				u = &ServiceTypeUsage{ServiceType: l.ServiceType, Volume: "0"}
				types[l.ServiceType] = u
			}
			u.Requests++
			u.Volume = addDecimalStrings(u.Volume, l.Amount)
		}
	}

	result := make([]ServiceTypeUsage, 0, len(types))
	for _, u := range types {
		result = append(result, *u)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Requests > result[j].Requests })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemoryStore) GetPolicyDenials(_ context.Context, tenantID string, limit int) ([]*RequestLog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*RequestLog
	for _, logs := range m.logs {
		for _, l := range logs {
			if l.TenantID != tenantID || l.Status != "policy_denied" {
				continue
			}
			cp := *l
			result = append(result, &cp)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// addDecimalStrings adds two USDC decimal strings for in-memory billing aggregation.
func addDecimalStrings(a, b string) string {
	fa, fb := parseDecimal(a), parseDecimal(b)
	return fmt.Sprintf("%.6f", fa+fb)
}

func parseDecimal(s string) float64 {
	if s == "" {
		return 0
	}
	var f float64
	_, _ = fmt.Sscanf(s, "%f", &f)
	return f
}

// Compile-time assertion.
var _ Store = (*MemoryStore)(nil)
