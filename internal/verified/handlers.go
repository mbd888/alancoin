package verified

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/validation"
)

// Handler provides HTTP handlers for the verified agents API.
type Handler struct {
	service *Service
}

// NewHandler creates a new verified agent handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up the verified agent routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/verified/apply", h.Apply)
	r.GET("/verified/:address", h.GetByAgent)
	r.POST("/verified/:address/revoke", h.Revoke)
	r.POST("/verified/:address/reinstate", h.Reinstate)
	r.GET("/verified", h.List)
}

// Apply handles POST /v1/verified/apply
func (h *Handler) Apply(c *gin.Context) {
	var req ApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	if errs := validation.Validate(
		validation.ValidAddress("agentAddr", req.AgentAddr),
		validation.ValidAmount("bondAmount", req.BondAmount),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_failed",
			"message": errs.Error(),
		})
		return
	}

	v, result, err := h.service.Apply(c.Request.Context(), req.AgentAddr, req.BondAmount)
	if err != nil {
		switch {
		case err == ErrAlreadyVerified:
			c.JSON(http.StatusConflict, gin.H{
				"error":   "already_verified",
				"message": "Agent already has active verification",
			})
		case err == ErrNotEligible:
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":      "not_eligible",
				"message":    "Agent does not meet verification requirements",
				"evaluation": result,
			})
		case strings.Contains(err.Error(), "bond"):
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "bond_error",
				"message": err.Error(),
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "internal_error",
				"message": "Failed to process verification",
			})
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"verification": v,
		"evaluation":   result,
	})
}

// GetByAgent handles GET /v1/verified/:address
func (h *Handler) GetByAgent(c *gin.Context) {
	address := c.Param("address")

	v, err := h.service.GetByAgent(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "No verification found for this agent",
		})
		return
	}

	c.JSON(http.StatusOK, v)
}

// Revoke handles POST /v1/verified/:address/revoke
func (h *Handler) Revoke(c *gin.Context) {
	address := c.Param("address")

	v, err := h.service.Revoke(c.Request.Context(), address)
	if err != nil {
		switch {
		case err == ErrNotVerified:
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_verified",
				"message": "Agent is not verified",
			})
		case err == ErrInvalidStatus:
			c.JSON(http.StatusConflict, gin.H{
				"error":   "invalid_status",
				"message": "Verification is already in a terminal state",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "internal_error",
				"message": "Failed to revoke verification",
			})
		}
		return
	}

	c.JSON(http.StatusOK, v)
}

// Reinstate handles POST /v1/verified/:address/reinstate
func (h *Handler) Reinstate(c *gin.Context) {
	address := c.Param("address")

	v, err := h.service.Reinstate(c.Request.Context(), address)
	if err != nil {
		switch {
		case err == ErrNotVerified:
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_verified",
				"message": "Agent is not verified",
			})
		case err == ErrInvalidStatus:
			c.JSON(http.StatusConflict, gin.H{
				"error":   "invalid_status",
				"message": "Only suspended verifications can be reinstated",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "internal_error",
				"message": "Failed to reinstate verification",
			})
		}
		return
	}

	c.JSON(http.StatusOK, v)
}

// List handles GET /v1/verified
func (h *Handler) List(c *gin.Context) {
	status := c.DefaultQuery("status", "active")
	limit := parseIntQuery(c, "limit", 50)

	var verifications []*Verification
	var err error

	switch status {
	case "all":
		verifications, err = h.service.ListAll(c.Request.Context(), limit)
	default:
		verifications, err = h.service.ListActive(c.Request.Context(), limit)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to list verifications",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"verifications": verifications,
		"count":         len(verifications),
	})
}

func parseIntQuery(c *gin.Context, key string, defaultVal int) int {
	if val := c.Query(key); val != "" {
		var i int
		if _, err := fmt.Sscanf(val, "%d", &i); err == nil && i > 0 {
			if i > 1000 {
				i = 1000
			}
			return i
		}
	}
	return defaultVal
}
