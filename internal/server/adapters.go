package server

// Glue adapters that bridge domain packages to the contracts each subsystem
// expects. These are pure wrappers (no shared state with Server beyond the
// fields injected at construction). Kept out of server.go so that file stays
// focused on construction, routing, and lifecycle.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"time"

	"github.com/mbd888/alancoin/internal/admin"
	"github.com/mbd888/alancoin/internal/billing"
	"github.com/mbd888/alancoin/internal/chargeback"
	"github.com/mbd888/alancoin/internal/contracts"
	"github.com/mbd888/alancoin/internal/dashboard"
	"github.com/mbd888/alancoin/internal/escrow"
	"github.com/mbd888/alancoin/internal/eventbus"
	"github.com/mbd888/alancoin/internal/forensics"
	"github.com/mbd888/alancoin/internal/gateway"
	"github.com/mbd888/alancoin/internal/health"
	"github.com/mbd888/alancoin/internal/intelligence"
	"github.com/mbd888/alancoin/internal/kya"
	"github.com/mbd888/alancoin/internal/ledger"
	"github.com/mbd888/alancoin/internal/offers"
	"github.com/mbd888/alancoin/internal/realtime"
	"github.com/mbd888/alancoin/internal/reconciliation"
	"github.com/mbd888/alancoin/internal/registry"
	"github.com/mbd888/alancoin/internal/reputation"
	"github.com/mbd888/alancoin/internal/streams"
	"github.com/mbd888/alancoin/internal/supervisor"
	"github.com/mbd888/alancoin/internal/tenant"
	"github.com/mbd888/alancoin/internal/tracerank"
	"github.com/mbd888/alancoin/internal/usdc"
	"github.com/mbd888/alancoin/internal/webhooks"
	"github.com/mbd888/alancoin/internal/workflows"

	"github.com/mbd888/alancoin/internal/config"
)

// --- Admin adapters ---

// adminGatewayAdapter adapts gateway.Service to admin.GatewayService
type adminGatewayAdapter struct {
	svc *gateway.Service
}

func (a *adminGatewayAdapter) GetSession(ctx context.Context, id string) (admin.GatewaySession, error) {
	s, err := a.svc.GetSession(ctx, id)
	if err != nil {
		return admin.GatewaySession{}, err
	}
	return admin.GatewaySession{
		ID:        s.ID,
		AgentAddr: s.AgentAddr,
		MaxTotal:  s.MaxTotal,
		Status:    string(s.Status),
	}, nil
}

func (a *adminGatewayAdapter) CloseSession(ctx context.Context, sessionID, callerAddr string) error {
	_, err := a.svc.CloseSession(ctx, sessionID, callerAddr)
	return err
}

func (a *adminGatewayAdapter) ListStuckSessions(ctx context.Context, limit int) ([]admin.StuckSession, error) {
	sessions, err := a.svc.ListByStatus(ctx, gateway.StatusSettlementFailed, limit)
	if err != nil {
		return nil, err
	}
	result := make([]admin.StuckSession, len(sessions))
	for i, s := range sessions {
		result[i] = admin.StuckSession{
			ID:         s.ID,
			AgentAddr:  s.AgentAddr,
			TenantID:   s.TenantID,
			MaxTotal:   s.MaxTotal,
			TotalSpent: s.TotalSpent,
			Status:     string(s.Status),
			ExpiresAt:  s.ExpiresAt,
			UpdatedAt:  s.UpdatedAt,
		}
	}
	return result, nil
}

// adminEscrowAdapter adapts escrow.Service to admin.EscrowService
type adminEscrowAdapter struct {
	svc *escrow.Service
}

func (a *adminEscrowAdapter) ForceCloseExpired(ctx context.Context) (int, error) {
	return a.svc.ForceCloseExpired(ctx)
}

// coalitionContractAdapter adapts contracts.Service to escrow.ContractChecker
type coalitionContractAdapter struct {
	svc *contracts.Service
}

func (a *coalitionContractAdapter) GetContractByEscrow(ctx context.Context, escrowID string) (*escrow.BoundContract, error) {
	c, err := a.svc.GetByEscrow(ctx, escrowID)
	if err != nil {
		return nil, err
	}
	return &escrow.BoundContract{
		ID:             c.ID,
		Status:         string(c.Status),
		QualityPenalty: c.QualityPenalty,
		HardViolations: c.HardViolations,
	}, nil
}

func (a *coalitionContractAdapter) BindContract(ctx context.Context, contractID, escrowID string) error {
	_, err := a.svc.BindToEscrow(ctx, contractID, escrowID)
	return err
}

func (a *coalitionContractAdapter) MarkContractPassed(ctx context.Context, contractID string) error {
	_, err := a.svc.MarkPassed(ctx, contractID)
	return err
}

