package billing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/tenant"
)

// Handler provides HTTP endpoints for subscription management.
type Handler struct {
	provider    Provider
	tenantStore tenant.Store
}

// NewHandler creates a billing HTTP handler.
func NewHandler(provider Provider, tenantStore tenant.Store) *Handler {
	return &Handler{provider: provider, tenantStore: tenantStore}
}

// RegisterRoutes registers billing endpoints under the tenant-scoped group.
// Expects routes like /v1/tenants/:id/billing/...
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/tenants/:id/billing/subscribe", h.Subscribe)
	r.POST("/tenants/:id/billing/upgrade", h.Upgrade)
	r.POST("/tenants/:id/billing/cancel", h.Cancel)
	r.GET("/tenants/:id/billing/subscription", h.GetSubscription)
}

// Subscribe handles POST /v1/tenants/:id/billing/subscribe
func (h *Handler) Subscribe(c *gin.Context) {
	tenantID := c.Param("id")
	if !h.requireOwnership(c, tenantID) {
		return
	}

	t, err := h.tenantStore.Get(c.Request.Context(), tenantID)
	if err != nil {
		if err == tenant.ErrTenantNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "tenant not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	if t.Plan == tenant.PlanFree {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_plan", "message": "free plan does not require a subscription"})
		return
	}

	if t.StripeSubscriptionID != "" {
		c.JSON(http.StatusConflict, gin.H{"error": "already_subscribed", "message": "tenant already has an active subscription"})
		return
	}

	// Create Stripe customer if not yet created. Persist immediately so we
	// don't orphan a Stripe customer object if the subscription step fails.
	if t.StripeCustomerID == "" {
		customerID, err := h.provider.CreateCustomer(c.Request.Context(), t.ID, t.Name, "")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "billing_error", "message": "failed to create billing customer"})
			return
		}
		t.StripeCustomerID = customerID
		if err := h.tenantStore.Update(c.Request.Context(), t); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "failed to save billing customer"})
			return
		}
	}

	subID, err := h.provider.CreateSubscription(c.Request.Context(), t.StripeCustomerID, t.Plan)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "billing_error", "message": "failed to create subscription"})
		return
	}

	t.StripeSubscriptionID = subID
	if err := h.tenantStore.Update(c.Request.Context(), t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "subscription created but failed to save to tenant"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"subscriptionId": subID,
		"plan":           t.Plan,
		"status":         "active",
	})
}

// Upgrade handles POST /v1/tenants/:id/billing/upgrade
func (h *Handler) Upgrade(c *gin.Context) {
	tenantID := c.Param("id")
	if !h.requireOwnership(c, tenantID) {
		return
	}

	var req struct {
		Plan tenant.Plan `json:"plan" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "plan required"})
		return
	}
	if !tenant.ValidPlan(req.Plan) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_plan", "message": "unknown plan"})
		return
	}

	t, err := h.tenantStore.Get(c.Request.Context(), tenantID)
	if err != nil {
		if err == tenant.ErrTenantNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "tenant not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	if t.StripeSubscriptionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no_subscription", "message": "tenant has no active subscription"})
		return
	}

	if err := h.provider.UpdateSubscription(c.Request.Context(), t.StripeSubscriptionID, req.Plan); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "billing_error", "message": "failed to update subscription"})
		return
	}

	// Update local plan + settings.
	t.Plan = req.Plan
	t.Settings = tenant.DefaultSettingsForPlan(req.Plan)
	if err := h.tenantStore.Update(c.Request.Context(), t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "subscription updated but failed to save plan change"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"subscriptionId": t.StripeSubscriptionID,
		"plan":           t.Plan,
		"message":        "plan upgraded with proration",
	})
}

// Cancel handles POST /v1/tenants/:id/billing/cancel
func (h *Handler) Cancel(c *gin.Context) {
	tenantID := c.Param("id")
	if !h.requireOwnership(c, tenantID) {
		return
	}

	t, err := h.tenantStore.Get(c.Request.Context(), tenantID)
	if err != nil {
		if err == tenant.ErrTenantNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "tenant not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	if t.StripeSubscriptionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no_subscription", "message": "tenant has no active subscription"})
		return
	}

	if err := h.provider.CancelSubscription(c.Request.Context(), t.StripeSubscriptionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "billing_error", "message": "failed to cancel subscription"})
		return
	}

	t.StripeSubscriptionID = ""
	t.Plan = tenant.PlanFree
	t.Settings = tenant.DefaultSettingsForPlan(tenant.PlanFree)
	t.Status = tenant.StatusCancelled
	if err := h.tenantStore.Update(c.Request.Context(), t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "subscription cancelled but failed to update tenant"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "subscription cancelled"})
}

// GetSubscription handles GET /v1/tenants/:id/billing/subscription
func (h *Handler) GetSubscription(c *gin.Context) {
	tenantID := c.Param("id")
	if !h.requireOwnership(c, tenantID) {
		return
	}

	t, err := h.tenantStore.Get(c.Request.Context(), tenantID)
	if err != nil {
		if err == tenant.ErrTenantNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "tenant not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": err.Error()})
		return
	}

	if t.StripeSubscriptionID == "" {
		c.JSON(http.StatusOK, gin.H{
			"subscription": nil,
			"plan":         t.Plan,
			"message":      "no active subscription",
		})
		return
	}

	sub, err := h.provider.GetSubscription(c.Request.Context(), t.StripeSubscriptionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "billing_error", "message": "failed to fetch subscription"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"subscription": sub,
		"plan":         t.Plan,
	})
}

func (h *Handler) requireOwnership(c *gin.Context, tenantID string) bool {
	callerTenant := auth.GetTenantID(c)
	if callerTenant != tenantID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "not your tenant"})
		return false
	}
	return true
}
