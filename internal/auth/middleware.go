package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	// ContextKeyAPIKey is the key for storing API key in gin context
	ContextKeyAPIKey = "apiKey"
	// ContextKeyAgentAddr is the key for storing authenticated agent address
	ContextKeyAgentAddr = "authAgentAddr"
)

// Middleware extracts and validates API key from request
// Sets apiKey and authAgentAddr in context if valid
func Middleware(m *Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get API key from header
		apiKey := c.GetHeader("Authorization")
		if apiKey == "" {
			apiKey = c.GetHeader("X-API-Key")
		}

		if apiKey != "" {
			key, err := m.ValidateKey(c.Request.Context(), apiKey)
			if err == nil {
				c.Set(ContextKeyAPIKey, key)
				c.Set(ContextKeyAgentAddr, key.AgentAddr)
			}
		}

		c.Next()
	}
}

// RequireAuth middleware rejects requests without valid auth
func RequireAuth(m *Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, exists := c.Get(ContextKeyAPIKey); !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "API key required. Include 'Authorization: Bearer sk_...' header.",
			})
			return
		}
		c.Next()
	}
}

// RequireOwnership middleware requires auth AND ownership of the :address param
func RequireOwnership(m *Manager, paramName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check auth first
		key, exists := c.Get(ContextKeyAPIKey)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "API key required.",
			})
			return
		}

		// Get target address from URL param
		targetAddr := strings.ToLower(c.Param(paramName))

		// Check ownership
		apiKey, ok := key.(*APIKey)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error":   "internal_error",
				"message": "Invalid authentication state",
			})
			return
		}
		if !strings.EqualFold(apiKey.AgentAddr, targetAddr) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "forbidden",
				"message": "You do not own this agent.",
			})
			return
		}

		c.Next()
	}
}

// GetAPIKey returns the API key from context (if authenticated)
func GetAPIKey(c *gin.Context) (*APIKey, bool) {
	key, exists := c.Get(ContextKeyAPIKey)
	if !exists {
		return nil, false
	}
	return key.(*APIKey), true
}

// GetAuthenticatedAgent returns the authenticated agent's address
func GetAuthenticatedAgent(c *gin.Context) string {
	addr, exists := c.Get(ContextKeyAgentAddr)
	if !exists {
		return ""
	}
	return addr.(string)
}

// IsAuthenticated checks if the request is authenticated
func IsAuthenticated(c *gin.Context) bool {
	_, exists := c.Get(ContextKeyAPIKey)
	return exists
}
