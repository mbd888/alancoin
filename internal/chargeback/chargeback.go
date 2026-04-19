// Package chargeback implements per-department agent cost attribution.
//
// Every payment flowing through Alancoin (gateway, escrow, workflow, stream)
// can be tagged with a cost center, department, and project. The chargeback
// engine aggregates this data into reports for internal billing, budget
// enforcement, and spend analysis.
//
// Features:
//   - Real-time budget envelopes per cost center
//   - Monthly chargeback reports by department
//   - Spend velocity alerts per cost center
//   - Export to CSV/JSON for GL integration
package chargeback

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/metrics"
	"github.com/mbd888/alancoin/internal/usdc"
)

var (
	ErrCostCenterNotFound = errors.New("chargeback: cost center not found")
	ErrBudgetExceeded     = errors.New("chargeback: cost center budget exceeded")
	ErrBudgetWarning      = errors.New("chargeback: cost center budget warning threshold reached")
	ErrInvalidPeriod      = errors.New("chargeback: invalid period")
)

// CostCenter represents an internal billing unit (department, team, project).
type CostCenter struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenantId"`
	Name          string    `json:"name"`                  // e.g. "Claims Processing"
	Department    string    `json:"department"`            // e.g. "Insurance Operations"
	ProjectCode   string    `json:"projectCode,omitempty"` // e.g. "PROJ-2026-Q1"
	MonthlyBudget string    `json:"monthlyBudget"`         // USDC limit per calendar month
	WarnAtPercent int       `json:"warnAtPercent"`         // Alert at this % of budget (e.g. 80)
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"createdAt"`
}

// SpendEntry records a single cost attribution event.
type SpendEntry struct {
	ID           string    `json:"id"`
	CostCenterID string    `json:"costCenterId"`
	TenantID     string    `json:"tenantId"`
	AgentAddr    string    `json:"agentAddr"`
	Amount       string    `json:"amount"` // USDC
	ServiceType  string    `json:"serviceType"`
	WorkflowID   string    `json:"workflowId,omitempty"`
	SessionID    string    `json:"sessionId,omitempty"`
	EscrowID     string    `json:"escrowId,omitempty"`
	Description  string    `json:"description,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// PeriodSummary aggregates spend for a cost center over a time period.
type PeriodSummary struct {
	CostCenterID   string  `json:"costCenterId"`
	CostCenterName string  `json:"costCenterName"`
	Department     string  `json:"department"`
	Period         string  `json:"period"`     // "2026-03"
	TotalSpend     string  `json:"totalSpend"` // USDC
	TxCount        int     `json:"txCount"`
	TopService     string  `json:"topService"`
	BudgetUsedPct  float64 `json:"budgetUsedPct"` // 0-100
}

// ChargebackReport is a tenant-level report for a given month.
type ChargebackReport struct {
	TenantID        string          `json:"tenantId"`
	Period          string          `json:"period"`
	GeneratedAt     time.Time       `json:"generatedAt"`
	TotalSpend      string          `json:"totalSpend"`
	CostCenterCount int             `json:"costCenterCount"`
	Summaries       []PeriodSummary `json:"summaries"`
}

// Store persists cost centers and spend entries.
type Store interface {
	CreateCostCenter(ctx context.Context, cc *CostCenter) error
	GetCostCenter(ctx context.Context, id string) (*CostCenter, error)
	ListCostCenters(ctx context.Context, tenantID string) ([]*CostCenter, error)
	UpdateCostCenter(ctx context.Context, cc *CostCenter) error

	RecordSpend(ctx context.Context, entry *SpendEntry) error
	GetSpendForPeriod(ctx context.Context, costCenterID string, from, to time.Time) ([]*SpendEntry, error)
	GetTotalForPeriod(ctx context.Context, costCenterID string, from, to time.Time) (*big.Int, int, error)
}

// Service manages cost attribution and budget enforcement.
type Service struct {
	store  Store
	logger *slog.Logger
	mu     sync.RWMutex
}

// NewService creates a new chargeback service.
func NewService(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger}
}

// CreateCostCenter registers a new internal billing unit.
func (s *Service) CreateCostCenter(ctx context.Context, tenantID, name, department, projectCode, monthlyBudget string, warnPct int) (*CostCenter, error) {
	cc := &CostCenter{
		ID:            idgen.WithPrefix("cc_"),
		TenantID:      tenantID,
		Name:          name,
		Department:    department,
		ProjectCode:   projectCode,
		MonthlyBudget: monthlyBudget,
		WarnAtPercent: warnPct,
		Active:        true,
		CreatedAt:     time.Now(),
	}

	if err := s.store.CreateCostCenter(ctx, cc); err != nil {
		return nil, err
	}

	s.logger.Info("chargeback: cost center created",
		"id", cc.ID, "name", name, "department", department, "budget", monthlyBudget)
	return cc, nil
}

// ListCostCenters returns all cost centers for a tenant.
func (s *Service) ListCostCenters(ctx context.Context, tenantID string) ([]*CostCenter, error) {
	return s.store.ListCostCenters(ctx, tenantID)
}

