package policy

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/validation"
)

// Handler provides HTTP endpoints for policy CRUD.
type Handler struct {
	store Store
}

// NewHandler creates a new policy handler.
func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

// RegisterRoutes sets up policy routes under the tenant-protected group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/tenants/:id/policies", h.Create)
	r.GET("/tenants/:id/policies", h.List)
	r.GET("/tenants/:id/policies/:policyId", h.Get)
	r.PUT("/tenants/:id/policies/:policyId", h.Update)
	r.DELETE("/tenants/:id/policies/:policyId", h.Delete)
}

// Create handles POST /v1/tenants/:id/policies
func (h *Handler) Create(c *gin.Context) {
	tenantID := c.Param("id")
	if !requireTenantOwnership(c, tenantID) {
		return
	}

	var req struct {
		Name            string     `json:"name" binding:"required"`
		Rules           []Rule     `json:"rules" binding:"required"`
		Priority        int        `json:"priority"`
		Enabled         *bool      `json:"enabled"`
		EnforcementMode string     `json:"enforcementMode"`
		ShadowExpiresAt *time.Time `json:"shadowExpiresAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "name and rules required"})
		return
	}

	if err := ValidateRules(req.Rules); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_rules", "message": err.Error()})
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	enforcementMode := "enforce"
	if req.EnforcementMode != "" {
		if req.EnforcementMode != "enforce" && req.EnforcementMode != "shadow" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "enforcementMode must be 'enforce' or 'shadow'"})
			return
		}
		enforcementMode = req.EnforcementMode
	}

	now := time.Now()
	var shadowExpiresAt time.Time
	if enforcementMode == "shadow" {
		maxShadow := now.Add(30 * 24 * time.Hour) // 30 days max
		if req.ShadowExpiresAt != nil && req.ShadowExpiresAt.Before(maxShadow) {
			shadowExpiresAt = *req.ShadowExpiresAt
		} else {
			shadowExpiresAt = maxShadow
		}
	}

	p := &SpendPolicy{
		ID:              idgen.WithPrefix("sp_"),
		TenantID:        tenantID,
		Name:            validation.SanitizeString(req.Name, 200),
		Rules:           req.Rules,
		Priority:        req.Priority,
		Enabled:         enabled,
		EnforcementMode: enforcementMode,
		ShadowExpiresAt: shadowExpiresAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := h.store.Create(c.Request.Context(), p); err != nil {
		if err == ErrNameTaken {
			c.JSON(http.StatusConflict, gin.H{"error": "name_taken", "message": "policy name already exists for this tenant"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "failed to create policy"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"policy": p})
}

// List handles GET /v1/tenants/:id/policies
func (h *Handler) List(c *gin.Context) {
	tenantID := c.Param("id")
	if !requireTenantOwnership(c, tenantID) {
		return
	}

	policies, err := h.store.List(c.Request.Context(), tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}
	if policies == nil {
		policies = []*SpendPolicy{}
	}
	c.JSON(http.StatusOK, gin.H{"policies": policies, "count": len(policies)})
}

// Get handles GET /v1/tenants/:id/policies/:policyId
func (h *Handler) Get(c *gin.Context) {
	tenantID := c.Param("id")
	if !requireTenantOwnership(c, tenantID) {
		return
	}

	policyID := c.Param("policyId")
	p, err := h.store.Get(c.Request.Context(), policyID)
	if err != nil {
		if err == ErrPolicyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "policy not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	if p.TenantID != tenantID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "policy not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"policy": p})
}

// Update handles PUT /v1/tenants/:id/policies/:policyId
func (h *Handler) Update(c *gin.Context) {
	tenantID := c.Param("id")
	if !requireTenantOwnership(c, tenantID) {
		return
	}

	policyID := c.Param("policyId")
	existing, err := h.store.Get(c.Request.Context(), policyID)
	if err != nil {
		if err == ErrPolicyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "policy not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	if existing.TenantID != tenantID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "policy not found"})
		return
	}

	var req struct {
		Name            *string    `json:"name"`
		Rules           []Rule     `json:"rules"`
		Priority        *int       `json:"priority"`
		Enabled         *bool      `json:"enabled"`
		EnforcementMode *string    `json:"enforcementMode"`
		ShadowExpiresAt *time.Time `json:"shadowExpiresAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "invalid body"})
		return
	}

	if req.Name != nil {
		existing.Name = validation.SanitizeString(*req.Name, 200)
	}
	if req.Rules != nil {
		if err := ValidateRules(req.Rules); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_rules", "message": err.Error()})
			return
		}
		existing.Rules = req.Rules
	}
	if req.Priority != nil {
		existing.Priority = *req.Priority
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.EnforcementMode != nil {
		if *req.EnforcementMode != "enforce" && *req.EnforcementMode != "shadow" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "enforcementMode must be 'enforce' or 'shadow'"})
			return
		}
		existing.EnforcementMode = *req.EnforcementMode
		if *req.EnforcementMode == "shadow" {
			now := time.Now()
			maxShadow := now.Add(30 * 24 * time.Hour)
			if req.ShadowExpiresAt != nil && req.ShadowExpiresAt.Before(maxShadow) {
				existing.ShadowExpiresAt = *req.ShadowExpiresAt
			} else if existing.ShadowExpiresAt.IsZero() {
				existing.ShadowExpiresAt = maxShadow
			}
		} else {
			existing.ShadowExpiresAt = time.Time{}
		}
	}
	existing.UpdatedAt = time.Now()

	if err := h.store.Update(c.Request.Context(), existing); err != nil {
		if err == ErrNameTaken {
			c.JSON(http.StatusConflict, gin.H{"error": "name_taken", "message": "policy name already exists for this tenant"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "failed to update policy"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"policy": existing})
}

// Delete handles DELETE /v1/tenants/:id/policies/:policyId
func (h *Handler) Delete(c *gin.Context) {
	tenantID := c.Param("id")
	if !requireTenantOwnership(c, tenantID) {
		return
	}

	policyID := c.Param("policyId")

	// Verify the policy belongs to this tenant before deleting.
	existing, err := h.store.Get(c.Request.Context(), policyID)
	if err != nil {
		if err == ErrPolicyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "policy not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}
	if existing.TenantID != tenantID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "policy not found"})
		return
	}

	if err := h.store.Delete(c.Request.Context(), policyID); err != nil {
		if err == ErrPolicyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "policy not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "failed to delete policy"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "policy deleted", "id": policyID})
}

// requireTenantOwnership checks if the caller owns the given tenant.
func requireTenantOwnership(c *gin.Context, tenantID string) bool {
	if auth.IsAdminRequest(c) {
		return true
	}
	callerTenant := auth.GetTenantID(c)
	if callerTenant != tenantID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "not your tenant"})
		return false
	}
	return true
}
