package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupMiddlewareTest() (*Manager, string, *APIKey) {
	store := NewMemoryStore()
	mgr := NewManager(store)
	rawKey, key, _ := mgr.GenerateKey(context.Background(), "0xAgentABC", "test-key")
	return mgr, rawKey, key
}

// --- Middleware() ---

func TestMiddleware_ValidKey_SetsContext(t *testing.T) {
	mgr, rawKey, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	c.Request.Header.Set("Authorization", rawKey)

	handler := Middleware(mgr)
	handler(c)

	// Should set agent address
	addr, exists := c.Get(ContextKeyAgentAddr)
	if !exists {
		t.Fatal("Expected agent addr to be set in context")
	}
	if addr.(string) != "0xagentabc" {
		t.Errorf("Expected 0xagentabc, got %s", addr.(string))
	}

	// Should set API key object
	key, exists := c.Get(ContextKeyAPIKey)
	if !exists {
		t.Fatal("Expected API key to be set in context")
	}
	if key.(*APIKey).Name != "test-key" {
		t.Errorf("Expected key name 'test-key', got %s", key.(*APIKey).Name)
	}
}

func TestMiddleware_ValidKeyViaXAPIKey(t *testing.T) {
	mgr, rawKey, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	c.Request.Header.Set("X-API-Key", rawKey)

	Middleware(mgr)(c)

	if _, exists := c.Get(ContextKeyAgentAddr); !exists {
		t.Error("Expected agent addr set via X-API-Key header")
	}
}

func TestMiddleware_InvalidKey_DoesNotAbort(t *testing.T) {
	mgr, _, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	c.Request.Header.Set("Authorization", "sk_invalidkey000000000000000000000000000000000000000000000000000000")

	Middleware(mgr)(c)

	// Should NOT set context
	if _, exists := c.Get(ContextKeyAPIKey); exists {
		t.Error("Expected API key NOT to be set for invalid key")
	}

	// Should NOT abort (soft auth)
	if c.IsAborted() {
		t.Error("Middleware should not abort on invalid key")
	}
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 (pass-through), got %d", w.Code)
	}
}

func TestMiddleware_MissingHeader_PassesThrough(t *testing.T) {
	mgr, _, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)

	Middleware(mgr)(c)

	if _, exists := c.Get(ContextKeyAPIKey); exists {
		t.Error("Expected no API key in context when header missing")
	}
	if c.IsAborted() {
		t.Error("Middleware should not abort when header missing")
	}
}

func TestMiddleware_RevokedKey_DoesNotSetContext(t *testing.T) {
	mgr, rawKey, key := setupMiddlewareTest()
	_ = mgr.RevokeKey(context.Background(), key.ID, "0xAgentABC")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	c.Request.Header.Set("Authorization", rawKey)

	Middleware(mgr)(c)

	if _, exists := c.Get(ContextKeyAPIKey); exists {
		t.Error("Expected revoked key NOT to set context")
	}
	if c.IsAborted() {
		t.Error("Middleware should not abort on revoked key")
	}
}

// --- RequireAuth() ---

func TestRequireAuth_NoAuth_Returns401(t *testing.T) {
	mgr, _, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)

	RequireAuth(mgr)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", w.Code)
	}
	if !c.IsAborted() {
		t.Error("Expected request to be aborted")
	}
}

func TestRequireAuth_WithAuth_Passes(t *testing.T) {
	mgr, _, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	c.Set(ContextKeyAPIKey, &APIKey{AgentAddr: "0xagentabc"})

	RequireAuth(mgr)(c)

	if c.IsAborted() {
		t.Error("Expected request to pass through when authenticated")
	}
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
}

// --- RequireOwnership() ---

func TestRequireOwnership_NoAuth_Returns401(t *testing.T) {
	mgr, _, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/agents/0xagentabc", nil)
	c.Params = gin.Params{{Key: "address", Value: "0xAgentABC"}}

	RequireOwnership(mgr, "address")(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", w.Code)
	}
}