// HasBudgetRemaining checks whether any active cost center for the tenant
// has budget remaining this month. Used as a pre-flight check before gateway proxy.
func (s *Service) HasBudgetRemaining(ctx context.Context, tenantID string) (bool, error) {
	centers, err := s.store.ListCostCenters(ctx, tenantID)
	if err != nil {
		return true, nil // errors → allow (don't block on budget check failures)
	}
	if len(centers) == 0 {
		return true, nil // no cost centers → no enforcement
	}

	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	for _, cc := range centers {
		if !cc.Active {
			continue
		}
		currentTotal, _, err := s.store.GetTotalForPeriod(ctx, cc.ID, monthStart, monthEnd)
		if err != nil {
			continue
		}
		budgetBig := parseUSDC(cc.MonthlyBudget)
		if currentTotal.Cmp(budgetBig) < 0 {
			return true, nil // at least one center has budget
		}
	}
	return false, nil
}

// RecordSpend attributes a cost event to a cost center with budget enforcement.
func (s *Service) RecordSpend(ctx context.Context, costCenterID, tenantID, agentAddr, amount, serviceType string, opts SpendOpts) (*SpendEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cc, err := s.store.GetCostCenter(ctx, costCenterID)
	if err != nil {
		return nil, ErrCostCenterNotFound
	}

	// Budget check: compute current month total
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	currentTotal, _, err := s.store.GetTotalForPeriod(ctx, costCenterID, monthStart, monthEnd)
	if err != nil {
		return nil, err
	}

	amountBig := parseUSDC(amount)
	budgetBig := parseUSDC(cc.MonthlyBudget)

	newTotal := new(big.Int).Add(currentTotal, amountBig)

	// Hard budget check
	if newTotal.Cmp(budgetBig) > 0 {
		metrics.ChargebackBudgetExceededTotal.Inc()
		s.logger.Warn("chargeback: budget exceeded",
			"cost_center", cc.Name, "current", usdc.Format(currentTotal), "attempted", amount)
		return nil, ErrBudgetExceeded
	}

	// Warning threshold check
	if cc.WarnAtPercent > 0 {
		threshold := new(big.Int).Mul(budgetBig, big.NewInt(int64(cc.WarnAtPercent)))
		threshold.Div(threshold, big.NewInt(100))
		if newTotal.Cmp(threshold) >= 0 && currentTotal.Cmp(threshold) < 0 {
			s.logger.Warn("chargeback: budget warning threshold reached",
				"cost_center", cc.Name, "pct", cc.WarnAtPercent, "current", usdc.Format(newTotal))
		}
	}

	// Idempotency: if a key is provided, check for duplicates.
	// This prevents double-counting when event bus redelivers messages.
	entryID := idgen.WithPrefix("sp_")
	if opts.IdempotencyKey != "" {
		entryID = "sp_" + opts.IdempotencyKey // deterministic ID for dedup
	}

	entry := &SpendEntry{
		ID:           entryID,
		CostCenterID: costCenterID,
		TenantID:     tenantID,
		AgentAddr:    agentAddr,
		Amount:       amount,
		ServiceType:  serviceType,
		WorkflowID:   opts.WorkflowID,
		SessionID:    opts.SessionID,
		EscrowID:     opts.EscrowID,
		Description:  opts.Description,
		Timestamp:    now,
	}

	if err := s.store.RecordSpend(ctx, entry); err != nil {
		return nil, err
	}

	metrics.ChargebackSpendTotal.WithLabelValues(serviceType).Inc()

	return entry, nil
}

// SpendOpts carries optional metadata for a spend event.
type SpendOpts struct {
	WorkflowID     string
	SessionID      string
	EscrowID       string
	Description    string
	IdempotencyKey string // If set, prevents duplicate spend entries from event bus redelivery
}

// GenerateReport produces a chargeback report for a tenant for a given month.
func (s *Service) GenerateReport(ctx context.Context, tenantID string, year int, month time.Month) (*ChargebackReport, error) {
	centers, err := s.store.ListCostCenters(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	from := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	period := from.Format("2006-01")

	totalAll := new(big.Int)
	var summaries []PeriodSummary

	for _, cc := range centers {
		total, txCount, err := s.store.GetTotalForPeriod(ctx, cc.ID, from, to)
		if err != nil {
			continue
		}

		budgetBig := parseUSDC(cc.MonthlyBudget)
		pct := 0.0
		if budgetBig.Sign() > 0 {
			// pct = (total * 10000 / budget) / 100 for 2 decimal places
			scaled := new(big.Int).Mul(total, big.NewInt(10000))
			scaled.Div(scaled, budgetBig)
			pct = float64(scaled.Int64()) / 100.0
		}

		totalAll.Add(totalAll, total)

		// Find top service type
		entries, _ := s.store.GetSpendForPeriod(ctx, cc.ID, from, to)
		topService := computeTopService(entries)

		summaries = append(summaries, PeriodSummary{
			CostCenterID:   cc.ID,
			CostCenterName: cc.Name,
			Department:     cc.Department,
			Period:         period,
			TotalSpend:     usdc.Format(total),
			TxCount:        txCount,
			TopService:     topService,
			BudgetUsedPct:  pct,
		})
	}

	return &ChargebackReport{
		TenantID:        tenantID,
		Period:          period,
		GeneratedAt:     time.Now(),
		TotalSpend:      usdc.Format(totalAll),
		CostCenterCount: len(centers),
		Summaries:       summaries,
	}, nil
}

func parseUSDC(s string) *big.Int {
	v, _ := usdc.Parse(s)
	return v
}

func computeTopService(entries []*SpendEntry) string {
	counts := make(map[string]int)
	for _, e := range entries {
		counts[e.ServiceType]++
	}
	topSvc := ""
	topCount := 0
	for svc, count := range counts {
		if count > topCount || (count == topCount && svc < topSvc) {
			topSvc = svc
			topCount = count
		}
	}
	return topSvc
}
