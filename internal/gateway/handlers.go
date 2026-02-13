package gateway

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/validation"
)

// moneyFields extracts funds-state context from a MoneyError if present.
// Returns extra fields to merge into the JSON error response.
func moneyFields(err error) gin.H {
	var me *MoneyError
	if errors.As(err, &me) {
		h := gin.H{"funds_status": me.FundsStatus, "recovery": me.Recovery}
		if me.Amount != "" {
			h["amount"] = me.Amount
		}
		if me.Reference != "" {
			h["reference"] = me.Reference
		}
		return h
	}
	return nil
}

// Handler provides HTTP endpoints for the gateway.
type Handler struct {
	service *Service
}

// NewHandler creates a new gateway handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterProtectedRoutes sets up session management routes (API key auth).
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/gateway/sessions", h.CreateSession)
	r.GET("/gateway/sessions", h.ListSessions)
	r.GET("/gateway/sessions/:id", h.GetSession)
	r.DELETE("/gateway/sessions/:id", h.CloseSession)
	r.GET("/gateway/sessions/:id/logs", h.ListLogs)
	r.POST("/gateway/call", h.SingleCall)
}

// RegisterProxyRoute sets up the proxy endpoint (gateway token auth).
func (h *Handler) RegisterProxyRoute(r *gin.RouterGroup) {
	r.POST("/gateway/proxy", h.gatewayTokenAuth(), h.Proxy)
}

// gatewayTokenAuth validates X-Gateway-Token header against session store
// and verifies caller identity (authAgentAddr must match session owner).
func (h *Handler) gatewayTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("X-Gateway-Token")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "X-Gateway-Token header required",
			})
			return
		}

		session, err := h.service.GetSession(c.Request.Context(), token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "Invalid gateway token",
			})
			return
		}

		// Verify caller owns this session (requires API key auth upstream).
		callerAddr := strings.ToLower(c.GetString("authAgentAddr"))
		if callerAddr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "API key authentication required for gateway proxy",
			})
			return
		}
		if callerAddr != session.AgentAddr {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "forbidden",
				"message": "This gateway session belongs to another agent",
			})
			return
		}

		if session.Status != StatusActive {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "session_closed",
				"message": "Gateway session is no longer active",
			})
			return
		}

		if session.IsExpired() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "session_expired",
				"message": "Gateway session has expired",
			})
			return
		}

		c.Set("gatewaySessionID", session.ID)
		c.Next()
	}
}

// CreateSession handles POST /v1/gateway/sessions
func (h *Handler) CreateSession(c *gin.Context) {
	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	if errs := validation.Validate(
		validation.ValidAmount("max_total", req.MaxTotal),
		validation.ValidAmount("max_per_request", req.MaxPerRequest),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": errs.Error(),
			"details": errs,
		})
		return
	}

	agentAddr := c.GetString("authAgentAddr")

	session, err := h.service.CreateSession(c.Request.Context(), agentAddr, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "session_failed"
		msg := "Failed to create gateway session"
		switch {
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
			msg = err.Error()
		case errors.Is(err, ErrPolicyDenied):
			status = http.StatusForbidden
			code = "policy_denied"
			msg = err.Error()
		}
		resp := gin.H{"error": code, "message": msg}
		if extra := moneyFields(err); extra != nil {
			for k, v := range extra {
				resp[k] = v
			}
		}
		c.JSON(status, resp)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"session": session,
		"token":   session.ID,
	})
}

// GetSession handles GET /v1/gateway/sessions/:id
func (h *Handler) GetSession(c *gin.Context) {
	id := c.Param("id")

	session, err := h.service.GetSession(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Session not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	// Only session owner can view
	callerAddr := strings.ToLower(c.GetString("authAgentAddr"))
	if callerAddr != session.AgentAddr {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "Not your session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"session": session})
}

// ListSessions handles GET /v1/gateway/sessions
func (h *Handler) ListSessions(c *gin.Context) {
	agentAddr := c.GetString("authAgentAddr")
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}

	sessions, err := h.service.ListSessions(c.Request.Context(), agentAddr, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "count": len(sessions)})
}

