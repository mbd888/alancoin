package contracts

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/validation"
)

// Handler provides HTTP endpoints for contract operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new contract handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up public (read-only) contract routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/contracts/:id", h.GetContract)
	r.GET("/contracts/:id/calls", h.ListCalls)
	r.GET("/agents/:address/contracts", h.ListContracts)
	r.GET("/contracts/active", h.ListActiveContracts)
}

// RegisterProtectedRoutes sets up protected (auth-required) contract routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/contracts", h.ProposeContract)
	r.POST("/contracts/:id/accept", h.AcceptContract)
	r.POST("/contracts/:id/reject", h.RejectContract)
	r.POST("/contracts/:id/call", h.RecordCall)
	r.POST("/contracts/:id/terminate", h.TerminateContract)
}

// ProposeContract handles POST /v1/contracts
func (h *Handler) ProposeContract(c *gin.Context) {
	var req ProposeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	if errs := validation.Validate(
		validation.ValidAddress("buyer_addr", req.BuyerAddr),
		validation.ValidAddress("seller_addr", req.SellerAddr),
		validation.ValidAmount("price_per_call", req.PricePerCall),
		validation.ValidAmount("buyer_budget", req.BuyerBudget),
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

	contract, err := h.service.Propose(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "contract_failed",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"contract": contract})
}

// GetContract handles GET /v1/contracts/:id
func (h *Handler) GetContract(c *gin.Context) {
	id := c.Param("id")

	contract, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrContractNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Contract not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"contract": contract})
}

// ListCalls handles GET /v1/contracts/:id/calls
func (h *Handler) ListCalls(c *gin.Context) {
	id := c.Param("id")
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}

	calls, err := h.service.ListCalls(c.Request.Context(), id, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"calls": calls,
		"count": len(calls),
	})
}

// ListContracts handles GET /v1/agents/:address/contracts
func (h *Handler) ListContracts(c *gin.Context) {
	address := c.Param("address")
	status := c.Query("status")
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}

	contracts, err := h.service.ListByAgent(c.Request.Context(), address, status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"contracts": contracts,
		"count":     len(contracts),
	})
}

// ListActiveContracts handles GET /v1/contracts/active
func (h *Handler) ListActiveContracts(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}

	contracts, err := h.service.ListActive(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"contracts": contracts,
		"count":     len(contracts),
	})
}

// AcceptContract handles POST /v1/contracts/:id/accept
func (h *Handler) AcceptContract(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	contract, err := h.service.Accept(c.Request.Context(), id, callerAddr)
	if err != nil {
		h.mapError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"contract": contract})
}

// RejectContract handles POST /v1/contracts/:id/reject
func (h *Handler) RejectContract(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	contract, err := h.service.Reject(c.Request.Context(), id, callerAddr)
	if err != nil {
		h.mapError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"contract": contract})
}

// RecordCall handles POST /v1/contracts/:id/call
func (h *Handler) RecordCall(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	var req RecordCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	if req.Status != "success" && req.Status != "failed" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Status must be 'success' or 'failed'",
		})
		return
	}

	contract, err := h.service.RecordCall(c.Request.Context(), id, req, callerAddr)
	if err != nil {
		h.mapError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"contract": contract})
}

// TerminateContract handles POST /v1/contracts/:id/terminate
func (h *Handler) TerminateContract(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	var req TerminateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Reason is required",
		})
		return
	}

	contract, err := h.service.Terminate(c.Request.Context(), id, callerAddr, req.Reason)
	if err != nil {
		h.mapError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"contract": contract})
}

// mapError maps service errors to HTTP responses.
func (h *Handler) mapError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, ErrContractNotFound):
		status = http.StatusNotFound
		code = "not_found"
	case errors.Is(err, ErrUnauthorized):
		status = http.StatusForbidden
		code = "unauthorized"
	case errors.Is(err, ErrAlreadyResolved), errors.Is(err, ErrInvalidStatus):
		status = http.StatusConflict
		code = "invalid_state"
	case errors.Is(err, ErrBudgetExhausted):
		status = http.StatusConflict
		code = "budget_exhausted"
	case errors.Is(err, ErrSLAViolation):
		status = http.StatusConflict
		code = "sla_violation"
	}
	c.JSON(status, gin.H{"error": code, "message": err.Error()})
}
