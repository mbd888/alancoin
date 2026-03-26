// Package dashboard provides JSON API endpoints for tenant analytics.
package dashboard

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/gateway"
	"github.com/mbd888/alancoin/internal/pagination"
	"github.com/mbd888/alancoin/internal/tenant"
)

// HealthProvider supplies subsystem health data for the dashboard.
type HealthProvider interface {
	CheckAll() ([]SubsystemStatus, string) // returns statuses + overall status string
}

// ReconciliationProvider supplies the last reconciliation report.
type ReconciliationProvider interface {
	LastReport() *ReconciliationSnapshot
}

// ReconciliationSnapshot is the dashboard-facing view of a reconciliation report.
type ReconciliationSnapshot struct {
	LedgerMismatches    int    `json:"ledgerMismatches"`
	StuckEscrows        int    `json:"stuckEscrows"`
	StaleStreams        int    `json:"staleStreams"`
	OrphanedHolds       int    `json:"orphanedHolds"`
	InvariantViolations int    `json:"invariantViolations"`
	Healthy             bool   `json:"healthy"`
	Timestamp           string `json:"timestamp"`
}

// SubsystemStatus represents one subsystem's health.
type SubsystemStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "up", "down", "degraded"
	Detail string `json:"detail"`
}

// EscrowLister lists escrows by agent address. Extracted interface to avoid importing the full escrow package.
type EscrowLister interface {
	ListByAgent(ctx context.Context, agentAddr string, limit int) ([]EscrowSummary, error)
}

// EscrowSummary is a minimal view of an escrow for the dashboard.
type EscrowSummary struct {
	ID            string     `json:"id"`
	BuyerAddr     string     `json:"buyerAddr"`
	SellerAddr    string     `json:"sellerAddr"`
	Amount        string     `json:"amount"`
	ServiceID     string     `json:"serviceId,omitempty"`
	Status        string     `json:"status"`
	AutoReleaseAt time.Time  `json:"autoReleaseAt"`
	DeliveredAt   *time.Time `json:"deliveredAt,omitempty"`
	DisputeReason string     `json:"disputeReason,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// WorkflowLister lists workflows by agent address.
type WorkflowLister interface {
	ListByAgent(ctx context.Context, agentAddr string, limit int) ([]WorkflowSummary, error)
}

// WorkflowSummary is a minimal view of a workflow for the dashboard.
type WorkflowSummary struct {
	ID             string    `json:"id"`
	BuyerAddr      string    `json:"buyerAddr"`
	Name           string    `json:"name"`
	TotalBudget    string    `json:"totalBudget"`
	SpentAmount    string    `json:"spentAmount"`
	Status         string    `json:"status"`
	TotalSteps     int       `json:"totalSteps"`
	CompletedSteps int       `json:"completedSteps"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// StreamLister lists streams by agent address.
type StreamLister interface {
	ListByAgent(ctx context.Context, agentAddr string, limit int) ([]StreamSummary, error)
}

// StreamSummary is a minimal view of a stream for the dashboard.
type StreamSummary struct {
	ID           string    `json:"id"`
	BuyerAddr    string    `json:"buyerAddr"`
	SellerAddr   string    `json:"sellerAddr"`
	HoldAmount   string    `json:"holdAmount"`
	SpentAmount  string    `json:"spentAmount"`
	PricePerTick string    `json:"pricePerTick"`
	TickCount    int       `json:"tickCount"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}

// OfferLister lists active offers.
type OfferLister interface {
	ListActive(ctx context.Context, serviceType string, limit int) ([]OfferSummary, error)
}

// OfferSummary is a minimal view of an offer for the dashboard.
type OfferSummary struct {
	ID           string    `json:"id"`
	SellerAddr   string    `json:"sellerAddr"`
	ServiceType  string    `json:"serviceType"`
	Description  string    `json:"description"`
	Price        string    `json:"price"`
	Capacity     int       `json:"capacity"`
	RemainingCap int       `json:"remainingCap"`
	Status       string    `json:"status"`
	ExpiresAt    time.Time `json:"expiresAt"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Handler provides dashboard API endpoints.
type Handler struct {
	gwStore        gateway.Store
	tenantStore    tenant.Store
	healthProvider HealthProvider
	reconProvider  ReconciliationProvider
	escrowLister   EscrowLister
	workflowLister WorkflowLister
	streamLister   StreamLister
	offerLister    OfferLister
}

// NewHandler creates a new dashboard handler.
func NewHandler(gwStore gateway.Store, tenantStore tenant.Store) *Handler {
	return &Handler{gwStore: gwStore, tenantStore: tenantStore}
}

// WithHealthProvider adds system health data to the dashboard.
func (h *Handler) WithHealthProvider(hp HealthProvider) *Handler {
	h.healthProvider = hp
	return h
}

// WithReconciliationProvider adds reconciliation data to the dashboard.
func (h *Handler) WithReconciliationProvider(rp ReconciliationProvider) *Handler {
	h.reconProvider = rp
	return h
}

// WithEscrowLister adds escrow listing capability to the dashboard.
func (h *Handler) WithEscrowLister(el EscrowLister) *Handler {
	h.escrowLister = el
	return h
}

// WithWorkflowLister adds workflow listing capability to the dashboard.
func (h *Handler) WithWorkflowLister(wl WorkflowLister) *Handler {
	h.workflowLister = wl
	return h
}

// WithStreamLister adds stream listing capability to the dashboard.
func (h *Handler) WithStreamLister(sl StreamLister) *Handler {
	h.streamLister = sl
	return h
}

// WithOfferLister adds offer listing capability to the dashboard.
func (h *Handler) WithOfferLister(ol OfferLister) *Handler {
	h.offerLister = ol
	return h
}

// checkOwnership verifies the caller owns the tenant or is an admin.
// Returns false (and sends 403) if the caller is not authorized.
func checkOwnership(c *gin.Context, tenantID string) bool {
	if auth.IsAdminRequest(c) {
		return true
	}
	if auth.GetTenantID(c) != tenantID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "not your tenant"})
		return false
	}
	return true
}