// coalitionRealtimeAdapter adapts realtime.Hub to escrow.RealtimeBroadcaster
type coalitionRealtimeAdapter struct {
	hub *realtime.Hub
}

func (a *coalitionRealtimeAdapter) BroadcastCoalitionEvent(eventType string, coalitionID, buyerAddr, status string) {
	a.hub.BroadcastCoalition(map[string]interface{}{
		"event":       eventType,
		"coalitionId": coalitionID,
		"buyerAddr":   buyerAddr,
		"status":      status,
	})
}

func (a *coalitionRealtimeAdapter) BroadcastEscrowEvent(eventType, escrowID, buyer, seller, amount, status string) {
	a.hub.BroadcastEscrowEvent(realtime.EventType(eventType), map[string]interface{}{
		"escrowId": escrowID,
		"from":     buyer,
		"to":       seller,
		"amount":   amount,
		"status":   status,
	})
}

// gatewayRealtimeAdapter adapts realtime.Hub to gateway.RealtimeBroadcaster
type gatewayRealtimeAdapter struct {
	hub *realtime.Hub
}

func (a *gatewayRealtimeAdapter) BroadcastProxySettlement(sessionID, buyer, seller, serviceType, amount string, latencyMs int64) {
	a.hub.BroadcastSessionEvent(realtime.EventProxySettlement, map[string]interface{}{
		"sessionId":   sessionID,
		"from":        buyer,
		"to":          seller,
		"serviceType": serviceType,
		"amount":      amount,
		"latencyMs":   latencyMs,
	})
}

func (a *gatewayRealtimeAdapter) BroadcastSessionCreated(agent, sessionID, maxTotal string) {
	a.hub.BroadcastSessionEvent(realtime.EventSessionCreated, map[string]interface{}{
		"authorAddr": agent,
		"sessionId":  sessionID,
		"maxTotal":   maxTotal,
	})
}

func (a *gatewayRealtimeAdapter) BroadcastSessionClosed(agent, sessionID, totalSpent, status string) {
	a.hub.BroadcastSessionEvent(realtime.EventSessionClosed, map[string]interface{}{
		"authorAddr": agent,
		"sessionId":  sessionID,
		"totalSpent": totalSpent,
		"status":     status,
	})
}

// escrowRealtimeAdapter adapts realtime.Hub to escrow.RealtimeBroadcaster
type escrowRealtimeAdapter struct {
	hub *realtime.Hub
}

func (a *escrowRealtimeAdapter) BroadcastCoalitionEvent(eventType string, coalitionID, buyerAddr, status string) {
	a.hub.BroadcastCoalition(map[string]interface{}{
		"event":       eventType,
		"coalitionId": coalitionID,
		"buyerAddr":   buyerAddr,
		"status":      status,
	})
}

func (a *escrowRealtimeAdapter) BroadcastEscrowEvent(eventType, escrowID, buyer, seller, amount, status string) {
	a.hub.BroadcastEscrowEvent(realtime.EventType(eventType), map[string]interface{}{
		"escrowId": escrowID,
		"from":     buyer,
		"to":       seller,
		"amount":   amount,
		"status":   status,
	})
}

// adminCoalitionAdapter adapts escrow.CoalitionService to admin.CoalitionService
type adminCoalitionAdapter struct {
	svc *escrow.CoalitionService
}

func (a *adminCoalitionAdapter) ForceCloseExpired(ctx context.Context) (int, error) {
	return a.svc.ForceCloseExpired(ctx)
}

// adminStreamAdapter adapts streams.Service to admin.StreamService
type adminStreamAdapter struct {
	svc *streams.Service
}

func (a *adminStreamAdapter) ForceCloseStale(ctx context.Context) (int, error) {
	return a.svc.ForceCloseStale(ctx)
}

// adminDenialExportAdapter adapts supervisor.BaselineStore to admin.DenialExporter
type adminDenialExportAdapter struct {
	store supervisor.BaselineStore
}

func (a *adminDenialExportAdapter) ListDenials(ctx context.Context, since time.Time, limit int) ([]admin.DenialExportRecord, error) {
	denials, err := a.store.ListDenials(ctx, since, limit)
	if err != nil {
		return nil, err
	}
	result := make([]admin.DenialExportRecord, len(denials))
	for i, d := range denials {
		result[i] = admin.DenialExportRecord{
			ID:              d.ID,
			AgentAddr:       d.AgentAddr,
			RuleName:        d.RuleName,
			Reason:          d.Reason,
			Amount:          formatBigInt(d.Amount),
			OpType:          d.OpType,
			Tier:            d.Tier,
			Counterparty:    d.Counterparty,
			HourlyTotal:     formatBigInt(d.HourlyTotal),
			BaselineMean:    formatBigInt(d.BaselineMean),
			BaselineStddev:  formatBigInt(d.BaselineStddev),
			OverrideAllowed: d.OverrideAllowed,
			CreatedAt:       d.CreatedAt,
		}
	}
	return result, nil
}

