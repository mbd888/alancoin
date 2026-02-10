package negotiation

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for autonomous negotiation.
type Handler struct {
	service *Service
}

// NewHandler creates a new negotiation handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up public (read-only) negotiation routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/rfps", h.ListRFPs)
	r.GET("/rfps/:id", h.GetRFP)
	r.GET("/rfps/:id/bids", h.ListBids)
	r.GET("/agents/:address/rfps", h.ListAgentRFPs)
}

// RegisterProtectedRoutes sets up protected (auth-required) negotiation routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/rfps", h.PublishRFP)
	r.POST("/rfps/:id/bids", h.PlaceBid)
	r.POST("/rfps/:id/bids/:bidId/counter", h.CounterBid)
	r.POST("/rfps/:id/select", h.SelectWinner)
	r.POST("/rfps/:id/cancel", h.CancelRFP)
}

// PublishRFP handles POST /v1/rfps
func (h *Handler) PublishRFP(c *gin.Context) {
	var req PublishRFPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Verify caller is the buyer
	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.BuyerAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must be the buyer",
		})
		return
	}

	rfp, err := h.service.PublishRFP(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "rfp_failed",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"rfp": rfp})
}

// GetRFP handles GET /v1/rfps/:id
func (h *Handler) GetRFP(c *gin.Context) {
	id := c.Param("id")

	rfp, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrRFPNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "RFP not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"rfp": rfp})
}

// ListRFPs handles GET /v1/rfps
func (h *Handler) ListRFPs(c *gin.Context) {
	serviceType := c.Query("type")
	limit := parseLimit(c.Query("limit"), 50, 200)

	rfps, err := h.service.ListOpenRFPs(c.Request.Context(), serviceType, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"rfps":  rfps,
		"count": len(rfps),
	})
}

// ListBids handles GET /v1/rfps/:id/bids
func (h *Handler) ListBids(c *gin.Context) {
	rfpID := c.Param("id")
	limit := parseLimit(c.Query("limit"), 50, 200)

	bids, err := h.service.ListBidsByRFP(c.Request.Context(), rfpID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"bids":  bids,
		"count": len(bids),
	})
}

// ListAgentRFPs handles GET /v1/agents/:address/rfps
func (h *Handler) ListAgentRFPs(c *gin.Context) {
	address := c.Param("address")
	role := c.Query("role")
	limit := parseLimit(c.Query("limit"), 50, 200)

	ctx := c.Request.Context()

	if role == "seller" {
		// List bids by seller, then extract unique RFP IDs
		bids, err := h.service.ListBidsBySeller(ctx, address, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "internal_error",
				"message": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"bids":  bids,
			"count": len(bids),
		})
		return
	}

	// Default: buyer role
	rfps, err := h.service.ListByBuyer(ctx, address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"rfps":  rfps,
		"count": len(rfps),
	})
}

// PlaceBid handles POST /v1/rfps/:id/bids
func (h *Handler) PlaceBid(c *gin.Context) {
	rfpID := c.Param("id")

	var req PlaceBidRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Verify caller is the seller
	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.SellerAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Authenticated agent must be the seller",
		})
		return
	}

	bid, err := h.service.PlaceBid(c.Request.Context(), rfpID, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "bid_failed"
		switch {
		case errors.Is(err, ErrRFPNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "rfp_not_open"
		case errors.Is(err, ErrBidDeadlinePast):
			status = http.StatusConflict
			code = "deadline_past"
		case errors.Is(err, ErrSelfBid):
			status = http.StatusBadRequest
			code = "self_bid"
		case errors.Is(err, ErrDuplicateBid):
			status = http.StatusConflict
			code = "duplicate_bid"
		case errors.Is(err, ErrBudgetOutOfRange):
			status = http.StatusBadRequest
			code = "budget_out_of_range"
		case errors.Is(err, ErrLowReputation):
			status = http.StatusForbidden
			code = "low_reputation"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"bid": bid})
}

// CounterBid handles POST /v1/rfps/:id/bids/:bidId/counter
func (h *Handler) CounterBid(c *gin.Context) {
	rfpID := c.Param("id")
	bidID := c.Param("bidId")

	var req CounterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	callerAddr := c.GetString("authAgentAddr")

	bid, err := h.service.Counter(c.Request.Context(), rfpID, bidID, callerAddr, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "counter_failed"
		switch {
		case errors.Is(err, ErrRFPNotFound):
			status = http.StatusNotFound
			code = "rfp_not_found"
		case errors.Is(err, ErrBidNotFound):
			status = http.StatusNotFound
			code = "bid_not_found"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_status"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrMaxCounterRounds):
			status = http.StatusConflict
			code = "max_rounds"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"bid": bid})
}

// SelectWinner handles POST /v1/rfps/:id/select
func (h *Handler) SelectWinner(c *gin.Context) {
	rfpID := c.Param("id")

	var req SelectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body: bidId is required",
		})
		return
	}

	callerAddr := c.GetString("authAgentAddr")

	rfp, bid, err := h.service.SelectWinner(c.Request.Context(), rfpID, req.BidID, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "select_failed"
		switch {
		case errors.Is(err, ErrRFPNotFound):
			status = http.StatusNotFound
			code = "rfp_not_found"
		case errors.Is(err, ErrBidNotFound):
			status = http.StatusNotFound
			code = "bid_not_found"
		case errors.Is(err, ErrAlreadyAwarded):
			status = http.StatusConflict
			code = "already_awarded"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_status"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"rfp":        rfp,
		"winningBid": bid,
	})
}

// CancelRFP handles POST /v1/rfps/:id/cancel
func (h *Handler) CancelRFP(c *gin.Context) {
	rfpID := c.Param("id")

	var req CancelRequest
	_ = c.ShouldBindJSON(&req)

	callerAddr := c.GetString("authAgentAddr")

	rfp, err := h.service.CancelRFP(c.Request.Context(), rfpID, callerAddr, req.Reason)
	if err != nil {
		status := http.StatusInternalServerError
		code := "cancel_failed"
		switch {
		case errors.Is(err, ErrRFPNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrAlreadyAwarded):
			status = http.StatusConflict
			code = "already_awarded"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"rfp": rfp})
}

func parseLimit(s string, defaultVal, maxVal int) int {
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return defaultVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}
