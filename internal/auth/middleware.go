package auth

import (
	"crypto/subtle"
	"net/http"
	"os"
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
	apiKey, ok := key.(*APIKey)
	if !ok {
		return nil, false
	}
	return apiKey, true
}

// GetAuthenticatedAgent returns the authenticated agent's address
func GetAuthenticatedAgent(c *gin.Context) string {
	addr, exists := c.Get(ContextKeyAgentAddr)
	if !exists {
		return ""
	}
	s, ok := addr.(string)
	if !ok {
		return ""
	}
	return s
}

// IsAuthenticated checks if the request is authenticated
func IsAuthenticated(c *gin.Context) bool {
	_, exists := c.Get(ContextKeyAPIKey)
	return exists
}

// RequireAdmin middleware restricts access to admin endpoints.
// Checks the X-Admin-Secret header against the ADMIN_SECRET env var.
// In demo mode (no ADMIN_SECRET set), allows any authenticated request.
func RequireAdmin() gin.HandlerFunc {
	adminSecret := os.Getenv("ADMIN_SECRET")
	return func(c *gin.Context) {
		if adminSecret == "" {
			// Demo mode: allow any authenticated request
			if _, exists := c.Get(ContextKeyAPIKey); !exists {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error":   "unauthorized",
					"message": "API key required.",
				})
				return
			}
			c.Next()
			return
		}

		// Production mode: require admin secret
		provided := c.GetHeader("X-Admin-Secret")
		if provided == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "forbidden",
				"message": "Admin access required.",
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(provided), []byte(adminSecret)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "forbidden",
				"message": "Invalid admin credentials.",
			})
			return
		}

		c.Next()
	}
}