// RegisterRoutes sets up dashboard routes under the given group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/tenants/:id/dashboard/overview", h.Overview)
	r.GET("/tenants/:id/dashboard/usage", h.Usage)
	r.GET("/tenants/:id/dashboard/top-services", h.TopServices)
	r.GET("/tenants/:id/dashboard/denials", h.Denials)
	r.GET("/tenants/:id/dashboard/sessions", h.Sessions)
	r.GET("/tenants/:id/dashboard/escrows", h.Escrows)
	r.GET("/tenants/:id/dashboard/workflows", h.Workflows)
	r.GET("/tenants/:id/dashboard/streams", h.Streams)
	r.GET("/tenants/:id/dashboard/offers", h.Offers)
	r.GET("/tenants/:id/dashboard/health", h.Health)
}

// Health returns system health status including subsystem checks and
// reconciliation state.
func (h *Handler) Health(c *gin.Context) {
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}

	response := gin.H{}

	// Subsystem health
	if h.healthProvider != nil {
		statuses, overall := h.healthProvider.CheckAll()
		response["status"] = overall
		response["services"] = statuses
	} else {
		response["status"] = "unknown"
		response["services"] = []SubsystemStatus{}
	}

	// Reconciliation
	if h.reconProvider != nil {
		if snap := h.reconProvider.LastReport(); snap != nil {
			response["reconciliation"] = snap
		}
	}

	c.JSON(http.StatusOK, response)
}

// Overview returns billing summary + active session count + agent count.
func (h *Handler) Overview(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}

	t, err := h.tenantStore.Get(ctx, tenantID)
	if err != nil {
		if err == tenant.ErrTenantNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	billing, err := h.gwStore.GetBillingSummary(ctx, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	sessions, err := h.gwStore.ListSessionsByTenant(ctx, tenantID, 1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}
	activeSessions := 0
	for _, s := range sessions {
		if s.Status == gateway.StatusActive {
			activeSessions++
		}
	}

	agentCount, err3 := h.tenantStore.CountAgents(ctx, tenantID)
	if err3 != nil {
		agentCount = 0 // degrade gracefully
	}

	c.JSON(http.StatusOK, gin.H{
		"tenant": gin.H{
			"id":   t.ID,
			"name": t.Name,
			"plan": t.Plan,
		},
		"billing": gin.H{
			"totalRequests":   billing.TotalRequests,
			"settledRequests": billing.SettledRequests,
			"settledVolume":   billing.SettledVolume,
			"feesCollected":   billing.FeesCollected,
			"takeRateBps":     t.Settings.TakeRateBPS,
		},
		"activeSessions": activeSessions,
		"agentCount":     agentCount,
	})
}

// Usage returns time-series billing data.
func (h *Handler) Usage(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}

	interval := c.DefaultQuery("interval", "day")
	switch interval {
	case "hour", "day", "week":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_interval", "message": "must be hour, day, or week"})
		return
	}

	from, to := parseTimeRange(c)

	points, err := h.gwStore.GetBillingTimeSeries(ctx, tenantID, interval, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"interval": interval,
		"from":     from,
		"to":       to,
		"points":   points,
		"count":    len(points),
	})
}

// TopServices returns the most-used service types by volume.
func (h *Handler) TopServices(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}

	limit := parseLimit(c, 10, 100)

	services, err := h.gwStore.GetTopServiceTypes(ctx, tenantID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"services": services,
		"count":    len(services),
	})
}

