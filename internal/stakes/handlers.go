package stakes

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for agent revenue staking.
type Handler struct {
	service *Service
}

// NewHandler creates a new stakes handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up public (read-only) stake routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/stakes", h.ListOfferings)
	r.GET("/stakes/:id", h.GetOffering)
	r.GET("/stakes/:id/distributions", h.ListDistributions)
	r.GET("/stakes/:id/orders", h.ListOrders)
	r.GET("/stakes/:id/holdings", h.ListStakeHoldings)
	r.GET("/agents/:address/stakes", h.ListAgentStakes)
	r.GET("/agents/:address/portfolio", h.GetPortfolio)
	r.GET("/agents/:address/holdings", h.ListInvestorHoldings)
}

// RegisterProtectedRoutes sets up protected (auth-required) stake routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/stakes", h.CreateOffering)
	r.POST("/stakes/:id/invest", h.Invest)
	r.POST("/stakes/:id/close", h.CloseOffering)
	r.POST("/stakes/orders", h.PlaceOrder)
	r.POST("/stakes/orders/:orderId/fill", h.FillOrder)
	r.DELETE("/stakes/orders/:orderId", h.CancelOrder)
}

// CreateOffering handles POST /v1/stakes
func (h *Handler) CreateOffering(c *gin.Context) {
	var req CreateStakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Verify the authenticated agent owns the agent address
	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.AgentAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must match agentAddr",
		})
		return
	}

	stake, err := h.service.CreateStake(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "create_failed"
		switch {
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
		case errors.Is(err, ErrMaxRevenueShare):
			status = http.StatusConflict
			code = "max_revenue_share"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"stake": stake})
}

// GetOffering handles GET /v1/stakes/:id
func (h *Handler) GetOffering(c *gin.Context) {
	id := c.Param("id")

	stake, err := h.service.GetStake(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrStakeNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Stake not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"stake": stake})
}

// ListOfferings handles GET /v1/stakes
func (h *Handler) ListOfferings(c *gin.Context) {
	limit := parseLimit(c, 50, 200)

	stakes, err := h.service.ListOpen(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"stakes": stakes, "count": len(stakes)})
}

// ListAgentStakes handles GET /v1/agents/:address/stakes
func (h *Handler) ListAgentStakes(c *gin.Context) {
	address := c.Param("address")

	stakes, err := h.service.ListByAgent(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"stakes": stakes, "count": len(stakes)})
}

// Invest handles POST /v1/stakes/:id/invest
func (h *Handler) Invest(c *gin.Context) {
	stakeID := c.Param("id")

	var req InvestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "Invalid request body"})
		return
	}

	// Verify the authenticated agent is the investor
	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.InvestorAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must match investorAddr",
		})
		return
	}

	holding, err := h.service.Invest(c.Request.Context(), stakeID, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "invest_failed"
		switch {
		case errors.Is(err, ErrStakeNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrStakeClosed):
			status = http.StatusConflict
			code = "stake_closed"
		case errors.Is(err, ErrSelfInvestment):
			status = http.StatusBadRequest
			code = "self_investment"
		case errors.Is(err, ErrInsufficientShare):
			status = http.StatusConflict
			code = "insufficient_shares"
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"holding": holding})
}

// GetPortfolio handles GET /v1/agents/:address/portfolio
func (h *Handler) GetPortfolio(c *gin.Context) {
	address := c.Param("address")

	portfolio, err := h.service.GetPortfolio(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"portfolio": portfolio})
}

// ListStakeHoldings handles GET /v1/stakes/:id/holdings
func (h *Handler) ListStakeHoldings(c *gin.Context) {
	stakeID := c.Param("id")

	holdings, err := h.service.ListHoldingsByStake(c.Request.Context(), stakeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"holdings": holdings, "count": len(holdings)})
}

// ListInvestorHoldings handles GET /v1/agents/:address/holdings
func (h *Handler) ListInvestorHoldings(c *gin.Context) {
	address := c.Param("address")

	holdings, err := h.service.ListHoldingsByInvestor(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"holdings": holdings, "count": len(holdings)})
}

// ListDistributions handles GET /v1/stakes/:id/distributions
func (h *Handler) ListDistributions(c *gin.Context) {
	stakeID := c.Param("id")
	limit := parseLimit(c, 50, 200)

	dists, err := h.service.ListDistributions(c.Request.Context(), stakeID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"distributions": dists, "count": len(dists)})
}

// CloseOffering handles POST /v1/stakes/:id/close
func (h *Handler) CloseOffering(c *gin.Context) {
	stakeID := c.Param("id")

	stake, err := h.service.CloseStake(c.Request.Context(), stakeID)
	if err != nil {
		status := http.StatusInternalServerError
		code := "close_failed"
		switch {
		case errors.Is(err, ErrStakeNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrStakeClosed):
			status = http.StatusConflict
			code = "already_closed"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"stake": stake})
}

// PlaceOrder handles POST /v1/stakes/orders
func (h *Handler) PlaceOrder(c *gin.Context) {
	var req PlaceOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "Invalid request body"})
		return
	}

	// Verify the authenticated agent is the seller
	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.SellerAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must match sellerAddr",
		})
		return
	}

	order, err := h.service.PlaceOrder(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "order_failed"
		switch {
		case errors.Is(err, ErrHoldingNotFound):
			status = http.StatusNotFound
			code = "holding_not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrNotVested):
			status = http.StatusConflict
			code = "not_vested"
		case errors.Is(err, ErrInsufficientHeld):
			status = http.StatusBadRequest
			code = "insufficient_shares"
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"order": order})
}

// FillOrder handles POST /v1/stakes/orders/:orderId/fill
func (h *Handler) FillOrder(c *gin.Context) {
	orderID := c.Param("orderId")

	var req FillOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "Invalid request body"})
		return
	}

	// Verify the authenticated agent is the buyer
	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.BuyerAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must match buyerAddr",
		})
		return
	}

	order, holding, err := h.service.FillOrder(c.Request.Context(), orderID, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "fill_failed"
		switch {
		case errors.Is(err, ErrOrderNotFound):
			status = http.StatusNotFound
			code = "order_not_found"
		case errors.Is(err, ErrOrderNotOpen):
			status = http.StatusConflict
			code = "order_not_open"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"order": order, "holding": holding})
}

// CancelOrder handles DELETE /v1/stakes/orders/:orderId
func (h *Handler) CancelOrder(c *gin.Context) {
	orderID := c.Param("orderId")
	callerAddr := c.GetString("authAgentAddr")

	order, err := h.service.CancelOrder(c.Request.Context(), orderID, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "cancel_failed"
		switch {
		case errors.Is(err, ErrOrderNotFound):
			status = http.StatusNotFound
			code = "order_not_found"
		case errors.Is(err, ErrOrderNotOpen):
			status = http.StatusConflict
			code = "order_not_open"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"order": order})
}

// ListOrders handles GET /v1/stakes/:id/orders
func (h *Handler) ListOrders(c *gin.Context) {
	stakeID := c.Param("id")
	status := c.Query("status")
	limit := parseLimit(c, 50, 200)

	orders, err := h.service.ListOrdersByStake(c.Request.Context(), stakeID, status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"orders": orders, "count": len(orders)})
}

func parseLimit(c *gin.Context, defaultLimit, maxLimit int) int {
	limit := defaultLimit
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > maxLimit {
				limit = maxLimit
			}
		}
	}
	return limit
}
