package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for auth management
type Handler struct {
	manager *Manager
}

// NewHandler creates a new auth handler
func NewHandler(m *Manager) *Handler {
	return &Handler{manager: m}
}

// Info returns auth configuration info
func (h *Handler) Info(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"type":      "api_key",
		"header":    "Authorization: Bearer sk_...",
		"altHeader": "X-API-Key: sk_...",
		"note":      "API key is returned on agent registration. Store it securely.",
		"publicEndpoints": []string{
			"GET /v1/agents",
			"GET /v1/agents/:address",
			"GET /v1/services",
			"GET /v1/feed",
			"GET /v1/network/stats",
		},
		"protectedEndpoints": []string{
			"DELETE /v1/agents/:address",
			"POST /v1/agents/:address/services",
			"DELETE /v1/agents/:address/services/:id",
			"POST /v1/agents/:address/sessions",
		},
	})
}

// ListKeys returns API keys for the authenticated agent
func (h *Handler) ListKeys(c *gin.Context) {
	key, ok := GetAPIKey(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	keys, err := h.manager.ListKeys(c.Request.Context(), key.AgentAddr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to list keys",
		})
		return
	}

	// Don't expose hashes
	safeKeys := make([]gin.H, len(keys))
	for i, k := range keys {
		safeKeys[i] = gin.H{
			"id":        k.ID,
			"name":      k.Name,
			"createdAt": k.CreatedAt,
			"lastUsed":  k.LastUsed,
			"revoked":   k.Revoked,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"keys":  safeKeys,
		"count": len(safeKeys),
	})
}

// CreateKeyRequest is the request body for creating a key
type CreateKeyRequest struct {
	Name string `json:"name"`
}

// CreateKey creates a new API key
func (h *Handler) CreateKey(c *gin.Context) {
	key, ok := GetAPIKey(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateKeyRequest
	c.ShouldBindJSON(&req)
	if req.Name == "" {
		req.Name = "Additional key"
	}

	rawKey, newKey, err := h.manager.GenerateKey(c.Request.Context(), key.AgentAddr, req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to create key",
			"message": "Failed to create API key",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"apiKey":  rawKey,
		"keyId":   newKey.ID,
		"name":    newKey.Name,
		"warning": "Store this key securely. It will not be shown again.",
	})
}

// RevokeKey revokes an API key
func (h *Handler) RevokeKey(c *gin.Context) {
	key, ok := GetAPIKey(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	keyID := c.Param("keyId")

	// Prevent revoking current key
	if keyID == key.ID {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "cannot_revoke_current",
			"message": "Cannot revoke the key you're using",
		})
		return
	}

	if err := h.manager.RevokeKey(c.Request.Context(), keyID, key.AgentAddr); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "key_not_found",
			"message": "Key not found or already revoked",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Key revoked",
		"keyId":   keyID,
	})
}

// RegenerateKey revokes old key and creates new one
func (h *Handler) RegenerateKey(c *gin.Context) {
	key, ok := GetAPIKey(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	keyID := c.Param("keyId")

	// Revoke old key
	h.manager.RevokeKey(c.Request.Context(), keyID, key.AgentAddr)

	// Create new key
	rawKey, newKey, err := h.manager.GenerateKey(c.Request.Context(), key.AgentAddr, "Regenerated key")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to regenerate",
			"message": "Failed to regenerate API key",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"apiKey":   rawKey,
		"keyId":    newKey.ID,
		"oldKeyId": keyID,
		"warning":  "Store this key securely. It will not be shown again.",
	})
}

// GetCurrentAgent returns info about the authenticated agent
func (h *Handler) GetCurrentAgent(c *gin.Context) {
	key, ok := GetAPIKey(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"agentAddress": key.AgentAddr,
		"keyId":        key.ID,
		"keyName":      key.Name,
		"createdAt":    key.CreatedAt,
		"lastUsed":     key.LastUsed,
	})
}
