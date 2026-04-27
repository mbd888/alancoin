package compliance

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
)

// resolveScope returns the scope param after verifying it matches the
// caller's tenant ID. When the caller has a non-empty tenant ID, the URL
// scope must equal it (case-insensitive). Deployments without tenancy
// (empty tenant ID) accept any scope, preserving single-tenant behavior.
// On mismatch the request is aborted with 403 and the bool is false.
func resolveScope(c *gin.Context) (string, bool) {
	urlScope := c.Param("scope")
	tenantID := auth.GetTenantID(c)
	if tenantID != "" && !strings.EqualFold(tenantID, urlScope) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "Scope does not match authenticated tenant.",
		})
		return "", false
	}
	return urlScope, true
}

// Handler exposes compliance operations over HTTP.
type Handler struct {
	service *Service
}

// NewHandler creates a new compliance handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{service: svc}
}

// RegisterRoutes wires routes onto the given router group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/compliance/:scope/readiness", h.GetReadiness)
	r.GET("/compliance/:scope/incidents", h.ListIncidents)
	r.POST("/compliance/:scope/incidents", h.RecordIncident)
	r.POST("/compliance/incidents/:id/ack", h.AcknowledgeIncident)
	r.GET("/compliance/controls", h.ListControls)
	r.PUT("/compliance/controls/:id", h.UpsertControl)
}

// GetReadiness handles GET /compliance/:scope/readiness
func (h *Handler) GetReadiness(c *gin.Context) {
	scope, ok := resolveScope(c)
	if !ok {
		return
	}
	report, err := h.service.Readiness(c.Request.Context(), scope)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to compute readiness",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"report": report})
}

// ListIncidents handles GET /compliance/:scope/incidents
// Query params: severity, source, agent, onlyUnacked, since, until, limit.
func (h *Handler) ListIncidents(c *gin.Context) {
	scope, ok := resolveScope(c)
	if !ok {
		return
	}
	filter := IncidentFilter{
		Scope:       scope,
		Source:      c.Query("source"),
		AgentAddr:   c.Query("agent"),
		OnlyUnacked: c.Query("onlyUnacked") == "true",
	}
	if sev := c.Query("severity"); sev != "" {
		filter.MinSeverity = IncidentSeverity(sev)
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
			if filter.Limit > 500 {
				filter.Limit = 500
			}
		}
	}
	if v := c.Query("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "since must be RFC3339"})
			return
		}
		filter.Since = t
	}
	if v := c.Query("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "until must be RFC3339"})
			return
		}
		filter.Until = t
	}

	incidents, err := h.service.ListIncidents(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to list incidents",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"incidents": incidents,
		"count":     len(incidents),
	})
}

// recordIncidentReq is the body shape for RecordIncident.
// OccurredAt defaults to server time when absent.
type recordIncidentReq struct {
	Source     string           `json:"source" binding:"required"`
	Severity   IncidentSeverity `json:"severity" binding:"required"`
	Kind       string           `json:"kind"`
	Title      string           `json:"title" binding:"required"`
	Detail     string           `json:"detail"`
	AgentAddr  string           `json:"agentAddr"`
	ReceiptRef string           `json:"receiptRef"`
	OccurredAt *time.Time       `json:"occurredAt"`
}

// RecordIncident handles POST /compliance/:scope/incidents
func (h *Handler) RecordIncident(c *gin.Context) {
	scope, ok := resolveScope(c)
	if !ok {
		return
	}
	var req recordIncidentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	if severityOrder(req.Severity) < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "severity must be info|warning|critical"})
		return
	}
	var occurred time.Time
	if req.OccurredAt != nil {
		occurred = *req.OccurredAt
	}
	inc, err := h.service.RecordFromAlert(c.Request.Context(), IncidentInput{
		Scope:      scope,
		Source:     req.Source,
		Kind:       req.Kind,
		Severity:   req.Severity,
		AgentAddr:  req.AgentAddr,
		Title:      req.Title,
		Detail:     req.Detail,
		ReceiptRef: req.ReceiptRef,
		OccurredAt: occurred,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Failed to record incident"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"incident": inc})
}

// ackReq is the body shape for AcknowledgeIncident.
type ackReq struct {
	Actor string `json:"actor" binding:"required"`
	Note  string `json:"note"`
}

// AcknowledgeIncident handles POST /compliance/incidents/:id/ack
func (h *Handler) AcknowledgeIncident(c *gin.Context) {
	var req ackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	id := c.Param("id")
	ctx := c.Request.Context()

	existing, err := h.service.GetIncident(ctx, id)
	if err != nil {
		if errors.Is(err, ErrIncidentNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Incident not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Failed to retrieve incident"})
		return
	}
	if tenantID := auth.GetTenantID(c); tenantID != "" && !strings.EqualFold(tenantID, existing.Scope) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "Incident does not belong to authenticated tenant.",
		})
		return
	}

	if err := h.service.AcknowledgeIncident(ctx, id, req.Actor, req.Note); err != nil {
		if errors.Is(err, ErrIncidentNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Incident not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Failed to acknowledge incident"})
		return
	}
	inc, err := h.service.GetIncident(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Failed to retrieve incident"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"incident": inc})
}

// ListControls handles GET /compliance/controls
func (h *Handler) ListControls(c *gin.Context) {
	controls, err := h.service.ListControls(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Failed to list controls"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"controls": controls,
		"count":    len(controls),
	})
}

// upsertControlReq is the body shape for UpsertControl. ID is taken from the URL.
type upsertControlReq struct {
	Title    string        `json:"title" binding:"required"`
	Group    string        `json:"group"`
	Status   ControlStatus `json:"status" binding:"required"`
	Evidence string        `json:"evidence"`
}

// UpsertControl handles PUT /compliance/controls/:id
//
// Controls are global (not tenant-scoped) so writes require platform admin
// credentials. Use ListControls (GET /compliance/controls) for read access.
func (h *Handler) UpsertControl(c *gin.Context) {
	if !auth.IsAdminRequest(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "Admin access required to modify controls.",
		})
		return
	}
	var req upsertControlReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	ctrl := Control{
		ID:       ControlID(c.Param("id")),
		Title:    req.Title,
		Group:    req.Group,
		Status:   req.Status,
		Evidence: req.Evidence,
	}
	if err := h.service.RegisterControl(c.Request.Context(), ctrl); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Failed to register control"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"control": ctrl})
}
