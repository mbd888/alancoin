package webhooks

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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

	// Validate events
	events := make([]EventType, len(req.Events))
	for i, e := range req.Events {
		events[i] = EventType(e)
	}

	// Generate ID and secret
	id := generateID("wh_")
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
	webhookID := c.Param("webhookId")

	if err := h.store.Delete(c.Request.Context(), webhookID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "delete_failed",
			"message": "Failed to delete webhook",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "deleted",
		"message": "Webhook deleted",
	})
}

func generateID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func generateSecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
