package receipts

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for receipt operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new receipt handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up public (read-only) receipt routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/receipts/:id", h.GetReceipt)
	r.GET("/agents/:address/receipts", h.ListByAgent)
	r.POST("/receipts/verify", h.VerifyReceipt)
}

// GetReceipt handles GET /v1/receipts/:id
func (h *Handler) GetReceipt(c *gin.Context) {
	id := c.Param("id")

	receipt, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Receipt not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"receipt": receipt})
}

// ListByAgent handles GET /v1/agents/:address/receipts
func (h *Handler) ListByAgent(c *gin.Context) {
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

	receipts, err := h.service.ListByAgent(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"receipts": receipts,
		"count":    len(receipts),
	})
}

// VerifyReceipt handles POST /v1/receipts/verify
func (h *Handler) VerifyReceipt(c *gin.Context) {
	var req VerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	resp, err := h.service.Verify(c.Request.Context(), req.ReceiptID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"verification": resp})
}