// CloseSession handles DELETE /v1/gateway/sessions/:id
func (h *Handler) CloseSession(c *gin.Context) {
	id := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	session, err := h.service.CloseSession(c.Request.Context(), id, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "close_failed"
		switch {
		case errors.Is(err, ErrSessionNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "forbidden"
		}
		resp := gin.H{"error": code, "message": err.Error()}
		if extra := moneyFields(err); extra != nil {
			for k, v := range extra {
				resp[k] = v
			}
		}
		c.JSON(status, resp)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session":       session,
		"totalSpent":    session.TotalSpent,
		"totalRefunded": session.Remaining(),
		"requestCount":  session.RequestCount,
	})
}

// Proxy handles POST /v1/gateway/proxy
func (h *Handler) Proxy(c *gin.Context) {
	sessionID := c.GetString("gatewaySessionID")

	var req ProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body: serviceType is required",
		})
		return
	}

	result, err := h.service.Proxy(c.Request.Context(), sessionID, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "proxy_failed"
		switch {
		case errors.Is(err, ErrSessionClosed):
			status = http.StatusConflict
			code = "session_closed"
		case errors.Is(err, ErrSessionExpired):
			status = http.StatusGone
			code = "session_expired"
		case errors.Is(err, ErrBudgetExceeded):
			status = http.StatusPaymentRequired
			code = "budget_exceeded"
		case errors.Is(err, ErrNoServiceAvailable):
			status = http.StatusNotFound
			code = "no_service"
		case errors.Is(err, ErrPolicyDenied):
			status = http.StatusForbidden
			code = "policy_denied"
		case errors.Is(err, ErrProxyFailed):
			status = http.StatusBadGateway
			code = "proxy_failed"
		}
		resp := gin.H{"error": code, "message": err.Error()}
		if extra := moneyFields(err); extra != nil {
			for k, v := range extra {
				resp[k] = v
			}
		}
		c.JSON(status, resp)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"result":     result,
		"totalSpent": result.TotalSpent,
		"remaining":  result.Remaining,
		"budgetLow":  result.BudgetLow,
	})
}

// SingleCall handles POST /v1/gateway/call
// One-shot: create session -> proxy -> close in a single HTTP call.
func (h *Handler) SingleCall(c *gin.Context) {
	var req SingleCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body: maxPrice and serviceType are required",
		})
		return
	}

	agentAddr := c.GetString("authAgentAddr")

	result, err := h.service.SingleCall(c.Request.Context(), agentAddr, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "call_failed"
		switch {
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
		case errors.Is(err, ErrNoServiceAvailable):
			status = http.StatusNotFound
			code = "no_service"
		case errors.Is(err, ErrPolicyDenied):
			status = http.StatusForbidden
			code = "policy_denied"
		case errors.Is(err, ErrProxyFailed):
			status = http.StatusBadGateway
			code = "proxy_failed"
		case errors.Is(err, ErrBudgetExceeded):
			status = http.StatusPaymentRequired
			code = "budget_exceeded"
		}
		resp := gin.H{"error": code, "message": err.Error()}
		if extra := moneyFields(err); extra != nil {
			for k, v := range extra {
				resp[k] = v
			}
		}
		c.JSON(status, resp)
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": result})
}

// ListLogs handles GET /v1/gateway/sessions/:id/logs
func (h *Handler) ListLogs(c *gin.Context) {
	sessionID := c.Param("id")
	limit := 100
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 1000 {
				limit = 1000
			}
		}
	}

	// Verify ownership
	session, err := h.service.GetSession(c.Request.Context(), sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Session not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	callerAddr := strings.ToLower(c.GetString("authAgentAddr"))
	if callerAddr != session.AgentAddr {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "Not your session"})
		return
	}

	logs, err := h.service.ListLogs(c.Request.Context(), sessionID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"logs": logs, "count": len(logs)})
}