func TestRequireOwnership_WrongAgent_Returns403(t *testing.T) {
	mgr, _, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/agents/0xOTHER", nil)
	c.Params = gin.Params{{Key: "address", Value: "0xOTHER"}}
	c.Set(ContextKeyAPIKey, &APIKey{AgentAddr: "0xagentabc"})

	RequireOwnership(mgr, "address")(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403, got %d", w.Code)
	}
}

func TestRequireOwnership_CorrectAgent_Passes(t *testing.T) {
	mgr, _, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/agents/0xagentabc", nil)
	c.Params = gin.Params{{Key: "address", Value: "0xagentabc"}}
	c.Set(ContextKeyAPIKey, &APIKey{AgentAddr: "0xagentabc"})

	RequireOwnership(mgr, "address")(c)

	if c.IsAborted() {
		t.Error("Expected request to pass when owner matches")
	}
}

func TestRequireOwnership_CaseInsensitive(t *testing.T) {
	mgr, _, _ := setupMiddlewareTest()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/agents/0xAGENTABC", nil)
	c.Params = gin.Params{{Key: "address", Value: "0xAGENTABC"}}
	c.Set(ContextKeyAPIKey, &APIKey{AgentAddr: "0xagentabc"})

	RequireOwnership(mgr, "address")(c)

	if c.IsAborted() {
		t.Error("Expected case-insensitive match to pass")
	}
}

// --- RequireAdmin() ---

func TestRequireAdmin_DemoMode_AuthenticatedPasses(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/admin/deposit", nil)
	c.Set(ContextKeyAPIKey, &APIKey{AgentAddr: "0xagentabc"})

	RequireAdmin()(c)

	if c.IsAborted() {
		t.Error("Expected authenticated request to pass in demo mode")
	}
}

func TestRequireAdmin_DemoMode_UnauthenticatedRejects(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/admin/deposit", nil)

	RequireAdmin()(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 in demo mode without auth, got %d", w.Code)
	}
}

func TestRequireAdmin_Production_CorrectSecret(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "supersecret123")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/admin/deposit", nil)
	c.Request.Header.Set("X-Admin-Secret", "supersecret123")

	RequireAdmin()(c)

	if c.IsAborted() {
		t.Error("Expected correct admin secret to pass")
	}
}

func TestRequireAdmin_Production_WrongSecret(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "supersecret123")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/admin/deposit", nil)
	c.Request.Header.Set("X-Admin-Secret", "wrongsecret")

	RequireAdmin()(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for wrong secret, got %d", w.Code)
	}
}

func TestRequireAdmin_Production_MissingHeader(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "supersecret123")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/admin/deposit", nil)

	RequireAdmin()(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for missing admin header, got %d", w.Code)
	}
}

// --- Helper functions ---

func TestGetAPIKey_Present(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	expected := &APIKey{ID: "ak_test", AgentAddr: "0xabc"}
	c.Set(ContextKeyAPIKey, expected)

	key, ok := GetAPIKey(c)
	if !ok {
		t.Fatal("Expected GetAPIKey to return true")
	}
	if key.ID != "ak_test" {
		t.Errorf("Expected key ID ak_test, got %s", key.ID)
	}
}

func TestGetAPIKey_Missing(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	_, ok := GetAPIKey(c)
	if ok {
		t.Error("Expected GetAPIKey to return false when no key in context")
	}
}

func TestGetAuthenticatedAgent_Present(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(ContextKeyAgentAddr, "0xagentabc")

	addr := GetAuthenticatedAgent(c)
	if addr != "0xagentabc" {
		t.Errorf("Expected 0xagentabc, got %s", addr)
	}
}

func TestGetAuthenticatedAgent_Missing(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	addr := GetAuthenticatedAgent(c)
	if addr != "" {
		t.Errorf("Expected empty string, got %s", addr)
	}
}

func TestIsAuthenticated_True(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(ContextKeyAPIKey, &APIKey{})

	if !IsAuthenticated(c) {
		t.Error("Expected IsAuthenticated to return true")
	}
}

func TestIsAuthenticated_False(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	if IsAuthenticated(c) {
		t.Error("Expected IsAuthenticated to return false")
	}
}
