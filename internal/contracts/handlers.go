package contracts

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for behavioral contract operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new contract handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up read-only contract routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/contracts/:id", h.GetContract)
	r.GET("/contracts/:id/audit-trail", h.GetAuditTrail)
}

// RegisterProtectedRoutes sets up auth-required contract routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/contracts", h.CreateContract)
	r.POST("/contracts/:id/bind", h.BindToEscrow)
	r.POST("/contracts/:id/check", h.CheckInvariant)
	r.POST("/contracts/:id/pass", h.MarkPassed)
}

// CreateContract handles POST /v1/contracts
func (h *Handler) CreateContract(c *gin.Context) {
	var req CreateContractRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "name, invariants, and recovery are required",
		})
		return
	}

	contract, err := h.service.Create(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "contract_failed"
		if errors.Is(err, ErrNoConditions) {
			status = http.StatusBadRequest
			code = "validation_error"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
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

// BindRequest is the request body for binding a contract to an escrow.
type BindRequest struct {
	EscrowID string `json:"escrowId" binding:"required"`
}

// BindToEscrow handles POST /v1/contracts/:id/bind
func (h *Handler) BindToEscrow(c *gin.Context) {
	id := c.Param("id")

	var req BindRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "escrowId is required",
		})
		return
	}

	contract, err := h.service.BindToEscrow(c.Request.Context(), id, req.EscrowID)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrContractNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrContractAlreadyBound):
			status = http.StatusConflict
			code = "already_bound"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"contract": contract})
}

// CheckInvariant handles POST /v1/contracts/:id/check
func (h *Handler) CheckInvariant(c *gin.Context) {
	id := c.Param("id")

	var req CheckInvariantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "memberAddr is required",
		})
		return
	}

	contract, err := h.service.CheckInvariant(c.Request.Context(), id, req)
	if err != nil {
		if errors.Is(err, ErrContractViolation) {
			// Return the contract with violation details — this is not an error
			// from the caller's perspective, it's a circuit break signal
			c.JSON(http.StatusOK, gin.H{
				"contract": contract,
				"violated": true,
				"action":   contract.Recovery,
			})
			return
		}
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

	c.JSON(http.StatusOK, gin.H{
		"contract": contract,
		"violated": false,
	})
}

// MarkPassed handles POST /v1/contracts/:id/pass
func (h *Handler) MarkPassed(c *gin.Context) {
	id := c.Param("id")

	contract, err := h.service.MarkPassed(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrContractNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Contract not found",
			})
			return
		}
		c.JSON(http.StatusConflict, gin.H{
			"error":   "invalid_state",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"contract": contract})
}

// GetAuditTrail handles GET /v1/contracts/:id/audit-trail
func (h *Handler) GetAuditTrail(c *gin.Context) {
	id := c.Param("id")

	trail, err := h.service.GetAuditTrail(c.Request.Context(), id)
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

	c.JSON(http.StatusOK, gin.H{"audit_trail": trail})
}