func formatBigInt(b *big.Int) string {
	if b == nil {
		return "0.000000"
	}
	return usdc.Format(b)
}

// --- Reconciliation adapters ---

// reconcileLedgerAdapter adapts ledger event store to reconciliation.LedgerChecker
type reconcileLedgerAdapter struct {
	eventStore  ledger.EventStore
	ledgerStore ledger.Store
}

func (a *reconcileLedgerAdapter) CheckAll(ctx context.Context) (int, error) {
	results, err := ledger.ReconcileAll(ctx, a.eventStore, a.ledgerStore)
	if err != nil {
		return 0, err
	}
	mismatches := 0
	for _, r := range results {
		if !r.Match {
			mismatches++
		}
	}
	return mismatches, nil
}

// reconcileEscrowAdapter adapts escrow.Store to reconciliation.EscrowChecker
type reconcileEscrowAdapter struct {
	store escrow.Store
}

func (a *reconcileEscrowAdapter) CountExpired(ctx context.Context) (int, error) {
	expired, err := a.store.ListExpired(ctx, time.Now(), 1000)
	if err != nil {
		return 0, err
	}
	return len(expired), nil
}

// reconcileStreamAdapter adapts streams.Store to reconciliation.StreamChecker
type reconcileStreamAdapter struct {
	store streams.Store
}

func (a *reconcileStreamAdapter) CountStale(ctx context.Context) (int, error) {
	stale, err := a.store.ListStale(ctx, time.Now(), 1000)
	if err != nil {
		return 0, err
	}
	return len(stale), nil
}

// reconcileHoldAdapter checks for orphaned ledger holds via SQL.
type reconcileHoldAdapter struct {
	db *sql.DB
}

