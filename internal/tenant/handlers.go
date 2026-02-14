package tenant

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/registry"
	"github.com/mbd888/alancoin/internal/validation"
)

var validSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

// Handler provides HTTP endpoints for tenant management.
type Handler struct {
	store    Store
	authMgr  *auth.Manager
	registry registry.Store
}

// NewHandler creates a new tenant handler.
func NewHandler(store Store, authMgr *auth.Manager, reg registry.Store) *Handler {
	return &Handler{store: store, authMgr: authMgr, registry: reg}
}

// RegisterAdminRoutes sets up the admin-only tenant creation route.
func (h *Handler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.POST("/tenants", h.CreateTenant)
}

// RegisterProtectedRoutes sets up tenant routes that require API key auth.
// Get/Update are accessible to both admins and tenant owners (checked per-handler).
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.GET("/tenants/:id", h.GetTenant)
	r.PATCH("/tenants/:id", h.UpdateTenant)
	r.GET("/tenants/:id/agents", h.ListAgents)
	r.POST("/tenants/:id/agents", h.RegisterAgent)
	r.POST("/tenants/:id/keys", h.CreateKey)
	r.GET("/tenants/:id/keys", h.ListKeys)
	r.DELETE("/tenants/:id/keys/:keyId", h.RevokeKey)
}

// ---------- Admin endpoints ----------

// CreateTenant handles POST /v1/tenants (admin only).
func (h *Handler) CreateTenant(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
		Slug string `json:"slug" binding:"required"`
		Plan Plan   `json:"plan"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "name and slug required"})
		return
	}

	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if !validSlug.MatchString(req.Slug) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_slug",
			"message": "slug must be 3-64 lowercase alphanumeric/hyphens, start/end with alphanumeric",
		})
		return
	}

	if req.Plan == "" {
		req.Plan = PlanFree
	}
	if !ValidPlan(req.Plan) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_plan", "message": "unknown plan"})
		return
	}

	now := time.Now()
	t := &Tenant{
		ID:        idgen.WithPrefix("ten_"),
		Name:      validation.SanitizeString(req.Name, 200),
		Slug:      req.Slug,
		Plan:      req.Plan,
		Status:    StatusActive,
		Settings:  DefaultSettingsForPlan(req.Plan),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := h.store.Create(c.Request.Context(), t); err != nil {
		if err == ErrSlugTaken {
			c.JSON(http.StatusConflict, gin.H{"error": "slug_taken", "message": "slug already in use"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "failed to create tenant"})
		return
	}

	// Generate an admin API key scoped to this tenant.
	// We use a placeholder agent address for the tenant admin key.
	adminAddr := "tenant-admin:" + t.ID
	rawKey, keyInfo, err := h.authMgr.GenerateKey(c.Request.Context(), adminAddr, "Tenant admin key")
	if err != nil {
		c.JSON(http.StatusCreated, gin.H{
			"tenant":  t,
			"warning": "Tenant created but admin key generation failed. Use admin API to create keys.",
		})
		return
	}

	// Bind the key to this tenant.
	keyInfo.TenantID = t.ID
	_ = h.authMgr.Store().Update(c.Request.Context(), keyInfo)

	c.JSON(http.StatusCreated, gin.H{
		"tenant":  t,
		"apiKey":  rawKey,
		"keyId":   keyInfo.ID,
		"warning": "Store this API key securely. It will not be shown again.",
	})
}

// GetTenant handles GET /v1/tenants/:id
func (h *Handler) GetTenant(c *gin.Context) {
	id := c.Param("id")

	t, err := h.store.Get(c.Request.Context(), id)
	if err != nil {
		if err == ErrTenantNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "tenant not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	// Non-admin callers must own the tenant.
	if !isAdmin(c) {
		callerTenant := auth.GetTenantID(c)
		if callerTenant != t.ID {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "not your tenant"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"tenant": t})
}

// UpdateTenant handles PATCH /v1/tenants/:id
func (h *Handler) UpdateTenant(c *gin.Context) {
	id := c.Param("id")

	t, err := h.store.Get(c.Request.Context(), id)
	if err != nil {
		if err == ErrTenantNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "tenant not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	if !isAdmin(c) {
		callerTenant := auth.GetTenantID(c)
		if callerTenant != t.ID {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "not your tenant"})
			return
		}
	}

	var req struct {
		Name     *string   `json:"name"`
		Plan     *Plan     `json:"plan"`
		Settings *Settings `json:"settings"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "invalid body"})
		return
	}

	if req.Name != nil {
		t.Name = validation.SanitizeString(*req.Name, 200)
	}
	if req.Plan != nil {
		if !ValidPlan(*req.Plan) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_plan", "message": "unknown plan"})
			return
		}
		// Only admins can change plan.
		if !isAdmin(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "plan changes require admin"})
			return
		}
		t.Plan = *req.Plan
		t.Settings = DefaultSettingsForPlan(*req.Plan)
	}
	if req.Settings != nil {
		// Allow tenant owner to set allowed_origins only; RPM/agents/budget come from plan.
		t.Settings.AllowedOrigins = req.Settings.AllowedOrigins
	}
	t.UpdatedAt = time.Now()

	if err := h.store.Update(c.Request.Context(), t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "failed to update tenant"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"tenant": t})
}

// ---------- Tenant-scoped endpoints ----------

// ListAgents handles GET /v1/tenants/:id/agents
func (h *Handler) ListAgents(c *gin.Context) {
	tenantID := c.Param("id")
	if !h.requireTenantOwnership(c, tenantID) {
		return
	}

	agents, err := h.store.ListAgents(c.Request.Context(), tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"agents": agents, "count": len(agents)})
}

