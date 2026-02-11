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

// RegisterAdminRoutes sets up admin-only negotiation routes.
func (h *Handler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.GET("/negotiation/analytics", h.GetAnalytics)
}

// RegisterProtectedRoutes sets up protected (auth-required) negotiation routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/rfps", h.PublishRFP)
	r.POST("/rfps/from-template/:templateId", h.PublishFromTemplate)
	r.POST("/rfps/:id/bids", h.PlaceBid)
	r.POST("/rfps/:id/bids/:bidId/counter", h.CounterBid)
	r.POST("/rfps/:id/bids/:bidId/withdraw", h.WithdrawBid)
	r.POST("/rfps/:id/select", h.SelectWinner)
	r.POST("/rfps/:id/cancel", h.CancelRFP)

	// Templates
	r.POST("/rfp-templates", h.CreateTemplate)
	r.GET("/rfp-templates", h.ListTemplates)
	r.GET("/rfp-templates/:templateId", h.GetTemplate)
	r.DELETE("/rfp-templates/:templateId", h.DeleteTemplate)
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
		case errors.Is(err, ErrBondRequired):
			status = http.StatusBadRequest
			code = "bond_required"
		case errors.Is(err, ErrInsufficientBond):
			status = http.StatusPaymentRequired
			code = "insufficient_bond"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"bid": bid})
}

// WithdrawBid handles POST /v1/rfps/:id/bids/:bidId/withdraw
func (h *Handler) WithdrawBid(c *gin.Context) {
	rfpID := c.Param("id")
	bidID := c.Param("bidId")

	callerAddr := c.GetString("authAgentAddr")

	bid, err := h.service.WithdrawBid(c.Request.Context(), rfpID, bidID, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "withdraw_failed"
		switch {
		case errors.Is(err, ErrRFPNotFound):
			status = http.StatusNotFound
			code = "rfp_not_found"
		case errors.Is(err, ErrBidNotFound):
			status = http.StatusNotFound
			code = "bid_not_found"
		case errors.Is(err, ErrBidAlreadyWithdrawn):
			status = http.StatusConflict
			code = "already_withdrawn"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_status"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrWithdrawalBlocked):
			status = http.StatusConflict
			code = "withdrawal_blocked"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"bid": bid})
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

// --- Template handlers ---

// CreateTemplate handles POST /v1/rfp-templates
func (h *Handler) CreateTemplate(c *gin.Context) {
	var req CreateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "Invalid request body"})
		return
	}

	callerAddr := c.GetString("authAgentAddr")
	tmpl, err := h.service.CreateTemplate(c.Request.Context(), callerAddr, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "template_failed", "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"template": tmpl})
}

// GetTemplate handles GET /v1/rfp-templates/:templateId
func (h *Handler) GetTemplate(c *gin.Context) {
	id := c.Param("templateId")

	tmpl, err := h.service.GetTemplate(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrTemplateNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Template not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"template": tmpl})
}

// ListTemplates handles GET /v1/rfp-templates
func (h *Handler) ListTemplates(c *gin.Context) {
	callerAddr := c.GetString("authAgentAddr")
	limit := parseLimit(c.Query("limit"), 50, 200)

	templates, err := h.service.ListTemplates(c.Request.Context(), callerAddr, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"templates": templates, "count": len(templates)})
}

// DeleteTemplate handles DELETE /v1/rfp-templates/:templateId
func (h *Handler) DeleteTemplate(c *gin.Context) {
	id := c.Param("templateId")
	callerAddr := c.GetString("authAgentAddr")

	err := h.service.DeleteTemplate(c.Request.Context(), id, callerAddr)
	if err != nil {
		if errors.Is(err, ErrTemplateNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Template not found"})
			return
		}
		if errors.Is(err, ErrUnauthorized) {
			c.JSON(http.StatusForbidden, gin.H{"error": "unauthorized", "message": "Not authorized to delete this template"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// PublishFromTemplate handles POST /v1/rfps/from-template/:templateId
func (h *Handler) PublishFromTemplate(c *gin.Context) {
	templateID := c.Param("templateId")

	var req PublishFromTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "Invalid request body"})
		return
	}

	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.BuyerAddr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "unauthorized", "message": "Authenticated agent must be the buyer"})
		return
	}

	rfp, err := h.service.PublishFromTemplate(c.Request.Context(), templateID, req)
	if err != nil {
		status := http.StatusBadRequest
		code := "rfp_from_template_failed"
		if errors.Is(err, ErrTemplateNotFound) {
			status = http.StatusNotFound
			code = "template_not_found"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"rfp": rfp})
}

// GetAnalytics handles GET /v1/admin/negotiation/analytics
func (h *Handler) GetAnalytics(c *gin.Context) {
	analytics, err := h.service.GetAnalytics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "analytics_failed",
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, analytics)
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
