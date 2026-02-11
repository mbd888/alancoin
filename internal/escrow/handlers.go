package escrow

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/validation"
)

// Handler provides HTTP endpoints for escrow operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new escrow handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up public (read-only) escrow routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/escrow/:id", h.GetEscrow)
	r.GET("/agents/:address/escrows", h.ListEscrows)
}

// RegisterProtectedRoutes sets up protected (auth-required) escrow routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/escrow", h.CreateEscrow)
	r.POST("/escrow/:id/deliver", h.MarkDelivered)
	r.POST("/escrow/:id/confirm", h.ConfirmEscrow)
	r.POST("/escrow/:id/dispute", h.DisputeEscrow)
	r.POST("/escrow/:id/evidence", h.SubmitEvidence)
	r.POST("/escrow/:id/arbitrate", h.AssignArbitrator)
	r.POST("/escrow/:id/resolve", h.ResolveArbitration)
}

// CreateEscrow handles POST /v1/escrow
func (h *Handler) CreateEscrow(c *gin.Context) {
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Validate address fields
	if errs := validation.Validate(
		validation.ValidAddress("buyer_addr", req.BuyerAddr),
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

	// Verify the authenticated agent is the buyer
	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.BuyerAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must be the buyer",
		})
		return
	}

	escrow, err := h.service.Create(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "escrow_failed",
			"message": "Failed to create escrow",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"escrow": escrow})
}

// GetEscrow handles GET /v1/escrow/:id
func (h *Handler) GetEscrow(c *gin.Context) {
	id := c.Param("id")

	escrow, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrEscrowNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Escrow not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"escrow": escrow})
}

// ListEscrows handles GET /v1/agents/:address/escrows
func (h *Handler) ListEscrows(c *gin.Context) {
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

	escrows, err := h.service.ListByAgent(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"escrows": escrows,
		"count":   len(escrows),
	})
}

// MarkDelivered handles POST /v1/escrow/:id/deliver
func (h *Handler) MarkDelivered(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr") // Set by auth middleware

	escrow, err := h.service.MarkDelivered(c.Request.Context(), id, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrEscrowNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrAlreadyResolved), errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_state"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"escrow": escrow})
}

// ConfirmEscrow handles POST /v1/escrow/:id/confirm
func (h *Handler) ConfirmEscrow(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	escrow, err := h.service.Confirm(c.Request.Context(), id, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrEscrowNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrAlreadyResolved), errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_state"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"escrow": escrow})
}

// DisputeEscrow handles POST /v1/escrow/:id/dispute
func (h *Handler) DisputeEscrow(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	var req DisputeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Reason is required",
		})
		return
	}

	escrow, err := h.service.Dispute(c.Request.Context(), id, callerAddr, req.Reason)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrEscrowNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrAlreadyResolved), errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_state"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"escrow": escrow})
}

// SubmitEvidence handles POST /v1/escrow/:id/evidence
func (h *Handler) SubmitEvidence(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	var req EvidenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Content is required",
		})
		return
	}

	escrow, err := h.service.SubmitEvidence(c.Request.Context(), id, callerAddr, req.Content)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrEscrowNotFound):
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

	c.JSON(http.StatusOK, gin.H{"escrow": escrow})
}

// AssignArbitrator handles POST /v1/escrow/:id/arbitrate
func (h *Handler) AssignArbitrator(c *gin.Context) {
	id := c.Param("id")

	var req ArbitrateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "arbitratorAddr is required",
		})
		return
	}

	if errs := validation.Validate(
		validation.ValidAddress("arbitrator_addr", req.ArbitratorAddr),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": errs.Error(),
		})
		return
	}

	escrow, err := h.service.AssignArbitrator(c.Request.Context(), id, req.ArbitratorAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrEscrowNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_state"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"escrow": escrow})
}

// ResolveArbitration handles POST /v1/escrow/:id/resolve
func (h *Handler) ResolveArbitration(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	var req ResolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "resolution is required (release, refund, or partial)",
		})
		return
	}

	escrow, err := h.service.ResolveArbitration(c.Request.Context(), id, callerAddr, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrEscrowNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
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

	c.JSON(http.StatusOK, gin.H{"escrow": escrow})
}