// RegisterAgent handles POST /v1/tenants/:id/agents — register an agent under a tenant.
func (h *Handler) RegisterAgent(c *gin.Context) {
	tenantID := c.Param("id")
	if !h.requireTenantOwnership(c, tenantID) {
		return
	}

	var req struct {
		Address     string                 `json:"address" binding:"required"`
		Name        string                 `json:"name" binding:"required"`
		Description string                 `json:"description"`
		Metadata    map[string]interface{} `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "address and name required"})
		return
	}

	if !validation.IsValidEthAddress(req.Address) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_address", "message": "invalid Ethereum address"})
		return
	}

	// Check agent cap.
	t, err := h.store.Get(c.Request.Context(), tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}
	if t.Settings.MaxAgents > 0 {
		count, err := h.store.CountAgents(c.Request.Context(), tenantID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
			return
		}
		if count >= t.Settings.MaxAgents {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "max_agents",
				"message": "maximum agents reached for your plan",
				"limit":   t.Settings.MaxAgents,
			})
			return
		}
	}

	// Register the agent in the registry.
	agent := &registry.Agent{
		Address:     req.Address,
		Name:        validation.SanitizeString(req.Name, 200),
		Description: validation.SanitizeString(req.Description, 1000),
		Metadata:    req.Metadata,
	}
	if err := h.registry.CreateAgent(c.Request.Context(), agent); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "agent_exists", "message": "agent already registered"})
		return
	}

	// Generate a tenant-scoped API key for this agent.
	rawKey, keyInfo, err := h.authMgr.GenerateKey(c.Request.Context(), agent.Address, "Tenant agent key")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "agent registered but key generation failed"})
		return
	}

	keyInfo.TenantID = tenantID
	_ = h.authMgr.Store().Update(c.Request.Context(), keyInfo)

	// For MemoryStore, also track the binding.
	if ms, ok := h.store.(*MemoryStore); ok {
		ms.BindAgent(agent.Address, tenantID)
	}

	c.JSON(http.StatusCreated, gin.H{
		"agent":   agent,
		"apiKey":  rawKey,
		"keyId":   keyInfo.ID,
		"warning": "Store this API key securely. It will not be shown again.",
	})
}

// CreateKey handles POST /v1/tenants/:id/keys — generate a tenant-scoped API key.
func (h *Handler) CreateKey(c *gin.Context) {
	tenantID := c.Param("id")
	if !h.requireTenantOwnership(c, tenantID) {
		return
	}

	var req struct {
		AgentAddr string `json:"agentAddr" binding:"required"`
		Name      string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "agentAddr required"})
		return
	}
	if req.Name == "" {
		req.Name = "Tenant key"
	}

	rawKey, keyInfo, err := h.authMgr.GenerateKey(c.Request.Context(), req.AgentAddr, req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "failed to create key"})
		return
	}

	keyInfo.TenantID = tenantID
	_ = h.authMgr.Store().Update(c.Request.Context(), keyInfo)

	c.JSON(http.StatusCreated, gin.H{
		"apiKey":  rawKey,
		"keyId":   keyInfo.ID,
		"name":    keyInfo.Name,
		"warning": "Store this key securely. It will not be shown again.",
	})
}

// ListKeys handles GET /v1/tenants/:id/keys
func (h *Handler) ListKeys(c *gin.Context) {
	tenantID := c.Param("id")
	if !h.requireTenantOwnership(c, tenantID) {
		return
	}

	// List agents first, then gather all keys.
	agents, err := h.store.ListAgents(c.Request.Context(), tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	var allKeys []gin.H
	for _, addr := range agents {
		keys, err := h.authMgr.ListKeys(c.Request.Context(), addr)
		if err != nil {
			continue
		}
		for _, k := range keys {
			if k.TenantID == tenantID {
				allKeys = append(allKeys, gin.H{
					"id":        k.ID,
					"agentAddr": k.AgentAddr,
					"name":      k.Name,
					"createdAt": k.CreatedAt,
					"lastUsed":  k.LastUsed,
					"revoked":   k.Revoked,
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"keys": allKeys, "count": len(allKeys)})
}

// RevokeKey handles DELETE /v1/tenants/:id/keys/:keyId
func (h *Handler) RevokeKey(c *gin.Context) {
	tenantID := c.Param("id")
	if !h.requireTenantOwnership(c, tenantID) {
		return
	}

	keyID := c.Param("keyId")

	// We need to find the key's agent address. List all tenant agents and search.
	agents, err := h.store.ListAgents(c.Request.Context(), tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	for _, addr := range agents {
		if err := h.authMgr.RevokeKey(c.Request.Context(), keyID, addr); err == nil {
			c.JSON(http.StatusOK, gin.H{"message": "key revoked", "keyId": keyID})
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "key_not_found", "message": "key not found in this tenant"})
}

// ---------- helpers ----------

// requireTenantOwnership checks if the caller owns the given tenant.
// Returns false (and sends error response) if not authorized.
func (h *Handler) requireTenantOwnership(c *gin.Context, tenantID string) bool {
	if isAdmin(c) {
		return true
	}
	callerTenant := auth.GetTenantID(c)
	if callerTenant != tenantID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "not your tenant"})
		return false
	}
	return true
}

// isAdmin returns true if the request was authenticated via admin secret.
func isAdmin(c *gin.Context) bool {
	// The RequireAdmin middleware sets this header check, but doesn't set a context value.
	// We check for the X-Admin-Secret header presence as a proxy.
	return c.GetHeader("X-Admin-Secret") != ""
}
