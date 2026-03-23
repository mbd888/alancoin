package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// setupHandlerTest creates a Manager backed by MemoryStore, generates one key
// for agent 0xAgent1, and returns a Handler plus the raw key and key metadata.
func setupHandlerTest(t *testing.T) (*Handler, *Manager, string, *APIKey) {
	t.Helper()
	store := NewMemoryStore()
	mgr := NewManager(store)
	rawKey, key, err := mgr.GenerateKey(context.Background(), "0xAgent1", "Primary")
	if err != nil {
		t.Fatalf("setupHandlerTest: GenerateKey failed: %v", err)
	}
	h := NewHandler(mgr)
	return h, mgr, rawKey, key
}

// setAuthContext sets the API key in the gin context so handlers see the
// request as authenticated.
func setAuthContext(c *gin.Context, key *APIKey) {
	c.Set(ContextKeyAPIKey, key)
	c.Set(ContextKeyAgentAddr, key.AgentAddr)
}

// parseJSON is a test helper that unmarshals the recorder body into a map.
func parseJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("Failed to parse response JSON: %v\nbody: %s", err, w.Body.String())
	}
	return m
}

// -----------------------------------------------------------------------
// NewHandler
// -----------------------------------------------------------------------

func TestNewHandler(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store)
	h := NewHandler(mgr)
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	if h.manager != mgr {
		t.Error("NewHandler did not set the manager field")
	}
}

// -----------------------------------------------------------------------
// Info handler
// -----------------------------------------------------------------------

