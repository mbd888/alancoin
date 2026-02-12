package streams

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/validation"
)

// ScopeChecker verifies that a session key possesses a required scope.
type ScopeChecker interface {
	ValidateScope(ctx context.Context, keyID, scope string) error
}

// Handler provides HTTP endpoints for streaming micropayments.
type Handler struct {
	service      *Service
	scopeChecker ScopeChecker
}

// NewHandler creates a new stream handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// WithScopeChecker adds scope enforcement for session key capabilities.
func (h *Handler) WithScopeChecker(sc ScopeChecker) *Handler {
	h.scopeChecker = sc
	return h
}

// RegisterRoutes sets up public (read-only) stream routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/streams/:id", h.GetStream)
	r.GET("/streams/:id/ticks", h.ListTicks)
	r.GET("/agents/:address/streams", h.ListStreams)
}

// RegisterProtectedRoutes sets up protected (auth-required) stream routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/streams", h.OpenStream)
	r.POST("/streams/:id/tick", h.TickStream)
	r.POST("/streams/:id/close", h.CloseStream)
}

// OpenStream handles POST /v1/streams
func (h *Handler) OpenStream(c *gin.Context) {
	var req OpenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Validate address fields
	if errs := validation.Validate(
		validation.ValidAddress("buyer_addr", req.BuyerAddr),
		validation.ValidAddress("seller_addr", req.SellerAddr),
		validation.ValidAmount("hold_amount", req.HoldAmount),
		validation.ValidAmount("price_per_tick", req.PricePerTick),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": errs.Error(),
			"details": errs,
		})
		return
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

	// Enforce "streams" scope if a session key is provided
	if h.scopeChecker != nil && req.SessionKeyID != "" {
		if err := h.scopeChecker.ValidateScope(c.Request.Context(), req.SessionKeyID, "streams"); err != nil {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "scope_not_allowed",
				"message": "Session key does not have the 'streams' scope",
			})
			return
		}
	}

	stream, err := h.service.Open(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "stream_failed"
		msg := "Failed to open stream"
		if errors.Is(err, ErrInvalidAmount) {
			status = http.StatusBadRequest
			code = "invalid_amount"
			msg = err.Error()
		}
		c.JSON(status, gin.H{"error": code, "message": msg})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"stream": stream})
}

// GetStream handles GET /v1/streams/:id
func (h *Handler) GetStream(c *gin.Context) {
	id := c.Param("id")

	stream, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrStreamNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Stream not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"stream": stream})
}

// ListStreams handles GET /v1/agents/:address/streams
func (h *Handler) ListStreams(c *gin.Context) {
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

	streams, err := h.service.ListByAgent(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"streams": streams,
		"count":   len(streams),
	})
}

// TickStream handles POST /v1/streams/:id/tick
func (h *Handler) TickStream(c *gin.Context) {
	id := c.Param("id")

	var req TickRequest
	// Allow empty body (uses pricePerTick as default)
	_ = c.ShouldBindJSON(&req)

	// Verify caller is either buyer or seller
	callerAddr := strings.ToLower(c.GetString("authAgentAddr"))
	stream, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrStreamNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Stream not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}
	if callerAddr != stream.BuyerAddr && callerAddr != stream.SellerAddr {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "unauthorized",
			"message": "Only stream buyer or seller can record ticks",
		})
		return
	}

	tick, updatedStream, err := h.service.RecordTick(c.Request.Context(), id, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "tick_failed"
		switch {
		case errors.Is(err, ErrStreamNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrAlreadyClosed):
			status = http.StatusConflict
			code = "stream_closed"
		case errors.Is(err, ErrHoldExhausted):
			status = http.StatusPaymentRequired
			code = "hold_exhausted"
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tick":   tick,
		"stream": updatedStream,
	})
}

// CloseStream handles POST /v1/streams/:id/close
func (h *Handler) CloseStream(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	var req CloseRequest
	_ = c.ShouldBindJSON(&req)

	stream, err := h.service.Close(c.Request.Context(), id, callerAddr, req.Reason)
	if err != nil {
		status := http.StatusInternalServerError
		code := "close_failed"
		switch {
		case errors.Is(err, ErrStreamNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrAlreadyClosed):
			status = http.StatusConflict
			code = "already_closed"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"stream": stream})
}

// ListTicks handles GET /v1/streams/:id/ticks
func (h *Handler) ListTicks(c *gin.Context) {
	id := c.Param("id")
	limit := 100
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 1000 {
				limit = 1000
			}
		}
	}

	ticks, err := h.service.ListTicks(c.Request.Context(), id, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ticks": ticks,
		"count": len(ticks),
	})
}
