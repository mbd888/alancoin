package billing

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"

	"github.com/mbd888/alancoin/internal/tenant"
)

// WebhookHandler processes Stripe webhook events.
type WebhookHandler struct {
	webhookSecret string
	tenantStore   tenant.Store
	priceToPlan   map[string]tenant.Plan // Stripe Price ID → plan
	logger        *slog.Logger
}

// NewWebhookHandler creates a handler for Stripe webhook events.
// priceToPlan maps Stripe Price IDs back to plan names for subscription sync.
func NewWebhookHandler(webhookSecret string, tenantStore tenant.Store, priceToPlan map[string]tenant.Plan, logger *slog.Logger) *WebhookHandler {
	return &WebhookHandler{
		webhookSecret: webhookSecret,
		tenantStore:   tenantStore,
		priceToPlan:   priceToPlan,
		logger:        logger,
	}
}

// RegisterRoute registers the Stripe webhook endpoint.
// This route has NO auth middleware — Stripe authenticates via signature.
func (h *WebhookHandler) RegisterRoute(r *gin.RouterGroup) {
	r.POST("/webhooks/stripe", h.HandleWebhook)
}

// HandleWebhook processes incoming Stripe webhook events.
func (h *WebhookHandler) HandleWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 65536))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read_failed"})
		return
	}

	event, err := webhook.ConstructEvent(body, c.GetHeader("Stripe-Signature"), h.webhookSecret)
	if err != nil {
		h.logger.Warn("stripe webhook signature verification failed", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_signature"})
		return
	}

	h.logger.Info("stripe webhook received", "type", event.Type, "id", event.ID)

	switch event.Type {
	case "customer.subscription.updated":
		h.handleSubscriptionUpdated(c, &event)
	case "customer.subscription.deleted":
		h.handleSubscriptionDeleted(c, &event)
	case "invoice.payment_failed":
		h.handlePaymentFailed(c, &event)
	case "invoice.paid":
		h.handleInvoicePaid(c, &event)
	default:
		// Acknowledge events we don't handle.
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}

func (h *WebhookHandler) handleSubscriptionUpdated(c *gin.Context, event *stripe.Event) {
	customerID, _ := event.Data.Object["customer"].(string)
	if customerID == "" {
		return
	}

	t := h.findTenantByCustomer(c, customerID)
	if t == nil {
		return
	}

	// Extract the current price ID from the subscription items.
	var priceID string
	if items, ok := event.Data.Object["items"].(map[string]interface{}); ok {
		if data, ok := items["data"].([]interface{}); ok && len(data) > 0 {
			if item, ok := data[0].(map[string]interface{}); ok {
				if price, ok := item["price"].(map[string]interface{}); ok {
					priceID, _ = price["id"].(string)
				}
			}
		}
	}

	if priceID == "" {
		h.logger.Warn("stripe subscription updated: could not extract price ID",
			"tenant", t.ID, "event", event.ID)
		return
	}

	newPlan, ok := h.priceToPlan[priceID]
	if !ok {
		h.logger.Warn("stripe subscription updated: unknown price ID",
			"tenant", t.ID, "priceId", priceID)
		return
	}

	// Sync status from Stripe.
	stripeStatus, _ := event.Data.Object["status"].(string)
	switch stripeStatus {
	case "active":
		t.Status = tenant.StatusActive
	case "past_due":
		t.Status = tenant.StatusSuspended
	case "canceled", "unpaid":
		t.Status = tenant.StatusCancelled
	}

	t.Plan = newPlan
	t.Settings = tenant.DefaultSettingsForPlan(newPlan)
	t.UpdatedAt = time.Now()

	if err := h.tenantStore.Update(c.Request.Context(), t); err != nil {
		h.logger.Error("failed to update tenant on subscription change",
			"tenant", t.ID, "newPlan", newPlan, "error", err)
		return
	}

	h.logger.Info("tenant plan synced from stripe subscription update",
		"tenant", t.ID, "plan", newPlan, "stripeStatus", stripeStatus)
}

func (h *WebhookHandler) handleSubscriptionDeleted(c *gin.Context, event *stripe.Event) {
	customerID, _ := event.Data.Object["customer"].(string)
	if customerID == "" {
		return
	}

	t := h.findTenantByCustomer(c, customerID)
	if t == nil {
		return
	}

	t.Status = tenant.StatusCancelled
	t.StripeSubscriptionID = ""
	t.UpdatedAt = time.Now()
	if err := h.tenantStore.Update(c.Request.Context(), t); err != nil {
		h.logger.Error("failed to update tenant on subscription deletion",
			"tenant", t.ID, "error", err)
	}
}

func (h *WebhookHandler) handlePaymentFailed(c *gin.Context, event *stripe.Event) {
	customerID, _ := event.Data.Object["customer"].(string)
	if customerID == "" {
		return
	}

	t := h.findTenantByCustomer(c, customerID)
	if t == nil {
		return
	}

	t.Status = tenant.StatusSuspended
	t.UpdatedAt = time.Now()
	if err := h.tenantStore.Update(c.Request.Context(), t); err != nil {
		h.logger.Error("failed to suspend tenant on payment failure",
			"tenant", t.ID, "error", err)
	}
	h.logger.Warn("tenant suspended due to payment failure", "tenant", t.ID)
}

func (h *WebhookHandler) handleInvoicePaid(c *gin.Context, event *stripe.Event) {
	customerID, _ := event.Data.Object["customer"].(string)
	if customerID == "" {
		return
	}

	t := h.findTenantByCustomer(c, customerID)
	if t == nil {
		return
	}

	if t.Status == tenant.StatusSuspended {
		t.Status = tenant.StatusActive
		t.UpdatedAt = time.Now()
		if err := h.tenantStore.Update(c.Request.Context(), t); err != nil {
			h.logger.Error("failed to reactivate tenant on payment",
				"tenant", t.ID, "error", err)
		}
		h.logger.Info("tenant reactivated after payment", "tenant", t.ID)
	}
}

// findTenantByCustomer looks up a tenant by Stripe customer ID.
func (h *WebhookHandler) findTenantByCustomer(c *gin.Context, customerID string) *tenant.Tenant {
	t, err := h.tenantStore.GetByStripeCustomerID(c.Request.Context(), customerID)
	if err != nil {
		h.logger.Warn("stripe webhook: tenant not found for customer", "customer", customerID, "error", err)
		return nil
	}
	return t
}
