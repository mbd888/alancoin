package escrow

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// TemplateHandler provides HTTP endpoints for escrow templates.
type TemplateHandler struct {
	service *TemplateService
}

// NewTemplateHandler creates a new template handler.
func NewTemplateHandler(service *TemplateService) *TemplateHandler {
	return &TemplateHandler{service: service}
}

// RegisterRoutes sets up public (read-only) template routes.
func (h *TemplateHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/escrow/templates", h.ListTemplates)
	r.GET("/escrow/templates/:id", h.GetTemplate)
	r.GET("/escrow/:id/milestones", h.ListMilestones)
}

// RegisterProtectedRoutes sets up protected template routes.
func (h *TemplateHandler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/escrow/templates", h.CreateTemplate)
	r.POST("/escrow/templates/:id/instantiate", h.InstantiateTemplate)
	r.POST("/escrow/:id/milestones/:idx/release", h.ReleaseMilestone)
}

// CreateTemplate handles POST /v1/escrow/templates
func (h *TemplateHandler) CreateTemplate(c *gin.Context) {
	var req CreateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "Invalid request body"})
		return
	}

	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.CreatorAddr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "unauthorized", "message": "Authenticated agent must match creatorAddr"})
		return
	}

	tmpl, err := h.service.CreateTemplate(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "create_failed"
		switch {
		case errors.Is(err, ErrMilestonesInvalid):
			status = http.StatusBadRequest
			code = "milestones_invalid"
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"template": tmpl})
}

// GetTemplate handles GET /v1/escrow/templates/:id
func (h *TemplateHandler) GetTemplate(c *gin.Context) {
	id := c.Param("id")
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

// ListTemplates handles GET /v1/escrow/templates
func (h *TemplateHandler) ListTemplates(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}

	templates, err := h.service.ListTemplates(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"templates": templates, "count": len(templates)})
}

// InstantiateTemplate handles POST /v1/escrow/templates/:id/instantiate
func (h *TemplateHandler) InstantiateTemplate(c *gin.Context) {
	templateID := c.Param("id")

	var req InstantiateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "Invalid request body"})
		return
	}

	callerAddr := c.GetString("authAgentAddr")
	if !strings.EqualFold(callerAddr, req.BuyerAddr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "unauthorized", "message": "Authenticated agent must match buyerAddr"})
		return
	}

	escrow, milestones, err := h.service.InstantiateTemplate(c.Request.Context(), templateID, req)
	if err != nil {
		status := http.StatusInternalServerError
		code := "instantiate_failed"
		switch {
		case errors.Is(err, ErrTemplateNotFound):
			status = http.StatusNotFound
			code = "template_not_found"
		case errors.Is(err, ErrInvalidAmount):
			status = http.StatusBadRequest
			code = "invalid_amount"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"escrow": escrow, "milestones": milestones})
}

// ReleaseMilestone handles POST /v1/escrow/:id/milestones/:idx/release
func (h *TemplateHandler) ReleaseMilestone(c *gin.Context) {
	escrowID := c.Param("id")
	idxStr := c.Param("idx")

	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_index", "message": "Milestone index must be a number"})
		return
	}

	callerAddr := c.GetString("authAgentAddr")

	milestone, err := h.service.ReleaseMilestone(c.Request.Context(), escrowID, idx, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "release_failed"
		switch {
		case errors.Is(err, ErrEscrowNotFound):
			status = http.StatusNotFound
			code = "escrow_not_found"
		case errors.Is(err, ErrMilestoneNotFound):
			status = http.StatusNotFound
			code = "milestone_not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		case errors.Is(err, ErrInvalidStatus):
			status = http.StatusConflict
			code = "invalid_status"
		case errors.Is(err, ErrMilestoneAlreadyDone):
			status = http.StatusConflict
			code = "already_released"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"milestone": milestone})
}

// ListMilestones handles GET /v1/escrow/:id/milestones
func (h *TemplateHandler) ListMilestones(c *gin.Context) {
	escrowID := c.Param("id")

	milestones, err := h.service.ListMilestones(c.Request.Context(), escrowID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"milestones": milestones, "count": len(milestones)})
}
