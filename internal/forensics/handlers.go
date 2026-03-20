package forensics

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for forensics operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new forensics handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up read-only forensics routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/forensics/agents/:address/baseline", h.GetBaseline)
	r.GET("/forensics/agents/:address/alerts", h.ListAgentAlerts)
	r.GET("/forensics/alerts", h.ListAllAlerts)
}

// RegisterProtectedRoutes sets up auth-required forensics routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/forensics/events", h.IngestEvent)
	r.POST("/forensics/alerts/:id/acknowledge", h.AcknowledgeAlert)
}

// IngestEvent handles POST /v1/forensics/events
func (h *Handler) IngestEvent(c *gin.Context) {
	var req SpendEvent
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	alerts, err := h.service.Ingest(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"alerts":     alerts,
		"alertCount": len(alerts),
	})
}

// GetBaseline handles GET /v1/forensics/agents/:address/baseline
func (h *Handler) GetBaseline(c *gin.Context) {
	baseline, err := h.service.GetBaseline(c.Request.Context(), c.Param("address"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no baseline for this agent"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"baseline": baseline})
}

// ListAgentAlerts handles GET /v1/forensics/agents/:address/alerts
func (h *Handler) ListAgentAlerts(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	alerts, err := h.service.ListAlerts(c.Request.Context(), c.Param("address"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"alerts": alerts, "count": len(alerts)})
}

// ListAllAlerts handles GET /v1/forensics/alerts?severity=critical&limit=50
func (h *Handler) ListAllAlerts(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	severity := AlertSeverity(c.Query("severity"))
	alerts, err := h.service.store.ListAllAlerts(c.Request.Context(), severity, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"alerts": alerts, "count": len(alerts)})
}

// AcknowledgeAlert handles POST /v1/forensics/alerts/:id/acknowledge
func (h *Handler) AcknowledgeAlert(c *gin.Context) {
	if err := h.service.AcknowledgeAlert(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
}