func (a *reconcileHoldAdapter) CountOrphaned(ctx context.Context) (int, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM ledger_entries le
		LEFT JOIN gateway_sessions gs ON gs.id = le.reference
		WHERE le.type = 'hold'
		  AND le.reference LIKE 'gw_%'
		  AND gs.id IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM ledger_entries le2
		      WHERE le2.agent_address = le.agent_address
		        AND le2.reference = le.reference
		        AND le2.type IN ('settle_hold_out', 'release_hold')
		  )
	`).Scan(&count)
	return count, err
}

// reconcileInvariantAdapter checks the conservation law A+P+E = TotalIn-TotalOut
// across all agent balances.
type reconcileInvariantAdapter struct {
	eventStore  ledger.EventStore
	ledgerStore ledger.Store
}

func (a *reconcileInvariantAdapter) CheckAllInvariants(ctx context.Context) (int, error) {
	agents, err := a.eventStore.GetAllAgents(ctx)
	if err != nil {
		return 0, err
	}
	violations := 0
	for _, addr := range agents {
		bal, err := a.ledgerStore.GetBalance(ctx, addr)
		if err != nil {
			continue
		}
		if err := ledger.CheckInvariant(bal); err != nil {
			violations++
		}
	}
	return violations, nil
}

// --- Dashboard adapters ---

// dashHealthAdapter adapts health.Registry to dashboard.HealthProvider.
type dashHealthAdapter struct {
	registry *health.Registry
}

func (a *dashHealthAdapter) CheckAll() ([]dashboard.SubsystemStatus, string) {
	healthy, statuses := a.registry.CheckAll(context.Background())
	overall := "healthy"
	if !healthy {
		overall = "degraded"
	}
	var out []dashboard.SubsystemStatus
	for _, s := range statuses {
		status := "up"
		if !s.Healthy {
			status = "down"
		}
		out = append(out, dashboard.SubsystemStatus{
			Name:   s.Name,
			Status: status,
			Detail: s.Detail,
		})
	}
	return out, overall
}

// dashReconAdapter adapts reconciliation.Runner to dashboard.ReconciliationProvider.
type dashReconAdapter struct {
	runner *reconciliation.Runner
}

func (a *dashReconAdapter) LastReport() *dashboard.ReconciliationSnapshot {
	report := a.runner.LastReport()
	if report == nil {
		return nil
	}
	return &dashboard.ReconciliationSnapshot{
		LedgerMismatches:    report.LedgerMismatches,
		StuckEscrows:        report.StuckEscrows,
		StaleStreams:        report.StaleStreams,
		OrphanedHolds:       report.OrphanedHolds,
		InvariantViolations: report.InvariantViolations,
		Healthy:             report.Healthy,
		Timestamp:           report.Timestamp.Format(time.RFC3339),
	}
}

// streamRealtimeAdapter adapts realtime.Hub to streams.RealtimeBroadcaster
type streamRealtimeAdapter struct {
	hub *realtime.Hub
}

func (a *streamRealtimeAdapter) BroadcastStreamOpened(streamID, buyer, seller, holdAmount string) {
	a.hub.BroadcastStreamEvent(realtime.EventStreamOpened, map[string]interface{}{
		"streamId": streamID,
		"from":     buyer,
		"to":       seller,
		"amount":   holdAmount,
	})
}

func (a *streamRealtimeAdapter) BroadcastStreamClosed(streamID, buyer, seller, spentAmount, status string) {
	a.hub.BroadcastStreamEvent(realtime.EventStreamClosed, map[string]interface{}{
		"streamId": streamID,
		"from":     buyer,
		"to":       seller,
		"amount":   spentAmount,
		"status":   status,
	})
}

// dashStreamAdapter adapts streams.Service to dashboard.StreamLister
type dashStreamAdapter struct {
	svc *streams.Service
}

func (a *dashStreamAdapter) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]dashboard.StreamSummary, error) {
	items, err := a.svc.ListByAgent(ctx, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.StreamSummary, 0, len(items))
	for _, s := range items {
		out = append(out, dashboard.StreamSummary{
			ID:           s.ID,
			BuyerAddr:    s.BuyerAddr,
			SellerAddr:   s.SellerAddr,
			HoldAmount:   s.HoldAmount,
			SpentAmount:  s.SpentAmount,
			PricePerTick: s.PricePerTick,
			TickCount:    s.TickCount,
			Status:       string(s.Status),
			CreatedAt:    s.CreatedAt,
		})
	}
	return out, nil
}

// dashOfferAdapter adapts offers.Service to dashboard.OfferLister
type dashOfferAdapter struct {
	svc *offers.Service
}

func (a *dashOfferAdapter) ListActive(ctx context.Context, serviceType string, limit int) ([]dashboard.OfferSummary, error) {
	items, err := a.svc.ListOffers(ctx, serviceType, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.OfferSummary, 0, len(items))
	for _, o := range items {
		out = append(out, dashboard.OfferSummary{
			ID:           o.ID,
			SellerAddr:   o.SellerAddr,
			ServiceType:  o.ServiceType,
			Description:  o.Description,
			Price:        o.Price,
			Capacity:     o.Capacity,
			RemainingCap: o.RemainingCap,
			Status:       string(o.Status),
			ExpiresAt:    o.ExpiresAt,
			CreatedAt:    o.CreatedAt,
		})
	}
	return out, nil
}

// dashEscrowAdapter adapts escrow.Service to dashboard.EscrowLister
type dashEscrowAdapter struct {
	svc *escrow.Service
}

func (a *dashEscrowAdapter) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]dashboard.EscrowSummary, error) {
	escrows, err := a.svc.ListByAgent(ctx, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.EscrowSummary, 0, len(escrows))
	for _, e := range escrows {
		out = append(out, dashboard.EscrowSummary{
			ID:            e.ID,
			BuyerAddr:     e.BuyerAddr,
			SellerAddr:    e.SellerAddr,
			Amount:        e.Amount,
			ServiceID:     e.ServiceID,
			Status:        string(e.Status),
			AutoReleaseAt: e.AutoReleaseAt,
			DeliveredAt:   e.DeliveredAt,
			DisputeReason: e.DisputeReason,
			CreatedAt:     e.CreatedAt,
			UpdatedAt:     e.UpdatedAt,
		})
	}
	return out, nil
}

// dashWorkflowAdapter adapts workflows.Service to dashboard.WorkflowLister
type dashWorkflowAdapter struct {
	svc *workflows.Service
}

func (a *dashWorkflowAdapter) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]dashboard.WorkflowSummary, error) {
	wfs, err := a.svc.ListByOwner(ctx, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.WorkflowSummary, 0, len(wfs))
	for _, w := range wfs {
		out = append(out, dashboard.WorkflowSummary{
			ID:             w.ID,
			BuyerAddr:      w.OwnerAddr,
			Name:           w.Name,
			TotalBudget:    w.BudgetTotal,
			SpentAmount:    w.BudgetSpent,
			Status:         string(w.Status),
			TotalSteps:     w.StepsTotal,
			CompletedSteps: w.StepsDone,
			CreatedAt:      w.CreatedAt,
			UpdatedAt:      w.UpdatedAt,
		})
	}
	return out, nil
}

// adminReconcileAdapter adapts reconciliation.Runner to admin.ReconciliationRunner
type adminReconcileAdapter struct {
	runner *reconciliation.Runner
}

func (a *adminReconcileAdapter) RunAll(ctx context.Context) (*admin.ReconciliationReport, error) {
	report, err := a.runner.RunAll(ctx)
	if err != nil {
		return nil, err
	}
	return &admin.ReconciliationReport{
		LedgerMismatches:    report.LedgerMismatches,
		StuckEscrows:        report.StuckEscrows,
		StaleStreams:        report.StaleStreams,
		OrphanedHolds:       report.OrphanedHolds,
		InvariantViolations: report.InvariantViolations,
		Healthy:             report.Healthy,
		Duration:            report.Duration,
		Timestamp:           report.Timestamp,
	}, nil
}

// --- Admin state inspection providers ---

type adminDBStateProvider struct {
	db *sql.DB
}

func (p *adminDBStateProvider) AdminState(_ context.Context) map[string]interface{} {
	stats := p.db.Stats()
	return map[string]interface{}{
		"open_connections":    stats.OpenConnections,
		"in_use":              stats.InUse,
		"idle":                stats.Idle,
		"max_open":            stats.MaxOpenConnections,
		"wait_count":          stats.WaitCount,
		"wait_duration_ms":    stats.WaitDuration.Milliseconds(),
		"max_idle_closed":     stats.MaxIdleClosed,
		"max_lifetime_closed": stats.MaxLifetimeClosed,
	}
}

type adminWSStateProvider struct {
	hub *realtime.Hub
}

func (p *adminWSStateProvider) AdminState(_ context.Context) map[string]interface{} {
	return p.hub.Stats()
}

type adminReconcileStateProvider struct {
	runner *reconciliation.Runner
}

func (p *adminReconcileStateProvider) AdminState(_ context.Context) map[string]interface{} {
	report := p.runner.LastReport()
	if report == nil {
		return map[string]interface{}{"last_run": nil}
	}
	return map[string]interface{}{
		"healthy":           report.Healthy,
		"ledger_mismatches": report.LedgerMismatches,
		"stuck_escrows":     report.StuckEscrows,
		"stale_streams":     report.StaleStreams,
		"orphaned_holds":    report.OrphanedHolds,
		"last_run":          report.Timestamp,
		"duration_ms":       report.Duration.Milliseconds(),
	}
}

// initBillingProvider returns the Stripe provider if configured, otherwise noop.
func initBillingProvider(cfg *config.Config, logger *slog.Logger) billing.Provider {
	if cfg.StripeSecretKey == "" {
		return billing.NewNoopProvider(logger)
	}
	priceIDs := map[tenant.Plan]string{}
	if cfg.StripePriceStarterID != "" {
		priceIDs[tenant.PlanStarter] = cfg.StripePriceStarterID
	}
	if cfg.StripePriceGrowthID != "" {
		priceIDs[tenant.PlanGrowth] = cfg.StripePriceGrowthID
	}
	if cfg.StripePriceEnterpriseID != "" {
		priceIDs[tenant.PlanEnterprise] = cfg.StripePriceEnterpriseID
	}
	return billing.NewStripeProvider(cfg.StripeSecretKey, priceIDs)
}

func billingProviderName(cfg *config.Config) string {
	if cfg.StripeSecretKey != "" {
		return "stripe"
	}
	return "noop"
}

// watcherCreditorAdapter adapts ledger.Store to watcher.Creditor
type watcherCreditorAdapter struct {
	store ledger.Store
}

func (a *watcherCreditorAdapter) Credit(ctx context.Context, agentAddr, amount, txHash, description string) error {
	return a.store.Credit(ctx, agentAddr, amount, txHash, description)
}

func (a *watcherCreditorAdapter) HasDeposit(ctx context.Context, txHash string) (bool, error) {
	return a.store.HasDeposit(ctx, txHash)
}

// watcherAgentResolverAdapter adapts registry.Store to watcher.AgentResolver
type watcherAgentResolverAdapter struct {
	reg registry.Store
}

func (a *watcherAgentResolverAdapter) IsRegisteredAgent(ctx context.Context, addr string) (bool, error) {
	agent, err := a.reg.GetAgent(ctx, addr)
	if err != nil {
		return false, nil // treat errors as "not found"
	}
	return agent != nil, nil
}

// --- KYA adapters ---

type kyaRegistryAdapter struct {
	reg registry.Store
}

func (a *kyaRegistryAdapter) GetAgentName(ctx context.Context, addr string) (string, error) {
	agent, err := a.reg.GetAgent(ctx, addr)
	if err != nil {
		return "", err
	}
	return agent.Name, nil
}

func (a *kyaRegistryAdapter) GetAgentCreatedAt(ctx context.Context, addr string) (time.Time, error) {
	agent, err := a.reg.GetAgent(ctx, addr)
	if err != nil {
		return time.Time{}, err
	}
	return agent.CreatedAt, nil
}

type kyaReputationAdapter struct {
	rep *reputation.RegistryProvider
}

func (a *kyaReputationAdapter) GetScore(ctx context.Context, addr string) (float64, error) {
	if a.rep == nil {
		return 50.0, nil
	}
	score, _, err := a.rep.GetScore(ctx, addr)
	return score, err
}

func (a *kyaReputationAdapter) GetSuccessRate(ctx context.Context, addr string) (float64, error) {
	if a.rep == nil {
		return 0.95, nil
	}
	m, err := a.rep.GetAgentMetrics(ctx, addr)
	if err != nil {
		return 0, err
	}
	if m.TotalTransactions == 0 {
		return 0, nil
	}
	return float64(m.SuccessfulTxns) / float64(m.TotalTransactions), nil
}

func (a *kyaReputationAdapter) GetDisputeRate(_ context.Context, addr string) (float64, error) {
	if a.rep == nil {
		return 0.02, nil
	}
	return a.rep.DisputeRate(addr), nil
}

func (a *kyaReputationAdapter) GetTxCount(ctx context.Context, addr string) (int, error) {
	if a.rep == nil {
		return 50, nil
	}
	m, err := a.rep.GetAgentMetrics(ctx, addr)
	if err != nil {
		return 0, err
	}
	return m.TotalTransactions, nil
}

// --- Arbitration adapters ---

type arbitrationEscrowAdapter struct {
	svc *escrow.Service
}

func (a *arbitrationEscrowAdapter) RefundBuyer(ctx context.Context, escrowID string) error {
	_, err := a.svc.Dispute(ctx, escrowID, "", "arbitration: buyer wins")
	return err
}

func (a *arbitrationEscrowAdapter) ReleaseSeller(ctx context.Context, escrowID string) error {
	_, err := a.svc.Confirm(ctx, escrowID, "")
	return err
}

func (a *arbitrationEscrowAdapter) SplitFunds(ctx context.Context, escrowID string, buyerPct int) error {
	esc, err := a.svc.Get(ctx, escrowID)
	if err != nil {
		return fmt.Errorf("get escrow for split: %w", err)
	}
	totalBig, ok := usdc.Parse(esc.Amount)
	if !ok {
		return fmt.Errorf("invalid escrow amount: %s", esc.Amount)
	}
	sellerPct := 100 - buyerPct
	releaseAmt := new(big.Int).Mul(totalBig, big.NewInt(int64(sellerPct)))
	releaseAmt.Div(releaseAmt, big.NewInt(100))

	_, err = a.svc.ResolveArbitration(ctx, escrowID, esc.ArbitratorAddr, escrow.ResolveRequest{
		Resolution:    "partial",
		ReleaseAmount: usdc.Format(releaseAmt),
		Reason:        fmt.Sprintf("arbitration split: %d%% buyer / %d%% seller", buyerPct, sellerPct),
	})
	return err
}

// --- Forensics adapter ---

type forensicsGatewayAdapter struct {
	svc *forensics.Service
}

func (a *forensicsGatewayAdapter) IngestSpend(ctx context.Context, agentAddr, counterparty string, amountFloat float64, serviceType string) error {
	_, err := a.svc.Ingest(ctx, forensics.SpendEvent{
		AgentAddr:    agentAddr,
		Counterparty: counterparty,
		Amount:       amountFloat,
		ServiceType:  serviceType,
		Timestamp:    time.Now(),
	})
	return err
}

// --- Chargeback adapter ---

type chargebackGatewayAdapter struct {
	svc *chargeback.Service
}

func (a *chargebackGatewayAdapter) RecordGatewaySpend(ctx context.Context, tenantID, agentAddr, amount, serviceType, sessionID string) error {
	// Auto-attribute to the first active cost center for the tenant.
	centers, err := a.svc.ListCostCenters(ctx, tenantID)
	if err != nil || len(centers) == 0 {
		return nil // No cost centers configured — skip silently
	}
	for _, cc := range centers {
		if cc.Active {
			_, err := a.svc.RecordSpend(ctx, cc.ID, tenantID, agentAddr, amount, serviceType, chargeback.SpendOpts{
				SessionID:      sessionID,
				Description:    "auto: gateway proxy",
				IdempotencyKey: sessionID + ":" + amount, // Prevent double-count on event bus redelivery
			})
			return err
		}
	}
	return nil
}

// --- Budget pre-flight adapter (gateway → chargeback budget check) ---

type budgetPreFlightAdapter struct {
	svc *chargeback.Service
}

func (a *budgetPreFlightAdapter) CheckBudget(ctx context.Context, tenantID, _, _ string) error {
	hasRemaining, err := a.svc.HasBudgetRemaining(ctx, tenantID)
	if err != nil {
		return nil // Don't block on check failures
	}
	if !hasRemaining {
		return fmt.Errorf("all cost centers for tenant %s have exhausted their monthly budget", tenantID)
	}
	return nil
}

// --- KYA trust gate adapter (escrow → KYA verification) ---

type kyaTrustGateAdapter struct {
	kyaSvc *kya.Service
}

func (a *kyaTrustGateAdapter) CheckCounterpartyTrust(ctx context.Context, agentAddr string) error {
	if a.kyaSvc == nil {
		return nil // no KYA service → allow all
	}
	cert, err := a.kyaSvc.GetByAgent(ctx, agentAddr)
	if err != nil {
		// No certificate found — allow (KYA is optional, not mandatory)
		return nil
	}
	if !cert.IsValid() {
		return fmt.Errorf("agent %s has an expired or revoked KYA certificate", agentAddr)
	}
	if cert.Reputation.TrustTier == kya.TierD {
		return fmt.Errorf("agent %s has trust tier D (no history) — escrow requires higher trust", agentAddr)
	}
	return nil
}

// --- Event bus adapter ---

type eventBusGatewayAdapter struct {
	bus eventbus.Bus
}

func (a *eventBusGatewayAdapter) PublishSettlement(ctx context.Context, sessionID, tenantID, buyerAddr, sellerAddr, amount, fee, serviceType, serviceID, reference string, latencyMs int64) error {
	amountFloat, _ := strconv.ParseFloat(amount, 64)
	event, err := eventbus.NewEvent(eventbus.TopicSettlement, buyerAddr, eventbus.SettlementPayload{
		SessionID:   sessionID,
		TenantID:    tenantID,
		BuyerAddr:   buyerAddr,
		SellerAddr:  sellerAddr,
		Amount:      amount,
		ServiceType: serviceType,
		ServiceID:   serviceID,
		Fee:         fee,
		Reference:   reference,
		LatencyMs:   latencyMs,
		AmountFloat: amountFloat,
	})
	if err != nil {
		return err
	}
	return a.bus.Publish(ctx, event)
}

// makeForensicsConsumer returns an event-bus consumer that feeds settlement
// events into the forensics anomaly detection engine in batches.
func (s *Server) makeForensicsConsumer() eventbus.Handler {
	return func(ctx context.Context, events []eventbus.Event) error {
		if s.forensicsService == nil {
			return nil
		}
		for _, e := range events {
			var p eventbus.SettlementPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				continue
			}
			_, _ = s.forensicsService.Ingest(ctx, forensics.SpendEvent{
				AgentAddr:    p.BuyerAddr,
				Counterparty: p.SellerAddr,
				Amount:       p.AmountFloat,
				ServiceType:  p.ServiceType,
				Timestamp:    e.Timestamp,
			})
		}
		return nil
	}
}

// makeChargebackConsumer returns an event-bus consumer that auto-attributes
// settlement costs to tenant cost centers in batches.
func (s *Server) makeChargebackConsumer() eventbus.Handler {
	return func(ctx context.Context, events []eventbus.Event) error {
		if s.chargebackService == nil {
			return nil
		}
		for _, e := range events {
			var p eventbus.SettlementPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				continue
			}
			if p.TenantID == "" {
				continue
			}
			adapter := &chargebackGatewayAdapter{s.chargebackService}
			if err := adapter.RecordGatewaySpend(ctx, p.TenantID, p.BuyerAddr, p.Amount, p.ServiceType, p.SessionID); err != nil {
				return fmt.Errorf("chargeback record spend for tenant %s buyer %s: %w", p.TenantID, p.BuyerAddr, err)
			}
		}
		return nil
	}
}

// makeWebhookConsumer returns an event-bus consumer that delivers settlement
// events to webhook subscribers, replacing fire-and-forget goroutines.
func (s *Server) makeWebhookConsumer() eventbus.Handler {
	return func(_ context.Context, events []eventbus.Event) error {
		if s.webhooks == nil {
			return nil
		}
		emitter := webhooks.NewEmitter(s.webhooks, s.logger)
		for _, e := range events {
			var p eventbus.SettlementPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				continue
			}
			emitter.EmitProxySuccess(p.BuyerAddr, p.SessionID, p.SellerAddr, p.Amount)
		}
		return nil
	}
}

// --- Intelligence engine adapters ---

// intelligenceTraceRankAdapter bridges tracerank.Store → intelligence.TraceRankProvider.
type intelligenceTraceRankAdapter struct {
	store tracerank.Store
}

func (a *intelligenceTraceRankAdapter) GetScore(ctx context.Context, address string) (graphScore float64, inDegree, outDegree int, inVolume, outVolume float64, err error) {
	if a.store == nil {
		return 0, 0, 0, 0, 0, nil
	}
	s, err := a.store.GetScore(ctx, address)
	if err != nil || s == nil {
		return 0, 0, 0, 0, 0, err
	}
	return s.GraphScore, s.InDegree, s.OutDegree, s.InVolume, s.OutVolume, nil
}

func (a *intelligenceTraceRankAdapter) GetAllScores(ctx context.Context) (map[string]intelligence.TraceRankData, error) {
	if a.store == nil {
		return map[string]intelligence.TraceRankData{}, nil
	}
	scores, err := a.store.GetTopScores(ctx, 10000)
	if err != nil {
		return nil, err
	}
	result := make(map[string]intelligence.TraceRankData, len(scores))
	for _, s := range scores {
		result[s.Address] = intelligence.TraceRankData{
			GraphScore: s.GraphScore,
			InDegree:   s.InDegree,
			OutDegree:  s.OutDegree,
			InVolume:   s.InVolume,
			OutVolume:  s.OutVolume,
		}
	}
	return result, nil
}

// intelligenceForensicsAdapter bridges forensics.Service → intelligence.ForensicsProvider.
type intelligenceForensicsAdapter struct {
	svc *forensics.Service
}

func (a *intelligenceForensicsAdapter) GetBaseline(ctx context.Context, agentAddr string) (txCount int, meanAmount, stdDevAmount float64, err error) {
	if a.svc == nil {
		return 0, 0, 0, nil
	}
	b, err := a.svc.GetBaseline(ctx, agentAddr)
	if err != nil || b == nil {
		return 0, 0, 0, err
	}
	return b.TxCount, b.MeanAmount, b.StdDevAmount, nil
}

func (a *intelligenceForensicsAdapter) CountAlerts30d(ctx context.Context, agentAddr string) (total, critical int, err error) {
	if a.svc == nil {
		return 0, 0, nil
	}
	alerts, err := a.svc.ListAlerts(ctx, agentAddr, 1000)
	if err != nil {
		return 0, 0, err
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	for _, alert := range alerts {
		if alert.DetectedAt.After(cutoff) {
			total++
			if alert.Severity == forensics.SeverityCritical {
				critical++
			}
		}
	}
	return total, critical, nil
}

// intelligenceReputationAdapter bridges reputation.RegistryProvider → intelligence.ReputationProvider.
type intelligenceReputationAdapter struct {
	provider *reputation.RegistryProvider
}

func (a *intelligenceReputationAdapter) GetMetrics(ctx context.Context, address string) (*intelligence.ReputationData, error) {
	m, err := a.provider.GetAgentMetrics(ctx, address)
	if err != nil || m == nil {
		return nil, err
	}
	// Compute score in-memory from metrics to avoid double-fetching.
	calc := reputation.NewCalculatorWithTraceRank()
	repScore := calc.Calculate(address, *m)
	return &intelligence.ReputationData{
		Score:                repScore.Score,
		TotalTransactions:    m.TotalTransactions,
		SuccessfulTxns:       m.SuccessfulTxns,
		FailedTxns:           m.FailedTxns,
		TotalVolumeUSD:       m.TotalVolumeUSD,
		UniqueCounterparties: m.UniqueCounterparties,
		DaysOnNetwork:        m.DaysOnNetwork,
	}, nil
}

func (a *intelligenceReputationAdapter) GetAllMetrics(ctx context.Context) (map[string]*intelligence.ReputationData, error) {
	metrics, err := a.provider.GetAllAgentMetrics(ctx)
	if err != nil {
		return nil, err
	}
	// Local calculator avoids N+1 fetches inside GetScore.
	calc := reputation.NewCalculatorWithTraceRank()
	result := make(map[string]*intelligence.ReputationData, len(metrics))
	for addr, m := range metrics {
		repScore := calc.Calculate(addr, *m)
		result[addr] = &intelligence.ReputationData{
			Score:                repScore.Score,
			TotalTransactions:    m.TotalTransactions,
			SuccessfulTxns:       m.SuccessfulTxns,
			FailedTxns:           m.FailedTxns,
			TotalVolumeUSD:       m.TotalVolumeUSD,
			UniqueCounterparties: m.UniqueCounterparties,
			DaysOnNetwork:        m.DaysOnNetwork,
		}
	}
	return result, nil
}

// intelligenceAgentSourceAdapter bridges registry.Store → intelligence.AgentSource.
type intelligenceAgentSourceAdapter struct {
	store registry.Store
}

func (a *intelligenceAgentSourceAdapter) ListAllAddresses(ctx context.Context) ([]string, error) {
	agents, err := a.store.ListAgents(ctx, registry.AgentQuery{Limit: 10000})
	if err != nil {
		return nil, err
	}
	addrs := make([]string, len(agents))
	for i, agent := range agents {
		addrs[i] = agent.Address
	}
	return addrs, nil
}
