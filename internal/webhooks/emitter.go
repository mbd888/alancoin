package webhooks

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	webhookEmitTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "webhook",
		Name:      "emit_total",
		Help:      "Total webhook emit attempts by event type.",
	}, []string{"event_type"})

	webhookEmitErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "webhook",
		Name:      "emit_errors_total",
		Help:      "Total webhook emit failures by event type.",
	}, []string{"event_type"})
)

func init() {
	prometheus.MustRegister(webhookEmitTotal, webhookEmitErrors)
}

// Emitter wraps a Dispatcher to emit lifecycle events across subsystems.
// All methods are fire-and-forget: errors are logged but never returned.
type Emitter struct {
	d              *Dispatcher
	logger         *slog.Logger
	wg             sync.WaitGroup
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// NewEmitter creates a new webhook emitter.
func NewEmitter(d *Dispatcher, logger *slog.Logger) *Emitter {
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is stored and called in Shutdown()
	return &Emitter{d: d, logger: logger, shutdownCtx: ctx, shutdownCancel: cancel}
}

// Shutdown cancels in-flight webhook deliveries and waits for them to complete.
func (e *Emitter) Shutdown(timeout time.Duration) {
	e.shutdownCancel()
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		e.logger.Warn("webhook emitter: shutdown timed out waiting for in-flight deliveries")
	}
}

func (e *Emitter) emit(agentAddr string, eventType EventType, data map[string]interface{}) {
	if e == nil || e.d == nil {
		return
	}
	e.wg.Add(1)
	defer e.wg.Done()

	webhookEmitTotal.WithLabelValues(string(eventType)).Inc()
	event := &Event{
		ID:        idgen.WithPrefix("evt_"),
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	}
	ctx, cancel := context.WithTimeout(e.shutdownCtx, 30*time.Second)
	defer cancel()
	if err := e.d.DispatchToAgent(ctx, agentAddr, event); err != nil {
		webhookEmitErrors.WithLabelValues(string(eventType)).Inc()
		e.logger.Warn("webhook emit failed", "event", eventType, "agent", agentAddr, "error", err)
	}
}

// --- Gateway events ---

// EmitSessionCreated emits a gateway.session.created event.
func (e *Emitter) EmitSessionCreated(agentAddr, sessionID, maxTotal string) {
	e.emit(agentAddr, EventGatewaySessionCreated, map[string]interface{}{
		"sessionId": sessionID,
		"agentAddr": agentAddr,
		"maxTotal":  maxTotal,
	})
}

// EmitSessionClosed emits a gateway.session.closed event.
func (e *Emitter) EmitSessionClosed(agentAddr, sessionID, totalSpent, status string) {
	e.emit(agentAddr, EventGatewaySessionClosed, map[string]interface{}{
		"sessionId":  sessionID,
		"agentAddr":  agentAddr,
		"totalSpent": totalSpent,
		"status":     status,
	})
}

// EmitProxySuccess emits a gateway.proxy.success event.
func (e *Emitter) EmitProxySuccess(agentAddr, sessionID, serviceUsed, amountPaid string) {
	e.emit(agentAddr, EventGatewayProxySuccess, map[string]interface{}{
		"sessionId":   sessionID,
		"agentAddr":   agentAddr,
		"serviceUsed": serviceUsed,
		"amountPaid":  amountPaid,
	})
}

// EmitSettlementFailed emits a gateway.settlement.failed event.
func (e *Emitter) EmitSettlementFailed(agentAddr, sessionID, sellerAddr, amount string) {
	e.emit(agentAddr, EventGatewaySettlementFailed, map[string]interface{}{
		"sessionId":  sessionID,
		"agentAddr":  agentAddr,
		"sellerAddr": sellerAddr,
		"amount":     amount,
	})
}

// --- Escrow events ---

// EmitEscrowCreated emits an escrow.created event.
func (e *Emitter) EmitEscrowCreated(buyerAddr, escrowID, sellerAddr, amount string) {
	e.emit(buyerAddr, EventEscrowCreated, map[string]interface{}{
		"escrowId":   escrowID,
		"buyerAddr":  buyerAddr,
		"sellerAddr": sellerAddr,
		"amount":     amount,
	})
}

// EmitEscrowDelivered emits an escrow.delivered event.
func (e *Emitter) EmitEscrowDelivered(buyerAddr, escrowID, sellerAddr string) {
	e.emit(buyerAddr, EventEscrowDelivered, map[string]interface{}{
		"escrowId":   escrowID,
		"buyerAddr":  buyerAddr,
		"sellerAddr": sellerAddr,
	})
}

// EmitEscrowReleased emits an escrow.released event.
func (e *Emitter) EmitEscrowReleased(sellerAddr, escrowID, buyerAddr, amount string) {
	e.emit(sellerAddr, EventEscrowReleased, map[string]interface{}{
		"escrowId":   escrowID,
		"buyerAddr":  buyerAddr,
		"sellerAddr": sellerAddr,
		"amount":     amount,
	})
}

