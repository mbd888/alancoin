package escrow

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/validation"
)

// CoalitionHandler provides HTTP endpoints for coalition escrow operations.
type CoalitionHandler struct {
	service *CoalitionService
}

// NewCoalitionHandler creates a new coalition escrow handler.
func NewCoalitionHandler(service *CoalitionService) *CoalitionHandler {
	return &CoalitionHandler{service: service}
}

// RegisterRoutes sets up public (read-only) coalition escrow routes.
func (h *CoalitionHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/escrow/coalition/:id", h.GetCoalition)
	r.GET("/agents/:address/coalitions", h.ListCoalitions)
}

// RegisterProtectedRoutes sets up protected (auth-required) coalition escrow routes.
func (h *CoalitionHandler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/escrow/coalition", h.CreateCoalition)
	r.POST("/escrow/coalition/:id/complete", h.ReportCompletion)
	r.POST("/escrow/coalition/:id/oracle-report", h.OracleReport)
	r.POST("/escrow/coalition/:id/abort", h.AbortCoalition)
}

// CreateCoalition handles POST /v1/escrow/coalition
func (h *CoalitionHandler) CreateCoalition(c *gin.Context) {
	var req CreateCoalitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body: buyerAddr, oracleAddr, totalAmount, splitStrategy, members, and qualityTiers are required",
		})
		return
	}

	// Validate address fields
	validators := []func() *validation.ValidationError{
		validation.ValidAddress("buyer_addr", req.BuyerAddr),
		validation.ValidAddress("oracle_addr", req.OracleAddr),
		validation.ValidAmount("total_amount", req.TotalAmount),
	}
	for i, m := range req.Members {
		validators = append(validators, validation.ValidAddress(
			"members["+strconv.Itoa(i)+"].agent_addr", m.AgentAddr,
		))
	}
	if errs := validation.Validate(validators...); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": errs.Error(),
			"details": errs,
		})
		return
	}

	// Validate optional ContractID format if provided
	if req.ContractID != "" {
		if len(req.ContractID) < 4 || len(req.ContractID) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "validation_error",
				"message": "contractId must be between 4 and 100 characters",
			})
			return
		}
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

	ce, err := h.service.Create(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "coalition_failed"
		switch {
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
		case errors.Is(err, ErrInvalidSplitStrategy):
			status = http.StatusBadRequest
			code = "invalid_split_strategy"
		case errors.Is(err, ErrNoMembers), errors.Is(err, ErrNoQualityTiers),
			errors.Is(err, ErrDuplicateMember), errors.Is(err, ErrWeightsSumInvalid):
			status = http.StatusBadRequest
			code = "validation_error"
		}
		resp := gin.H{"error": code, "message": safeMessage(status, err, "Failed to create coalition")}
		if extra := moneyFields(err); extra != nil {
			for k, v := range extra {
				resp[k] = v
			}
		}
		c.JSON(status, resp)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"coalition": ce})
}

// GetCoalition handles GET /v1/escrow/coalition/:id
func (h *CoalitionHandler) GetCoalition(c *gin.Context) {
	id := c.Param("id")

	ce, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrCoalitionNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Coalition escrow not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve coalition",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"coalition": ce})
}

// ListCoalitions handles GET /v1/agents/:address/coalitions
func (h *CoalitionHandler) ListCoalitions(c *gin.Context) {
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

	coalitions, err := h.service.ListByAgent(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to list coalitions",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"coalitions": coalitions, "count": len(coalitions)})
}

// ReportCompletion handles POST /v1/escrow/coalition/:id/complete
func (h *CoalitionHandler) ReportCompletion(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	ce, err := h.service.ReportCompletion(c.Request.Context(), id, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrCoalitionNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrNotMember):
			status = http.StatusForbidden
			code = "not_member"
		case errors.Is(err, ErrMemberAlreadyReported):
			status = http.StatusConflict
			code = "already_reported"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_state"
		}
		c.JSON(status, gin.H{"error": code, "message": safeMessage(status, err, "Failed to report completion")})
		return
	}

	c.JSON(http.StatusOK, gin.H{"coalition": ce})
}

// OracleReport handles POST /v1/escrow/coalition/:id/oracle-report
func (h *CoalitionHandler) OracleReport(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	var req OracleReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "qualityScore is required (0.0 to 1.0)",
		})
		return
	}

	ce, err := h.service.OracleReport(c.Request.Context(), id, callerAddr, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrCoalitionNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrOracleUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrAlreadyResolved):
			status = http.StatusConflict
			code = "already_settled"
		case errors.Is(err, ErrInvalidQualityScore):
			status = http.StatusBadRequest
			code = "invalid_score"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_state"
		}
		c.JSON(status, gin.H{"error": code, "message": safeMessage(status, err, "Failed to process oracle report")})
		return
	}

	c.JSON(http.StatusOK, gin.H{"coalition": ce})
}

// AbortCoalition handles POST /v1/escrow/coalition/:id/abort
func (h *CoalitionHandler) AbortCoalition(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	ce, err := h.service.Abort(c.Request.Context(), id, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrCoalitionNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrAlreadyResolved):
			status = http.StatusConflict
			code = "already_settled"
		}
		resp := gin.H{"error": code, "message": safeMessage(status, err, "Failed to abort coalition")}
		if extra := moneyFields(err); extra != nil {
			for k, v := range extra {
				resp[k] = v
			}
		}
		c.JSON(status, resp)
		return
	}

	c.JSON(http.StatusOK, gin.H{"coalition": ce})
}
