package auth

import (
	"crypto/subtle"
	"log/slog"
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
	// ContextKeyTenantID is the key for storing tenant ID from API key
	ContextKeyTenantID = "authTenantID"
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
				if key.TenantID != "" {
					c.Set(ContextKeyTenantID, key.TenantID)
				}
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

// GetTenantID returns the tenant ID from context (empty if no tenant).
func GetTenantID(c *gin.Context) string {
	v, _ := c.Get(ContextKeyTenantID)
	s, _ := v.(string)
	return s
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
// Demo mode requires explicit DEMO_MODE=true to allow any authenticated request.
func RequireAdmin() gin.HandlerFunc {
	adminSecret := os.Getenv("ADMIN_SECRET")
	demoMode := os.Getenv("DEMO_MODE") == "true"
	if adminSecret == "" && !demoMode {
		slog.Error("ADMIN_SECRET is not set and DEMO_MODE is not enabled. Admin endpoints will reject all requests. Set ADMIN_SECRET for production or DEMO_MODE=true for development.")
	}
	return func(c *gin.Context) {
		if adminSecret == "" {
			if !demoMode {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error":   "forbidden",
					"message": "Admin access is disabled. Set ADMIN_SECRET or enable DEMO_MODE.",
				})
				return
			}
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

// IsAdminRequest checks if the request carries a valid admin secret.
// Uses constant-time comparison to prevent timing attacks.
// Returns false if ADMIN_SECRET is not set (unless DEMO_MODE is enabled).
func IsAdminRequest(c *gin.Context) bool {
	provided := c.GetHeader("X-Admin-Secret")
	if provided == "" {
		return false
	}
	adminSecret := os.Getenv("ADMIN_SECRET")
	if adminSecret == "" {
		// Demo mode: any non-empty admin header is accepted
		return os.Getenv("DEMO_MODE") == "true"
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(adminSecret)) == 1
}