func TestInfo_ReturnsAuthInfo(t *testing.T) {
	h, _, _, _ := setupHandlerTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/auth/info", nil)

	h.Info(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	resp := parseJSON(t, w)
	if resp["type"] != "api_key" {
		t.Errorf("Expected type 'api_key', got %v", resp["type"])
	}
	if resp["header"] == nil {
		t.Error("Expected header field in response")
	}
	if resp["publicEndpoints"] == nil {
		t.Error("Expected publicEndpoints field in response")
	}
	if resp["protectedEndpoints"] == nil {
		t.Error("Expected protectedEndpoints field in response")
	}
}

// -----------------------------------------------------------------------
// ListKeys handler
// -----------------------------------------------------------------------

func TestListKeys_Success(t *testing.T) {
	h, mgr, _, key := setupHandlerTest(t)

	// Create a second key for the same agent
	_, _, _ = mgr.GenerateKey(context.Background(), "0xAgent1", "Secondary")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/auth/keys", nil)
	setAuthContext(c, key)

	h.ListKeys(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d. body: %s", w.Code, w.Body.String())
	}

	resp := parseJSON(t, w)
	count := resp["count"].(float64)
	if count != 2 {
		t.Errorf("Expected 2 keys, got %v", count)
	}

	keys := resp["keys"].([]interface{})
	if len(keys) != 2 {
		t.Errorf("Expected 2 keys in array, got %d", len(keys))
	}

	// Verify hashes are not exposed
	for _, k := range keys {
		km := k.(map[string]interface{})
		if _, hasHash := km["hash"]; hasHash {
			t.Error("Key hash should not be exposed in ListKeys response")
		}
		if _, hasID := km["id"]; !hasID {
			t.Error("Key should have 'id' field")
		}
		if _, hasName := km["name"]; !hasName {
			t.Error("Key should have 'name' field")
		}
	}
}

func TestListKeys_Unauthorized(t *testing.T) {
	h, _, _, _ := setupHandlerTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/auth/keys", nil)
	// No auth context set

	h.ListKeys(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["error"] != "unauthorized" {
		t.Errorf("Expected error 'unauthorized', got %v", resp["error"])
	}
}

func TestListKeys_NoKeysForAgent(t *testing.T) {
	h, _, _, _ := setupHandlerTest(t)

	// Use a key from a different agent that has no other keys
	otherKey := &APIKey{
		ID:        "ak_other",
		AgentAddr: "0xnokeys",
		Name:      "Other",
		CreatedAt: time.Now(),
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/auth/keys", nil)
	setAuthContext(c, otherKey)

	h.ListKeys(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	resp := parseJSON(t, w)
	count := resp["count"].(float64)
	if count != 0 {
		t.Errorf("Expected 0 keys, got %v", count)
	}
}

// -----------------------------------------------------------------------
// CreateKey handler
// -----------------------------------------------------------------------

func TestCreateKey_Success_WithName(t *testing.T) {
	h, _, _, key := setupHandlerTest(t)

	body, _ := json.Marshal(CreateKeyRequest{Name: "My new key"})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/auth/keys", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuthContext(c, key)

	h.CreateKey(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d. body: %s", w.Code, w.Body.String())
	}

	resp := parseJSON(t, w)
	if resp["apiKey"] == nil {
		t.Error("Expected apiKey in response")
	}
	if resp["keyId"] == nil {
		t.Error("Expected keyId in response")
	}
	if resp["name"] != "My new key" {
		t.Errorf("Expected name 'My new key', got %v", resp["name"])
	}
	if resp["warning"] == nil {
		t.Error("Expected warning in response")
	}
}

func TestCreateKey_Success_DefaultName(t *testing.T) {
	h, _, _, key := setupHandlerTest(t)

	// Send empty body (no name)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/auth/keys", nil)
	setAuthContext(c, key)

	h.CreateKey(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d. body: %s", w.Code, w.Body.String())
	}

	resp := parseJSON(t, w)
	if resp["name"] != "Additional key" {
		t.Errorf("Expected default name 'Additional key', got %v", resp["name"])
	}
}

func TestCreateKey_Unauthorized(t *testing.T) {
	h, _, _, _ := setupHandlerTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/auth/keys", nil)

	h.CreateKey(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", w.Code)
	}
}

// -----------------------------------------------------------------------
// RevokeKey handler
// -----------------------------------------------------------------------

func TestRevokeKey_Success(t *testing.T) {
	h, mgr, _, key := setupHandlerTest(t)

	// Create a second key to revoke
	_, secondKey, _ := mgr.GenerateKey(context.Background(), "0xAgent1", "To revoke")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/v1/auth/keys/"+secondKey.ID, nil)
	c.Params = gin.Params{{Key: "keyId", Value: secondKey.ID}}
	setAuthContext(c, key)

	h.RevokeKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d. body: %s", w.Code, w.Body.String())
	}

	resp := parseJSON(t, w)
	if resp["message"] != "Key revoked" {
		t.Errorf("Expected 'Key revoked', got %v", resp["message"])
	}
	if resp["keyId"] != secondKey.ID {
		t.Errorf("Expected keyId %s, got %v", secondKey.ID, resp["keyId"])
	}
}

func TestRevokeKey_CannotRevokeSelf(t *testing.T) {
	h, _, _, key := setupHandlerTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/v1/auth/keys/"+key.ID, nil)
	c.Params = gin.Params{{Key: "keyId", Value: key.ID}}
	setAuthContext(c, key)

	h.RevokeKey(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400, got %d. body: %s", w.Code, w.Body.String())
	}

	resp := parseJSON(t, w)
	if resp["error"] != "cannot_revoke_current" {
		t.Errorf("Expected error 'cannot_revoke_current', got %v", resp["error"])
	}
}

func TestRevokeKey_NotFound(t *testing.T) {
	h, _, _, key := setupHandlerTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/v1/auth/keys/ak_nonexistent", nil)
	c.Params = gin.Params{{Key: "keyId", Value: "ak_nonexistent"}}
	setAuthContext(c, key)

	h.RevokeKey(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected 404, got %d. body: %s", w.Code, w.Body.String())
	}

	resp := parseJSON(t, w)
	if resp["error"] != "key_not_found" {
		t.Errorf("Expected error 'key_not_found', got %v", resp["error"])
	}
}

func TestRevokeKey_Unauthorized(t *testing.T) {
	h, _, _, _ := setupHandlerTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/v1/auth/keys/ak_test", nil)
	c.Params = gin.Params{{Key: "keyId", Value: "ak_test"}}

	h.RevokeKey(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", w.Code)
	}
}

func TestRevokeKey_AlreadyRevoked(t *testing.T) {
	h, mgr, _, key := setupHandlerTest(t)

	// Create and revoke a key
	_, secondKey, _ := mgr.GenerateKey(context.Background(), "0xAgent1", "Already revoked")
	_ = mgr.RevokeKey(context.Background(), secondKey.ID, "0xAgent1")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/v1/auth/keys/"+secondKey.ID, nil)
	c.Params = gin.Params{{Key: "keyId", Value: secondKey.ID}}
	setAuthContext(c, key)

	h.RevokeKey(c)

	// The handler calls manager.RevokeKey which finds the key (it still exists),
	// sets revoked=true again, and Update succeeds. So this returns 200.
	// This is idempotent behavior.
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 for already-revoked key (idempotent), got %d. body: %s", w.Code, w.Body.String())
	}
}

// -----------------------------------------------------------------------
// RegenerateKey handler
// -----------------------------------------------------------------------

func TestRegenerateKey_Success(t *testing.T) {
	h, mgr, _, key := setupHandlerTest(t)

	// Create a second key to regenerate
	_, secondKey, _ := mgr.GenerateKey(context.Background(), "0xAgent1", "To regenerate")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/auth/keys/"+secondKey.ID+"/regenerate", nil)
	c.Params = gin.Params{{Key: "keyId", Value: secondKey.ID}}
	setAuthContext(c, key)

	h.RegenerateKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d. body: %s", w.Code, w.Body.String())
	}

	resp := parseJSON(t, w)
	if resp["apiKey"] == nil {
		t.Error("Expected apiKey in response")
	}
	if resp["keyId"] == nil {
		t.Error("Expected new keyId in response")
	}
	if resp["oldKeyId"] != secondKey.ID {
		t.Errorf("Expected oldKeyId %s, got %v", secondKey.ID, resp["oldKeyId"])
	}
	if resp["warning"] == nil {
		t.Error("Expected warning in response")
	}
}

func TestRegenerateKey_RevocationFails_KeyNotFound(t *testing.T) {
	h, _, _, key := setupHandlerTest(t)

	// Try to regenerate a key that doesn't exist — revocation step fails
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/auth/keys/ak_nonexistent/regenerate", nil)
	c.Params = gin.Params{{Key: "keyId", Value: "ak_nonexistent"}}
	setAuthContext(c, key)

	h.RegenerateKey(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Expected 500, got %d. body: %s", w.Code, w.Body.String())
	}

	resp := parseJSON(t, w)
	if resp["error"] != "revocation_failed" {
		t.Errorf("Expected error 'revocation_failed', got %v", resp["error"])
	}
}

func TestRegenerateKey_Unauthorized(t *testing.T) {
	h, _, _, _ := setupHandlerTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/auth/keys/ak_test/regenerate", nil)
	c.Params = gin.Params{{Key: "keyId", Value: "ak_test"}}

	h.RegenerateKey(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", w.Code)
	}
}

// -----------------------------------------------------------------------
// GetCurrentAgent handler
// -----------------------------------------------------------------------

func TestGetCurrentAgent_Success(t *testing.T) {
	h, _, _, key := setupHandlerTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/auth/me", nil)
	setAuthContext(c, key)

	h.GetCurrentAgent(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d. body: %s", w.Code, w.Body.String())
	}

	resp := parseJSON(t, w)
	if resp["agentAddress"] != key.AgentAddr {
		t.Errorf("Expected agentAddress %s, got %v", key.AgentAddr, resp["agentAddress"])
	}
	if resp["keyId"] != key.ID {
		t.Errorf("Expected keyId %s, got %v", key.ID, resp["keyId"])
	}
	if resp["keyName"] != key.Name {
		t.Errorf("Expected keyName %s, got %v", key.Name, resp["keyName"])
	}
}

func TestGetCurrentAgent_Unauthorized(t *testing.T) {
	h, _, _, _ := setupHandlerTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/auth/me", nil)

	h.GetCurrentAgent(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", w.Code)
	}
}

// -----------------------------------------------------------------------
// Manager.Store() accessor
// -----------------------------------------------------------------------

func TestManagerStore_ReturnsStore(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store)
	got := mgr.Store()
	if got != store {
		t.Error("Store() should return the same store that was passed to NewManager")
	}
}

