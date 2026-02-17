package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// GatewayService abstracts gateway operations for admin handlers.
type GatewayService interface {
	GetSession(ctx context.Context, id string) (GatewaySession, error)
	CloseSession(ctx context.Context, sessionID, callerAddr string) error
	ListStuckSessions(ctx context.Context, limit int) ([]StuckSession, error)
}

// GatewaySession is a minimal session view for admin operations.
type GatewaySession struct {
	ID        string
	AgentAddr string
	MaxTotal  string
	Status    string
}

// EscrowService abstracts escrow operations for admin handlers.
type EscrowService interface {
	ForceCloseExpired(ctx context.Context) (int, error)
}

// StreamService abstracts stream operations for admin handlers.
type StreamService interface {
	ForceCloseStale(ctx context.Context) (int, error)
}

// ReconciliationRunner runs cross-subsystem reconciliation.
type ReconciliationRunner interface {
	RunAll(ctx context.Context) (*ReconciliationReport, error)
}

// DenialExporter exports denial records for ML training data.
type DenialExporter interface {
	ListDenials(ctx context.Context, since time.Time, limit int) ([]DenialExportRecord, error)
}

// LedgerAdmin abstracts ledger admin ops for force-releasing holds.
type LedgerAdmin interface {
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error
}

// GatewayStoreAdmin gives direct store access for admin ops.
type GatewayStoreAdmin interface {
	GetSessionRaw(ctx context.Context, id string) (*StuckSession, error)
	UpdateSessionStatus(ctx context.Context, id, status string) error
}

// Handler provides admin HTTP endpoints.
type Handler struct {
	gwService    GatewayService
	escrowForce  EscrowService
	streamForce  StreamService
	reconciler   ReconciliationRunner
	denialExport DenialExporter
}

// NewHandler creates a new admin handler.
func NewHandler() *Handler {
	return &Handler{}
}

// WithGatewayService sets the gateway service for session operations.
func (h *Handler) WithGatewayService(svc GatewayService) *Handler {
	h.gwService = svc
	return h
}

// WithEscrowService sets the escrow service for force-close operations.
func (h *Handler) WithEscrowService(svc EscrowService) *Handler {
	h.escrowForce = svc
	return h
}

// WithStreamService sets the stream service for force-close operations.
func (h *Handler) WithStreamService(svc StreamService) *Handler {
	h.streamForce = svc
	return h
}

// WithReconciler sets the reconciliation runner for on-demand reconciliation.
func (h *Handler) WithReconciler(r ReconciliationRunner) *Handler {
	h.reconciler = r
	return h
}

// WithDenialExporter sets the denial exporter for ML training data export.
func (h *Handler) WithDenialExporter(d DenialExporter) *Handler {
	h.denialExport = d
	return h
}

// RegisterRoutes sets up admin routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/admin/gateway/stuck", h.listStuck)
	r.POST("/admin/gateway/sessions/:id/resolve", h.resolveSession)
	r.POST("/admin/gateway/sessions/:id/retry-settlement", h.retrySettlement)
	r.POST("/admin/escrow/force-close-expired", h.forceCloseExpiredEscrows)
	r.POST("/admin/streams/force-close-stale", h.forceCloseStaleStreams)
	r.POST("/admin/reconcile", h.triggerReconciliation)
	r.GET("/admin/denials/export", h.exportDenials)
}

// listStuck returns sessions with settlement_failed status.
func (h *Handler) listStuck(c *gin.Context) {
	if h.gwService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "gateway service not configured"})
		return
	}

	limit := 100
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}

	sessions, err := h.gwService.ListStuckSessions(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list stuck sessions", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "count": len(sessions)})
}

// resolveSession force-closes a stuck session by releasing its hold and marking it closed.
func (h *Handler) resolveSession(c *gin.Context) {
	if h.gwService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "gateway service not configured"})
		return
	}

	sessionID := c.Param("id")
	sess, err := h.gwService.GetSession(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found", "message": err.Error()})
		return
	}

	if sess.Status != "settlement_failed" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_status",
			"message": "Only settlement_failed sessions can be force-resolved",
			"status":  sess.Status,
		})
		return
	}

	// Force-close: use the session owner's address as the caller for authorization.
	if err := h.gwService.CloseSession(c.Request.Context(), sessionID, sess.AgentAddr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve session", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"resolved": true, "sessionId": sessionID})
}

// retrySettlement attempts to re-settle a failed session.
func (h *Handler) retrySettlement(c *gin.Context) {
	if h.gwService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "gateway service not configured"})
		return
	}

	sessionID := c.Param("id")
	sess, err := h.gwService.GetSession(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found", "message": err.Error()})
		return
	}

	if sess.Status != "settlement_failed" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_status",
			"message": "Only settlement_failed sessions can be retried",
			"status":  sess.Status,
		})
		return
	}

	// For retry, we close the session which will release remaining hold.
	if err := h.gwService.CloseSession(c.Request.Context(), sessionID, sess.AgentAddr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "retry settlement failed", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"retried": true, "sessionId": sessionID})
}

// forceCloseExpiredEscrows force-closes all expired escrows.
func (h *Handler) forceCloseExpiredEscrows(c *gin.Context) {
	if h.escrowForce == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "escrow service not configured"})
		return
	}

	closed, err := h.escrowForce.ForceCloseExpired(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to force-close escrows", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"closedCount": closed})
}

// forceCloseStaleStreams force-closes all stale streams.
func (h *Handler) forceCloseStaleStreams(c *gin.Context) {
	if h.streamForce == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stream service not configured"})
		return
	}

	closed, err := h.streamForce.ForceCloseStale(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to force-close streams", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"closedCount": closed})
}

// triggerReconciliation runs an on-demand cross-subsystem reconciliation.
func (h *Handler) triggerReconciliation(c *gin.Context) {
	if h.reconciler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "reconciliation not configured"})
		return
	}

	report, err := h.reconciler.RunAll(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reconciliation failed", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"report": report})
}

// exportDenials exports denial records for ML training data.
func (h *Handler) exportDenials(c *gin.Context) {
	if h.denialExport == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "denial export not configured"})
		return
	}

	since := time.Now().AddDate(0, 0, -30) // Default: last 30 days
	if s := c.Query("since"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			since = parsed
		}
	}

	limit := 1000
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 10000 {
			limit = parsed
		}
	}

	records, err := h.denialExport.ListDenials(c.Request.Context(), since, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to export denials", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"denials": records, "count": len(records), "since": since})
}