// EmitEscrowRefunded emits an escrow.refunded event.
func (e *Emitter) EmitEscrowRefunded(buyerAddr, escrowID, amount string) {
	e.emit(buyerAddr, EventEscrowRefunded, map[string]interface{}{
		"escrowId":  escrowID,
		"buyerAddr": buyerAddr,
		"amount":    amount,
	})
}

// EmitEscrowDisputed emits an escrow.disputed event.
func (e *Emitter) EmitEscrowDisputed(sellerAddr, escrowID, buyerAddr, reason string) {
	e.emit(sellerAddr, EventEscrowDisputed, map[string]interface{}{
		"escrowId":   escrowID,
		"buyerAddr":  buyerAddr,
		"sellerAddr": sellerAddr,
		"reason":     reason,
	})
}

// --- Stream events ---

// EmitStreamOpened emits a stream.opened event.
func (e *Emitter) EmitStreamOpened(sellerAddr, streamID, buyerAddr, holdAmount string) {
	e.emit(sellerAddr, EventStreamOpened, map[string]interface{}{
		"streamId":   streamID,
		"buyerAddr":  buyerAddr,
		"sellerAddr": sellerAddr,
		"holdAmount": holdAmount,
	})
}

// EmitStreamClosed emits a stream.closed event.
func (e *Emitter) EmitStreamClosed(buyerAddr, streamID, sellerAddr, spentAmount, status string) {
	e.emit(buyerAddr, EventStreamClosed, map[string]interface{}{
		"streamId":    streamID,
		"buyerAddr":   buyerAddr,
		"sellerAddr":  sellerAddr,
		"spentAmount": spentAmount,
		"status":      status,
	})
}

// --- KYA events ---

func (e *Emitter) EmitKYAIssued(agentAddr, certID, trustTier string) {
	e.emit(agentAddr, EventKYAIssued, map[string]interface{}{
		"certId":    certID,
		"agentAddr": agentAddr,
		"trustTier": trustTier,
	})
}

func (e *Emitter) EmitKYARevoked(agentAddr, certID, reason string) {
	e.emit(agentAddr, EventKYARevoked, map[string]interface{}{
		"certId":    certID,
		"agentAddr": agentAddr,
		"reason":    reason,
	})
}

// --- Chargeback events ---

func (e *Emitter) EmitChargebackBudgetWarning(agentAddr, costCenterName string, usedPct int) {
	e.emit(agentAddr, EventChargebackBudgetWarning, map[string]interface{}{
		"costCenter": costCenterName,
		"usedPct":    usedPct,
	})
}

func (e *Emitter) EmitChargebackBudgetExceeded(agentAddr, costCenterName, attemptedAmount string) {
	e.emit(agentAddr, EventChargebackBudgetExceeded, map[string]interface{}{
		"costCenter":      costCenterName,
		"attemptedAmount": attemptedAmount,
	})
}

// --- Arbitration events ---

func (e *Emitter) EmitArbitrationCaseFiled(buyerAddr, caseID, escrowID, amount string) {
	e.emit(buyerAddr, EventArbitrationCaseFiled, map[string]interface{}{
		"caseId":   caseID,
		"escrowId": escrowID,
		"amount":   amount,
	})
}

func (e *Emitter) EmitArbitrationCaseResolved(agentAddr, caseID, outcome, decision string) {
	e.emit(agentAddr, EventArbitrationCaseResolved, map[string]interface{}{
		"caseId":   caseID,
		"outcome":  outcome,
		"decision": decision,
	})
}

// --- Forensics events ---

func (e *Emitter) EmitForensicsCriticalAlert(agentAddr, alertID, alertType string, score float64) {
	e.emit(agentAddr, EventForensicsAlertCritical, map[string]interface{}{
		"alertId": alertID,
		"type":    alertType,
		"score":   score,
	})
}

// --- Intelligence events ---

// EmitTierTransition emits an intelligence.tier.transition event when an agent
// moves between intelligence tiers. Critical for EU AI Act compliance monitoring.
func (e *Emitter) EmitTierTransition(agentAddr, oldTier, newTier string, oldScore, newScore float64) {
	e.emit(agentAddr, EventIntelligenceTierTransition, map[string]interface{}{
		"agentAddr":      agentAddr,
		"previousTier":   oldTier,
		"newTier":        newTier,
		"previousScore":  oldScore,
		"newScore":       newScore,
		"scoreChange":    newScore - oldScore,
		"transitionedAt": time.Now().UTC(),
	})
}

// EmitScoreAlert emits an intelligence.score.alert event when an agent's
// credit score drops significantly (>10 points in a single computation).
func (e *Emitter) EmitScoreAlert(agentAddr string, oldScore, newScore float64, reason string) {
	e.emit(agentAddr, EventIntelligenceScoreAlert, map[string]interface{}{
		"agentAddr":   agentAddr,
		"oldScore":    oldScore,
		"newScore":    newScore,
		"scoreChange": newScore - oldScore,
		"reason":      reason,
	})
}