// -----------------------------------------------------------------------
// MemoryStore.Delete()
// -----------------------------------------------------------------------

func TestMemoryStore_Delete_ExistingKey(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	key := &APIKey{
		ID:        "ak_todelete",
		Hash:      "somehash",
		AgentAddr: "0xagent",
		Name:      "Delete me",
		CreatedAt: time.Now(),
	}
	if err := store.Create(ctx, key); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify key exists
	keys, _ := store.GetByAgent(ctx, "0xagent")
	if len(keys) != 1 {
		t.Fatalf("Expected 1 key before delete, got %d", len(keys))
	}

	// Delete
	if err := store.Delete(ctx, "ak_todelete"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify key is gone
	keys, _ = store.GetByAgent(ctx, "0xagent")
	if len(keys) != 0 {
		t.Errorf("Expected 0 keys after delete, got %d", len(keys))
	}
}

func TestMemoryStore_Delete_NonExistentKey(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Deleting a key that doesn't exist should not error
	if err := store.Delete(ctx, "ak_nonexistent"); err != nil {
		t.Errorf("Delete of non-existent key should not error, got: %v", err)
	}
}

// -----------------------------------------------------------------------
// GetTenantID helper
// -----------------------------------------------------------------------

func TestGetTenantID_Present(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(ContextKeyTenantID, "ten_abc")

	tid := GetTenantID(c)
	if tid != "ten_abc" {
		t.Errorf("Expected 'ten_abc', got %q", tid)
	}
}

func TestGetTenantID_Missing(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	tid := GetTenantID(c)
	if tid != "" {
		t.Errorf("Expected empty string when tenant ID not set, got %q", tid)
	}
}

func TestGetTenantID_WrongType(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(ContextKeyTenantID, 12345) // wrong type

	tid := GetTenantID(c)
	if tid != "" {
		t.Errorf("Expected empty string for wrong type, got %q", tid)
	}
}

// -----------------------------------------------------------------------
// IsAdminRequest helper
// -----------------------------------------------------------------------

func TestIsAdminRequest_Production_CorrectSecret(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "mysecret")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/admin/test", nil)
	c.Request.Header.Set("X-Admin-Secret", "mysecret")

	if !IsAdminRequest(c) {
		t.Error("Expected IsAdminRequest to return true with correct secret")
	}
}

