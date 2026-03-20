package arbitration

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for arbitration operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new arbitration handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up read-only arbitration routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/arbitration/cases/:id", h.GetCase)
	r.GET("/arbitration/cases", h.ListOpen)
	r.GET("/arbitration/escrows/:escrowId/cases", h.ListByEscrow)
}

// RegisterProtectedRoutes sets up auth-required arbitration routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/arbitration/cases", h.FileCase)
	r.POST("/arbitration/cases/:id/auto-resolve", h.AutoResolve)
	r.POST("/arbitration/cases/:id/assign", h.AssignArbiter)
	r.POST("/arbitration/cases/:id/evidence", h.SubmitEvidence)
	r.POST("/arbitration/cases/:id/resolve", h.Resolve)
}

// FileCase handles POST /v1/arbitration/cases
func (h *Handler) FileCase(c *gin.Context) {
	callerAddr := c.GetString("authAgentAddr")

	var req struct {
		EscrowID   string `json:"escrowId" binding:"required"`
		BuyerAddr  string `json:"buyerAddr" binding:"required"`
		SellerAddr string `json:"sellerAddr" binding:"required"`
		Amount     string `json:"amount" binding:"required"`
		Reason     string `json:"reason" binding:"required"`
		ContractID string `json:"contractId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	// Only the buyer or seller in the escrow can file a case
	if callerAddr != req.BuyerAddr && callerAddr != req.SellerAddr {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "only the buyer or seller can file an arbitration case"})
		return
	}

	arbCase, err := h.service.FileCase(c.Request.Context(),
		req.EscrowID, req.BuyerAddr, req.SellerAddr, req.Amount, req.Reason, req.ContractID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"case": arbCase})
}

// GetCase handles GET /v1/arbitration/cases/:id
func (h *Handler) GetCase(c *gin.Context) {
	arbCase, err := h.service.store.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"case": arbCase})
}

// AutoResolve handles POST /v1/arbitration/cases/:id/auto-resolve
func (h *Handler) AutoResolve(c *gin.Context) {
	var req struct {
		ContractPassed bool `json:"contractPassed"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	resolved, err := h.service.AutoResolve(c.Request.Context(), c.Param("id"), req.ContractPassed)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrCaseNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, ErrCaseAlreadyClosed) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": "operation failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"resolved": resolved})
}

// AssignArbiter handles POST /v1/arbitration/cases/:id/assign
func (h *Handler) AssignArbiter(c *gin.Context) {
	var req struct {
		ArbiterAddr string `json:"arbiterAddr" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	if err := h.service.AssignArbiter(c.Request.Context(), c.Param("id"), req.ArbiterAddr); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrCaseNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, ErrCaseAlreadyClosed) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": "operation failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "assigned"})
}

// SubmitEvidence handles POST /v1/arbitration/cases/:id/evidence
func (h *Handler) SubmitEvidence(c *gin.Context) {
	var req struct {
		SubmittedBy string `json:"submittedBy" binding:"required"`
		Role        string `json:"role" binding:"required"`
		Type        string `json:"type" binding:"required"`
		Content     string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	ev, err := h.service.SubmitEvidence(c.Request.Context(), c.Param("id"),
		req.SubmittedBy, req.Role, req.Type, req.Content)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrCaseNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, ErrCaseAlreadyClosed) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": "operation failed"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"evidence": ev})
}

// Resolve handles POST /v1/arbitration/cases/:id/resolve
func (h *Handler) Resolve(c *gin.Context) {
	var req struct {
		ArbiterAddr string  `json:"arbiterAddr" binding:"required"`
		Outcome     Outcome `json:"outcome" binding:"required"`
		SplitPct    int     `json:"splitPct"`
		Decision    string  `json:"decision" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	if err := h.service.Resolve(c.Request.Context(), c.Param("id"),
		req.ArbiterAddr, req.Outcome, req.SplitPct, req.Decision); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrCaseNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, ErrCaseAlreadyClosed) {
			status = http.StatusConflict
		} else if errors.Is(err, ErrNotArbiter) {
			status = http.StatusForbidden
		} else if errors.Is(err, ErrInvalidOutcome) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": "operation failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "resolved"})
}

// ListOpen handles GET /v1/arbitration/cases?limit=50
func (h *Handler) ListOpen(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	cases, err := h.service.store.ListOpen(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"cases": cases, "count": len(cases)})
}

// ListByEscrow handles GET /v1/arbitration/escrows/:escrowId/cases
func (h *Handler) ListByEscrow(c *gin.Context) {
	cases, err := h.service.store.ListByEscrow(c.Request.Context(), c.Param("escrowId"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"cases": cases, "count": len(cases)})
}
