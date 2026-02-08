package credit

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for credit operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new credit handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up public (read-only) credit routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/agents/:address/credit", h.GetCreditLine)
	r.GET("/credit/active", h.ListActiveCredits)
}

// RegisterProtectedRoutes sets up protected (auth-required) credit routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/agents/:address/credit/apply", h.ApplyForCredit)
	r.POST("/agents/:address/credit/repay", h.RepayCredit)
	r.POST("/agents/:address/credit/review", h.ReviewCredit)
}

// RegisterAdminRoutes sets up admin-only credit routes.
func (h *Handler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.POST("/credit/:address/revoke", h.RevokeCredit)
	r.POST("/credit/check-defaults", h.CheckDefaults)
}

// GetCreditLine handles GET /v1/agents/:address/credit
func (h *Handler) GetCreditLine(c *gin.Context) {
	address := c.Param("address")

	line, err := h.service.GetByAgent(c.Request.Context(), address)
	if err != nil {
		if err == ErrCreditLineNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "No credit line found for this agent",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"credit_line": line})
}

// ListActiveCredits handles GET /v1/credit/active
func (h *Handler) ListActiveCredits(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	lines, err := h.service.ListActive(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"credit_lines": lines,
		"count":        len(lines),
	})
}

// ApplyForCredit handles POST /v1/agents/:address/credit/apply
func (h *Handler) ApplyForCredit(c *gin.Context) {
	address := c.Param("address")

	// Verify the authenticated agent is the applicant
	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must be the applicant",
		})
		return
	}

	line, result, err := h.service.Apply(c.Request.Context(), address)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch err {
		case ErrNotEligible:
			status = http.StatusUnprocessableEntity
			code = "not_eligible"
		case ErrCreditLineExists:
			status = http.StatusConflict
			code = "already_exists"
		case ErrCreditLineRevoked:
			status = http.StatusForbidden
			code = "revoked"
		case ErrCreditLineDefaulted:
			status = http.StatusForbidden
			code = "defaulted"
		}
		resp := gin.H{"error": code, "message": err.Error()}
		if result != nil {
			resp["evaluation"] = result
		}
		c.JSON(status, resp)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"credit_line": line,
		"evaluation":  result,
	})
}

// RepayCredit handles POST /v1/agents/:address/credit/repay
func (h *Handler) RepayCredit(c *gin.Context) {
	address := c.Param("address")

	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must be the credit holder",
		})
		return
	}

	var req RepaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Amount is required",
		})
		return
	}

	if err := h.service.Repay(c.Request.Context(), address, req.Amount); err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		if err == ErrCreditLineNotFound {
			status = http.StatusNotFound
			code = "not_found"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "repayment processed"})
}

// ReviewCredit handles POST /v1/agents/:address/credit/review
func (h *Handler) ReviewCredit(c *gin.Context) {
	address := c.Param("address")

	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must be the credit holder",
		})
		return
	}

	line, result, err := h.service.Review(c.Request.Context(), address)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		if err == ErrCreditLineNotFound {
			status = http.StatusNotFound
			code = "not_found"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"credit_line": line,
		"evaluation":  result,
	})
}

// RevokeCredit handles POST /v1/admin/credit/:address/revoke
func (h *Handler) RevokeCredit(c *gin.Context) {
	address := c.Param("address")

	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)

	line, err := h.service.Revoke(c.Request.Context(), address, body.Reason)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		if err == ErrCreditLineNotFound {
			status = http.StatusNotFound
			code = "not_found"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"credit_line": line})
}

// CheckDefaults handles POST /v1/admin/credit/check-defaults
func (h *Handler) CheckDefaults(c *gin.Context) {
	count, err := h.service.CheckDefaults(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"defaults_found": count,
		"message":        "default check completed",
	})
}
