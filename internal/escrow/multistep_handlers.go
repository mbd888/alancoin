package escrow

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/validation"
)

// MultiStepHandler provides HTTP endpoints for multistep escrow operations.
type MultiStepHandler struct {
	service *MultiStepService
}

// NewMultiStepHandler creates a new multistep escrow handler.
func NewMultiStepHandler(service *MultiStepService) *MultiStepHandler {
	return &MultiStepHandler{service: service}
}

// RegisterRoutes sets up public (read-only) multistep escrow routes.
func (h *MultiStepHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/escrow/multistep/:id", h.GetMultiStepEscrow)
}

// RegisterProtectedRoutes sets up protected (auth-required) multistep escrow routes.
func (h *MultiStepHandler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/escrow/multistep", h.CreateMultiStepEscrow)
	r.POST("/escrow/multistep/:id/confirm-step", h.ConfirmStep)
	r.POST("/escrow/multistep/:id/refund", h.RefundRemaining)
}

// PlannedStepRequest describes an expected step in the pipeline.
type PlannedStepRequest struct {
	SellerAddr string `json:"sellerAddr" binding:"required"`
	Amount     string `json:"amount" binding:"required"`
}

// CreateMultiStepRequest is the request body for creating a multistep escrow.
type CreateMultiStepRequest struct {
	TotalAmount  string               `json:"totalAmount" binding:"required"`
	TotalSteps   int                  `json:"totalSteps" binding:"required"`
	PlannedSteps []PlannedStepRequest `json:"plannedSteps" binding:"required"`
}

// ConfirmStepRequest is the request body for confirming a step.
type ConfirmStepRequest struct {
	StepIndex  int    `json:"stepIndex" binding:"min=0"`
	SellerAddr string `json:"sellerAddr" binding:"required"`
	Amount     string `json:"amount" binding:"required"`
}

// CreateMultiStepEscrow handles POST /v1/escrow/multistep
func (h *MultiStepHandler) CreateMultiStepEscrow(c *gin.Context) {
	var req CreateMultiStepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body: totalAmount, totalSteps, and plannedSteps are required",
		})
		return
	}

	if errs := validation.Validate(
		validation.ValidAmount("total_amount", req.TotalAmount),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": errs.Error(),
			"details": errs,
		})
		return
	}

	if req.TotalSteps <= 0 || req.TotalSteps > MaxTotalSteps {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": fmt.Sprintf("totalSteps must be between 1 and %d", MaxTotalSteps),
		})
		return
	}

	if len(req.PlannedSteps) != req.TotalSteps {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": fmt.Sprintf("plannedSteps length (%d) must match totalSteps (%d)", len(req.PlannedSteps), req.TotalSteps),
		})
		return
	}

	// Convert request planned steps to domain type
	planned := make([]PlannedStep, len(req.PlannedSteps))
	for i, ps := range req.PlannedSteps {
		planned[i] = PlannedStep(ps)
	}

	callerAddr := c.GetString("authAgentAddr")

	mse, err := h.service.LockSteps(c.Request.Context(), callerAddr, req.TotalAmount, req.TotalSteps, planned)
	if err != nil {
		status := http.StatusInternalServerError
		code := "escrow_failed"
		if errors.Is(err, ErrInvalidAmount) {
			status = http.StatusBadRequest
			code = "validation_error"
		}
		c.JSON(status, gin.H{
			"error":   code,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"escrow": mse})
}

// ConfirmStep handles POST /v1/escrow/multistep/:id/confirm-step
func (h *MultiStepHandler) ConfirmStep(c *gin.Context) {
	id := c.Param("id")

	var req ConfirmStepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	if req.StepIndex < 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": "stepIndex must be non-negative",
		})
		return
	}

	if errs := validation.Validate(
		validation.ValidAddress("seller_addr", req.SellerAddr),
		validation.ValidAmount("amount", req.Amount),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": errs.Error(),
			"details": errs,
		})
		return
	}

	// Verify caller is the buyer
	callerAddr := c.GetString("authAgentAddr")
	mse, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrMultiStepNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "not_found", "message": err.Error()})
		return
	}
	if !strings.EqualFold(callerAddr, mse.BuyerAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Only the buyer can confirm steps",
		})
		return
	}

	mse, err = h.service.ConfirmStep(c.Request.Context(), id, req.StepIndex, req.SellerAddr, req.Amount)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrMultiStepNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrDuplicateStep):
			status = http.StatusConflict
			code = "duplicate_step"
		case errors.Is(err, ErrStepOutOfRange):
			status = http.StatusBadRequest
			code = "step_out_of_range"
		case errors.Is(err, ErrAmountExceedsTotal):
			status = http.StatusBadRequest
			code = "amount_exceeds_total"
		case errors.Is(err, ErrStepMismatch):
			status = http.StatusForbidden
			code = "step_mismatch"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_state"
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"escrow": mse})
}

// RefundRemaining handles POST /v1/escrow/multistep/:id/refund
func (h *MultiStepHandler) RefundRemaining(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	mse, err := h.service.RefundRemaining(c.Request.Context(), id, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrMultiStepNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_state"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"escrow": mse})
}

// GetMultiStepEscrow handles GET /v1/escrow/multistep/:id
func (h *MultiStepHandler) GetMultiStepEscrow(c *gin.Context) {
	id := c.Param("id")

	mse, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrMultiStepNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Multistep escrow not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"escrow": mse})
}
