package gas

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP handlers for gas operations
type Handler struct {
	paymaster Paymaster
}

// NewHandler creates a new gas handler
func NewHandler(paymaster Paymaster) *Handler {
	return &Handler{paymaster: paymaster}
}

// RegisterRoutes sets up the gas routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/gas/estimate", h.EstimateGas)
	r.GET("/gas/status", h.GetStatus)
}

// EstimateGasRequest is the request body for gas estimation
type EstimateGasRequest struct {
	From   string `json:"from" binding:"required"`
	To     string `json:"to" binding:"required"`
	Amount string `json:"amount" binding:"required"`
}

// EstimateGas handles POST /gas/estimate
func (h *Handler) EstimateGas(c *gin.Context) {
	var req EstimateGasRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	estimate, err := h.paymaster.EstimateGasFee(c.Request.Context(), &EstimateRequest{
		From:   req.From,
		To:     req.To,
		Amount: req.Amount,
	})

	if err != nil {
		if gasErr, ok := err.(*GasSponsorshipError); ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   gasErr.Code,
				"message": gasErr.Message,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "estimate_failed",
			"message": "Failed to estimate gas",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"estimate": estimate,
		"message":  "Gas will be sponsored. Agent pays USDC only.",
	})
}

// GetStatus handles GET /gas/status
func (h *Handler) GetStatus(c *gin.Context) {
	// Get paymaster status
	balance, err := h.paymaster.GetBalance(c.Request.Context())

	status := gin.H{
		"sponsorshipEnabled": true,
		"network":            "base-sepolia",
		"currency":           "USDC",
		"message":            "Agents pay in USDC only. Gas is sponsored by the platform.",
	}

	if err == nil && balance != nil {
		status["paymasterBalance"] = formatETH(balance)
	}

	// Add daily spending if available
	if pm, ok := h.paymaster.(*PlatformPaymaster); ok {
		spent, limit := pm.GetDailySpending()
		status["dailySpending"] = gin.H{
			"spent": spent,
			"limit": limit,
		}
	}

	c.JSON(http.StatusOK, status)
}
