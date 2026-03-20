package chargeback

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
)

// Handler provides HTTP endpoints for chargeback operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new chargeback handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up read-only chargeback routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/chargeback/cost-centers", h.ListCostCenters)
	r.GET("/chargeback/cost-centers/:id", h.GetCostCenter)
	r.GET("/chargeback/cost-centers/:id/spend", h.ListSpend)
	r.GET("/chargeback/reports", h.GenerateReport)
}

// RegisterProtectedRoutes sets up auth-required chargeback routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/chargeback/cost-centers", h.CreateCostCenter)
	r.PUT("/chargeback/cost-centers/:id", h.UpdateCostCenter)
	r.POST("/chargeback/spend", h.RecordSpend)
}

// callerTenantID extracts the authenticated caller's tenant ID.
// Returns empty string + 403 if not available.
func callerTenantID(c *gin.Context) (string, bool) {
	tid := auth.GetTenantID(c)
	if tid == "" && !auth.IsAdminRequest(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "tenant context required"})
		return "", false
	}
	return tid, true
}

// CreateCostCenter handles POST /v1/chargeback/cost-centers
func (h *Handler) CreateCostCenter(c *gin.Context) {
	tenantID, ok := callerTenantID(c)
	if !ok {
		return
	}

	var req struct {
		Name          string `json:"name" binding:"required"`
		Department    string `json:"department" binding:"required"`
		ProjectCode   string `json:"projectCode"`
		MonthlyBudget string `json:"monthlyBudget" binding:"required"`
		WarnAtPercent int    `json:"warnAtPercent"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	if req.WarnAtPercent <= 0 {
		req.WarnAtPercent = 80
	}

	cc, err := h.service.CreateCostCenter(c.Request.Context(),
		tenantID, req.Name, req.Department, req.ProjectCode, req.MonthlyBudget, req.WarnAtPercent)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"costCenter": cc})
}

// GetCostCenter handles GET /v1/chargeback/cost-centers/:id
func (h *Handler) GetCostCenter(c *gin.Context) {
	tenantID, ok := callerTenantID(c)
	if !ok {
		return
	}

	cc, err := h.service.store.GetCostCenter(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	// Enforce tenant isolation
	if cc.TenantID != tenantID && !auth.IsAdminRequest(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"costCenter": cc})
}

// ListCostCenters handles GET /v1/chargeback/cost-centers
func (h *Handler) ListCostCenters(c *gin.Context) {
	tenantID, ok := callerTenantID(c)
	if !ok {
		return
	}

	centers, err := h.service.store.ListCostCenters(c.Request.Context(), tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"costCenters": centers, "count": len(centers)})
}

// UpdateCostCenter handles PUT /v1/chargeback/cost-centers/:id
func (h *Handler) UpdateCostCenter(c *gin.Context) {
	tenantID, ok := callerTenantID(c)
	if !ok {
		return
	}

	cc, err := h.service.store.GetCostCenter(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	if cc.TenantID != tenantID && !auth.IsAdminRequest(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req struct {
		Name          *string `json:"name"`
		MonthlyBudget *string `json:"monthlyBudget"`
		WarnAtPercent *int    `json:"warnAtPercent"`
		Active        *bool   `json:"active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	if req.Name != nil {
		cc.Name = *req.Name
	}
	if req.MonthlyBudget != nil {
		cc.MonthlyBudget = *req.MonthlyBudget
	}
	if req.WarnAtPercent != nil {
		cc.WarnAtPercent = *req.WarnAtPercent
	}
	if req.Active != nil {
		cc.Active = *req.Active
	}
	if err := h.service.store.UpdateCostCenter(c.Request.Context(), cc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"costCenter": cc})
}

// RecordSpend handles POST /v1/chargeback/spend
func (h *Handler) RecordSpend(c *gin.Context) {
	tenantID, ok := callerTenantID(c)
	if !ok {
		return
	}

	var req struct {
		CostCenterID string `json:"costCenterId" binding:"required"`
		AgentAddr    string `json:"agentAddr" binding:"required"`
		Amount       string `json:"amount" binding:"required"`
		ServiceType  string `json:"serviceType" binding:"required"`
		WorkflowID   string `json:"workflowId"`
		SessionID    string `json:"sessionId"`
		EscrowID     string `json:"escrowId"`
		Description  string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	entry, err := h.service.RecordSpend(c.Request.Context(),
		req.CostCenterID, tenantID, req.AgentAddr, req.Amount, req.ServiceType,
		SpendOpts{
			WorkflowID:  req.WorkflowID,
			SessionID:   req.SessionID,
			EscrowID:    req.EscrowID,
			Description: req.Description,
		})
	if err != nil {
		status := http.StatusInternalServerError
		msg := "Internal server error"
		if errors.Is(err, ErrBudgetExceeded) {
			status = http.StatusConflict
			msg = "Cost center budget exceeded"
		} else if errors.Is(err, ErrCostCenterNotFound) {
			status = http.StatusNotFound
			msg = "Cost center not found"
		}
		c.JSON(status, gin.H{"error": msg})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"entry": entry})
}

// ListSpend handles GET /v1/chargeback/cost-centers/:id/spend?from=...&to=...
func (h *Handler) ListSpend(c *gin.Context) {
	tenantID, ok := callerTenantID(c)
	if !ok {
		return
	}

	// Verify the cost center belongs to this tenant
	cc, err := h.service.store.GetCostCenter(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	if cc.TenantID != tenantID && !auth.IsAdminRequest(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	from := time.Now().AddDate(0, 0, -30)
	to := time.Now()
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

	entries, err := h.service.store.GetSpendForPeriod(c.Request.Context(), c.Param("id"), from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "count": len(entries)})
}

// GenerateReport handles GET /v1/chargeback/reports?year=2026&month=3
func (h *Handler) GenerateReport(c *gin.Context) {
	tenantID, ok := callerTenantID(c)
	if !ok {
		return
	}

	now := time.Now()
	year := now.Year()
	month := now.Month()

	if v := c.Query("year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			year = n
		}
	}
	if v := c.Query("month"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 12 {
			month = time.Month(n)
		}
	}

	report, err := h.service.GenerateReport(c.Request.Context(), tenantID, year, month)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"report": report})
}
