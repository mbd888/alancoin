package webhooks

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/idgen"
)

// Handler provides HTTP endpoints for webhook management
type Handler struct {
	store      Store
	dispatcher *Dispatcher
}

// NewHandler creates a new webhook handler
func NewHandler(store Store, dispatcher *Dispatcher) *Handler {
	return &Handler{
		store:      store,
		dispatcher: dispatcher,
	}
}

// RegisterRoutes sets up webhook routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/agents/:address/webhooks", h.CreateWebhook)
	r.GET("/agents/:address/webhooks", h.ListWebhooks)
	r.DELETE("/agents/:address/webhooks/:webhookId", h.DeleteWebhook)
}

// CreateWebhookRequest for creating a webhook subscription
type CreateWebhookRequest struct {
	URL    string   `json:"url" binding:"required"`
	Events []string `json:"events" binding:"required"`
}

// CreateWebhook handles POST /agents/:address/webhooks
func (h *Handler) CreateWebhook(c *gin.Context) {
	address := c.Param("address")

	var req CreateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Validate webhook URL to prevent SSRF attacks
	if err := validateWebhookURL(req.URL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_url",
			"message": err.Error(),
		})
		return
	}

	// Validate events against known types
	validEvents := map[EventType]bool{
		EventPaymentReceived:   true,
		EventPaymentSent:       true,
		EventSessionKeyUsed:    true,
		EventSessionKeyCreated: true,
		EventSessionKeyRevoked: true,
		EventBalanceDeposit:    true,
		EventBalanceWithdraw:   true,
	}
	events := make([]EventType, 0, len(req.Events))
	for _, e := range req.Events {
		et := EventType(e)
		if !validEvents[et] {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_event_type",
				"message": fmt.Sprintf("Unknown event type: %s", e),
			})
			return
		}
		events = append(events, et)
	}

	// Generate ID and secret
	id := idgen.WithPrefix("wh_")
	secret := generateSecret()

	sub := &Subscription{
		ID:        id,
		AgentAddr: address,
		URL:       req.URL,
		Secret:    secret,
		Events:    events,
		Active:    true,
		CreatedAt: time.Now(),
	}

	if err := h.store.Create(c.Request.Context(), sub); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "create_failed",
			"message": "Failed to create webhook",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"webhook": gin.H{
			"id":        sub.ID,
			"url":       sub.URL,
			"events":    sub.Events,
			"active":    sub.Active,
			"createdAt": sub.CreatedAt,
		},
		"secret": secret, // Only shown once!
		"usage": gin.H{
			"signature": "Verify with HMAC-SHA256(payload, secret)",
			"header":    "X-Alancoin-Signature",
		},
	})
}

// ListWebhooks handles GET /agents/:address/webhooks
func (h *Handler) ListWebhooks(c *gin.Context) {
	address := c.Param("address")

	subs, err := h.store.GetByAgent(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "list_failed",
			"message": "Failed to list webhooks",
		})
		return
	}

	// Don't expose secrets
	webhooks := make([]gin.H, len(subs))
	for i, sub := range subs {
		webhooks[i] = gin.H{
			"id":          sub.ID,
			"url":         sub.URL,
			"events":      sub.Events,
			"active":      sub.Active,
			"createdAt":   sub.CreatedAt,
			"lastSuccess": sub.LastSuccess,
			"lastError":   sub.LastError,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"webhooks": webhooks,
	})
}

// DeleteWebhook handles DELETE /agents/:address/webhooks/:webhookId
func (h *Handler) DeleteWebhook(c *gin.Context) {
	address := c.Param("address")
	webhookID := c.Param("webhookId")

	// Verify the webhook belongs to this agent before deleting
	webhook, err := h.store.Get(c.Request.Context(), webhookID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "Webhook not found",
		})
		return
	}
	if webhook.AgentAddr != address {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "Webhook does not belong to this agent",
		})
		return
	}

	if err := h.store.Delete(c.Request.Context(), webhookID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "delete_failed",
			"message": "Failed to delete webhook",
		})
		return
	}

	c.Status(http.StatusNoContent)
}

// validateWebhookURL checks that a webhook URL is safe to call.
// Blocks private/internal IPs to prevent SSRF attacks.
func validateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format")
	}

	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("URL scheme must be http or https")
	}

	if u.Host == "" {
		return fmt.Errorf("URL must have a host")
	}

	host := u.Hostname()

	// Block known internal hostnames
	blocked := []string{"localhost", "metadata.google.internal"}
	for _, b := range blocked {
		if strings.EqualFold(host, b) {
			return fmt.Errorf("URL host is not allowed")
		}
	}

	// Block private/loopback/link-local IP addresses
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("URL must not point to a private or internal IP address")
		}
	}

	// Also resolve the hostname and check resolved IPs
	if ip == nil {
		ips, err := net.LookupHost(host)
		if err != nil {
			return fmt.Errorf("cannot resolve URL host: %s", host)
		}
		for _, ipStr := range ips {
			resolved := net.ParseIP(ipStr)
			if resolved != nil && (resolved.IsLoopback() || resolved.IsPrivate() || resolved.IsLinkLocalUnicast() || resolved.IsUnspecified()) {
				return fmt.Errorf("URL host resolves to a private or internal IP address")
			}
		}
	}

	return nil
}

func generateSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
