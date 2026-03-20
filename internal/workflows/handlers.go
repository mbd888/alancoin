package workflows

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/validation"
)

// Handler provides HTTP endpoints for workflow operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new workflow handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up read-only workflow routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/workflows/:id", h.GetWorkflow)
	r.GET("/workflows/:id/costs", h.GetCostReport)
	r.GET("/workflows/:id/audit", h.GetAuditTrail)
	r.GET("/agents/:address/workflows", h.ListWorkflows)
}

// RegisterProtectedRoutes sets up auth-required workflow routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/workflows", h.CreateWorkflow)
	r.POST("/workflows/:id/steps/:step/start", h.StartStep)
	r.POST("/workflows/:id/steps/:step/complete", h.CompleteStep)
	r.POST("/workflows/:id/steps/:step/fail", h.FailStep)
	r.POST("/workflows/:id/abort", h.AbortWorkflow)
}

// CreateWorkflow handles POST /v1/workflows
func (h *Handler) CreateWorkflow(c *gin.Context) {
	var req CreateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "name, budgetTotal, and steps are required",
		})
		return
	}

	if errs := validation.Validate(
		validation.ValidAmount("budget_total", req.BudgetTotal),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": errs.Error(),
			"details": errs,
		})
		return
	}

	if len(req.Steps) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": "at least one step is required",
		})
		return
	}

	callerAddr := c.GetString("authAgentAddr")
	wf, err := h.service.Create(c.Request.Context(), callerAddr, req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrInvalidAmount) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": "workflow_failed", "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"workflow": wf})
}

// GetWorkflow handles GET /v1/workflows/:id
func (h *Handler) GetWorkflow(c *gin.Context) {
	wf, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, ErrWorkflowNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Workflow not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"workflow": wf})
}

// GetCostReport handles GET /v1/workflows/:id/costs
func (h *Handler) GetCostReport(c *gin.Context) {
	report, err := h.service.GetCostReport(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, ErrWorkflowNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Workflow not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"costReport": report})
}

// GetAuditTrail handles GET /v1/workflows/:id/audit
func (h *Handler) GetAuditTrail(c *gin.Context) {
	trail, err := h.service.GetAuditTrail(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, ErrWorkflowNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Workflow not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"auditTrail": trail})
}

// ListWorkflows handles GET /v1/agents/:address/workflows
func (h *Handler) ListWorkflows(c *gin.Context) {
	address := c.Param("address")
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}

	workflows, err := h.service.ListByOwner(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"workflows": workflows, "count": len(workflows)})
}

// CompleteStepRequest is the body for completing a step.
type CompleteStepRequest struct {
	ActualCost string `json:"actualCost" binding:"required"`
}

// StartStep handles POST /v1/workflows/:id/steps/:step/start
func (h *Handler) StartStep(c *gin.Context) {
	wfID := c.Param("id")
	stepName := c.Param("step")
	callerAddr := c.GetString("authAgentAddr")

	wf, err := h.service.StartStep(c.Request.Context(), wfID, stepName, callerAddr)
	if err != nil {
		c.JSON(h.errStatus(err), gin.H{"error": h.errCode(err), "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"workflow": wf})
}

// CompleteStep handles POST /v1/workflows/:id/steps/:step/complete
func (h *Handler) CompleteStep(c *gin.Context) {
	wfID := c.Param("id")
	stepName := c.Param("step")
	callerAddr := c.GetString("authAgentAddr")

	var req CompleteStepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "actualCost is required",
		})
		return
	}

	wf, err := h.service.CompleteStep(c.Request.Context(), wfID, stepName, callerAddr, req.ActualCost)
	if err != nil {
		c.JSON(h.errStatus(err), gin.H{"error": h.errCode(err), "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"workflow": wf})
}

// FailStepRequest is the body for failing a step.
type FailStepRequest struct {
	Reason string `json:"reason"`
}

// FailStep handles POST /v1/workflows/:id/steps/:step/fail
func (h *Handler) FailStep(c *gin.Context) {
	wfID := c.Param("id")
	stepName := c.Param("step")
	callerAddr := c.GetString("authAgentAddr")

	var req FailStepRequest
	_ = c.ShouldBindJSON(&req) // reason is optional

	wf, err := h.service.FailStep(c.Request.Context(), wfID, stepName, callerAddr, req.Reason)
	if err != nil {
		c.JSON(h.errStatus(err), gin.H{"error": h.errCode(err), "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"workflow": wf})
}

// AbortWorkflow handles POST /v1/workflows/:id/abort
func (h *Handler) AbortWorkflow(c *gin.Context) {
	wfID := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	wf, err := h.service.Abort(c.Request.Context(), wfID, callerAddr)
	if err != nil {
		c.JSON(h.errStatus(err), gin.H{"error": h.errCode(err), "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"workflow": wf})
}

func (h *Handler) errStatus(err error) int {
	switch {
	case errors.Is(err, ErrWorkflowNotFound), errors.Is(err, ErrStepNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrUnauthorized):
		return http.StatusForbidden
	case errors.Is(err, ErrWorkflowCompleted), errors.Is(err, ErrStepAlreadyStarted),
		errors.Is(err, ErrStepAlreadyDone), errors.Is(err, ErrWorkflowAborted):
		return http.StatusConflict
	case errors.Is(err, ErrBudgetExceeded), errors.Is(err, ErrStepBudgetExceeded),
		errors.Is(err, ErrVelocityBreaker), errors.Is(err, ErrInvalidAmount),
		errors.Is(err, ErrStepNotStarted):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func (h *Handler) errCode(err error) string {
	switch {
	case errors.Is(err, ErrWorkflowNotFound), errors.Is(err, ErrStepNotFound):
		return "not_found"
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, ErrBudgetExceeded):
		return "budget_exceeded"
	case errors.Is(err, ErrStepBudgetExceeded):
		return "step_budget_exceeded"
	case errors.Is(err, ErrVelocityBreaker):
		return "circuit_broken"
	case errors.Is(err, ErrWorkflowCompleted), errors.Is(err, ErrWorkflowAborted):
		return "already_closed"
	case errors.Is(err, ErrStepAlreadyStarted), errors.Is(err, ErrStepAlreadyDone):
		return "invalid_state"
	default:
		return "internal_error"
	}
}