func TestIsAdminRequest_Production_WrongSecret(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "mysecret")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/admin/test", nil)
	c.Request.Header.Set("X-Admin-Secret", "wrongsecret")

	if IsAdminRequest(c) {
		t.Error("Expected IsAdminRequest to return false with wrong secret")
	}
}

func TestIsAdminRequest_Production_MissingHeader(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "mysecret")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/admin/test", nil)

	if IsAdminRequest(c) {
		t.Error("Expected IsAdminRequest to return false without header")
	}
}

func TestIsAdminRequest_NoSecret_NoDemoMode(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "")
	t.Setenv("DEMO_MODE", "")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/admin/test", nil)

	if IsAdminRequest(c) {
		t.Error("Expected IsAdminRequest to return false when no secret and no demo mode")
	}
}

func TestIsAdminRequest_DemoMode_Authenticated(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "")
	t.Setenv("DEMO_MODE", "true")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/admin/test", nil)
	c.Set(ContextKeyAPIKey, &APIKey{AgentAddr: "0xagent"})

	if !IsAdminRequest(c) {
		t.Error("Expected IsAdminRequest to return true in demo mode with auth")
	}
}

func TestIsAdminRequest_DemoMode_Unauthenticated(t *testing.T) {
	t.Setenv("ADMIN_SECRET", "")
	t.Setenv("DEMO_MODE", "true")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/admin/test", nil)

	if IsAdminRequest(c) {
		t.Error("Expected IsAdminRequest to return false in demo mode without auth")
	}
}

// -----------------------------------------------------------------------
// Middleware sets TenantID when key has one
// -----------------------------------------------------------------------

func TestMiddleware_SetsTenantID(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store)
	ctx := context.Background()

	rawKey, key, err := mgr.GenerateKey(ctx, "0xTenantAgent", "tenant-key")
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	// Set tenant ID on the key directly in the store
	key.TenantID = "ten_123"
	_ = store.Update(ctx, key)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	c.Request.Header.Set("Authorization", rawKey)

	Middleware(mgr)(c)

	tid := GetTenantID(c)
	if tid != "ten_123" {
		t.Errorf("Expected tenant ID 'ten_123', got %q", tid)
	}
}