// Denials returns recent policy-denied requests for compliance audit.
func (h *Handler) Denials(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}

	limit := parseLimit(c, 50, 500)

	denials, err := h.gwStore.GetPolicyDenials(ctx, tenantID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"denials": denials,
		"count":   len(denials),
	})
}

// Sessions returns sessions for a tenant, optionally filtered by status.
func (h *Handler) Sessions(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}

	limit := parseLimit(c, 50, 500)
	statusFilter := c.Query("status")

	var opts []gateway.ListOption
	if cursor := c.Query("cursor"); cursor != "" {
		opts = append(opts, gateway.WithCursor(cursor))
	}

	sessions, err := h.gwStore.ListSessionsByTenant(ctx, tenantID, limit+1, opts...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	if statusFilter != "" {
		filtered := make([]*gateway.Session, 0, len(sessions))
		for _, s := range sessions {
			if string(s.Status) == statusFilter {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	sessions, nextCursor, hasMore := pagination.ComputePage(sessions, limit, func(s *gateway.Session) (time.Time, string) {
		return s.CreatedAt, s.ID
	})

	resp := gin.H{"sessions": sessions, "count": len(sessions), "has_more": hasMore}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	c.JSON(http.StatusOK, resp)
}

func parseTimeRange(c *gin.Context) (from, to time.Time) {
	to = time.Now()
	from = to.AddDate(0, 0, -30) // default: last 30 days

	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	return
}

func parseLimit(c *gin.Context, defaultVal, maxVal int) int {
	limit := defaultVal
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxVal {
		limit = maxVal
	}
	return limit
}

// Escrows returns escrows for all agents belonging to a tenant.
func (h *Handler) Escrows(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}
	if h.escrowLister == nil {
		c.JSON(http.StatusOK, gin.H{"escrows": []interface{}{}, "count": 0, "has_more": false})
		return
	}

	limit := parseLimit(c, 50, 200)
	agents, err := h.tenantStore.ListAgents(ctx, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	var all []EscrowSummary
	perAgent := limit
	if len(agents) > 0 {
		perAgent = limit / len(agents)
		if perAgent < 10 {
			perAgent = 10
		}
	}
	for _, addr := range agents {
		escrows, err := h.escrowLister.ListByAgent(ctx, addr, perAgent)
		if err != nil {
			continue
		}
		all = append(all, escrows...)
	}

	// Sort by creation time descending
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})

	if len(all) > limit {
		all = all[:limit]
	}

	c.JSON(http.StatusOK, gin.H{"escrows": all, "count": len(all), "has_more": false})
}

// Workflows returns workflows for all agents belonging to a tenant.
func (h *Handler) Workflows(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}
	if h.workflowLister == nil {
		c.JSON(http.StatusOK, gin.H{"workflows": []interface{}{}, "count": 0})
		return
	}

	limit := parseLimit(c, 50, 200)
	agents, err := h.tenantStore.ListAgents(ctx, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	var all []WorkflowSummary
	perAgent := limit
	if len(agents) > 0 {
		perAgent = limit / len(agents)
		if perAgent < 10 {
			perAgent = 10
		}
	}
	for _, addr := range agents {
		workflows, err := h.workflowLister.ListByAgent(ctx, addr, perAgent)
		if err != nil {
			continue
		}
		all = append(all, workflows...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})

	if len(all) > limit {
		all = all[:limit]
	}

	c.JSON(http.StatusOK, gin.H{"workflows": all, "count": len(all)})
}

// Streams returns streams for all agents belonging to a tenant.
func (h *Handler) Streams(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}
	if h.streamLister == nil {
		c.JSON(http.StatusOK, gin.H{"streams": []interface{}{}, "count": 0})
		return
	}

	limit := parseLimit(c, 50, 200)
	agents, err := h.tenantStore.ListAgents(ctx, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	var all []StreamSummary
	perAgent := limit
	if len(agents) > 0 {
		perAgent = limit / len(agents)
		if perAgent < 10 {
			perAgent = 10
		}
	}
	for _, addr := range agents {
		streams, err := h.streamLister.ListByAgent(ctx, addr, perAgent)
		if err != nil {
			continue
		}
		all = append(all, streams...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})

	if len(all) > limit {
		all = all[:limit]
	}

	c.JSON(http.StatusOK, gin.H{"streams": all, "count": len(all)})
}

// Offers returns active marketplace offers. Not tenant-scoped since the marketplace is global.
func (h *Handler) Offers(c *gin.Context) {
	tenantID := c.Param("id")
	if !checkOwnership(c, tenantID) {
		return
	}
	if h.offerLister == nil {
		c.JSON(http.StatusOK, gin.H{"offers": []interface{}{}, "count": 0})
		return
	}

	limit := parseLimit(c, 50, 200)
	serviceType := c.Query("serviceType")

	offers, err := h.offerLister.ListActive(c.Request.Context(), serviceType, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"offers": offers, "count": len(offers)})
}
