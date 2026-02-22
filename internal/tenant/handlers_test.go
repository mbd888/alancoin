package tenant

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
	os.Setenv("DEMO_MODE", "true") // Allow admin requests in tests
}

// --- Test Setup ---

func setupTestHandler() (*Handler, *MemoryStore, *auth.Manager, *registry.MemoryStore) {
	tenantStore := NewMemoryStore()
	authStore := auth.NewMemoryStore()
	authMgr := auth.NewManager(authStore)
	registryStore := registry.NewMemoryStore()

	handler := NewHandler(tenantStore, authMgr, registryStore)

	// Create a default tenant for testing
	_ = tenantStore.Create(context.Background(), &Tenant{
		ID:        "ten_1",
		Name:      "Test Tenant",
		Slug:      "test-tenant",
		Plan:      PlanFree,
		Status:    StatusActive,
		Settings:  DefaultSettingsForPlan(PlanFree),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	return handler, tenantStore, authMgr, registryStore
}

// makeContext creates a gin.Context for direct handler testing with proper auth.
func makeContext(t *testing.T, method, path string, body []byte, tenantParam, callerTenantID string, isAdmin bool, extraParams ...gin.Param) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	params := gin.Params{{Key: "id", Value: tenantParam}}
	params = append(params, extraParams...)
	c.Params = params

	if body != nil {
		c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
	} else {
		c.Request = httptest.NewRequest(method, path, nil)
	}

	if isAdmin {
		c.Request.Header.Set("X-Admin-Secret", "test-secret")
	} else if callerTenantID != "" {
		c.Set(auth.ContextKeyTenantID, callerTenantID)
	}

	return w, c
}

// --- CreateTenant (Admin-only, uses RegisterAdminRoutes — router is fine here) ---

func TestCreateTenant_Success(t *testing.T) {
	handler, store, _, _ := setupTestHandler()

	reqBody := map[string]string{
		"name": "New Tenant",
		"slug": "new-tenant",
		"plan": "starter",
	}
	body, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)
	handler.RegisterAdminRoutes(router.Group("/"))
	req := httptest.NewRequest("POST", "/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	tenant := resp["tenant"].(map[string]interface{})
	assert.Equal(t, "New Tenant", tenant["name"])
	assert.Equal(t, "new-tenant", tenant["slug"])
	assert.Equal(t, "starter", tenant["plan"])
	assert.NotEmpty(t, resp["apiKey"])
	assert.NotEmpty(t, resp["keyId"])

	created, err := store.Get(context.Background(), tenant["id"].(string))
	require.NoError(t, err)
	assert.Equal(t, "New Tenant", created.Name)
}

func TestCreateTenant_DuplicateSlug(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	reqBody := map[string]string{
		"name": "Another Tenant",
		"slug": "test-tenant", // duplicate
		"plan": "free",
	}
	body, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)
	handler.RegisterAdminRoutes(router.Group("/"))
	req := httptest.NewRequest("POST", "/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "slug_taken", resp["error"])
}

func TestCreateTenant_InvalidSlug(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	tests := []struct {
		name string
		slug string
	}{
		{"too short", "ab"},
		{"starts with hyphen", "-invalid"},
		{"ends with hyphen", "invalid-"},
		{"special chars", "inva!id"},
		{"spaces", "invalid slug"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqBody := map[string]string{
				"name": "Test",
				"slug": tt.slug,
				"plan": "free",
			}
			body, _ := json.Marshal(reqBody)

			w := httptest.NewRecorder()
			_, router := gin.CreateTestContext(w)
			handler.RegisterAdminRoutes(router.Group("/"))
			req := httptest.NewRequest("POST", "/tenants", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			assert.Equal(t, "invalid_slug", resp["error"])
		})
	}
}

func TestCreateTenant_InvalidPlan(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	reqBody := map[string]string{
		"name": "Test Tenant",
		"slug": "valid-slug",
		"plan": "premium", // invalid plan
	}
	body, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)
	handler.RegisterAdminRoutes(router.Group("/"))
	req := httptest.NewRequest("POST", "/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "invalid_plan", resp["error"])
}

func TestCreateTenant_DefaultsToFreePlan(t *testing.T) {
	handler, store, _, _ := setupTestHandler()

	reqBody := map[string]string{
		"name": "Free Tenant",
		"slug": "free-tenant",
	}
	body, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)
	handler.RegisterAdminRoutes(router.Group("/"))
	req := httptest.NewRequest("POST", "/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	tenant := resp["tenant"].(map[string]interface{})

	created, _ := store.Get(context.Background(), tenant["id"].(string))
	assert.Equal(t, PlanFree, created.Plan)
	assert.Equal(t, 3, created.Settings.MaxAgents)
}

// --- GetTenant ---

func TestGetTenant_Success(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	w, c := makeContext(t, "GET", "/tenants/ten_1", nil, "ten_1", "ten_1", false)
	handler.GetTenant(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	tenant := resp["tenant"].(map[string]interface{})
	assert.Equal(t, "ten_1", tenant["id"])
	assert.Equal(t, "Test Tenant", tenant["name"])
}

func TestGetTenant_NotOwner_Forbidden(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	w, c := makeContext(t, "GET", "/tenants/ten_1", nil, "ten_1", "ten_other", false)
	handler.GetTenant(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "forbidden", resp["error"])
}

func TestGetTenant_AdminCanAccessAny(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	w, c := makeContext(t, "GET", "/tenants/ten_1", nil, "ten_1", "", true)
	handler.GetTenant(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetTenant_NotFound(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	w, c := makeContext(t, "GET", "/tenants/ten_nonexistent", nil, "ten_nonexistent", "ten_nonexistent", false)
	handler.GetTenant(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- UpdateTenant ---

func TestUpdateTenant_Success(t *testing.T) {
	handler, store, _, _ := setupTestHandler()

	reqBody := map[string]interface{}{"name": "Updated Name"}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "PATCH", "/tenants/ten_1", body, "ten_1", "ten_1", false)
	handler.UpdateTenant(c)

	assert.Equal(t, http.StatusOK, w.Code)
	updated, _ := store.Get(context.Background(), "ten_1")
	assert.Equal(t, "Updated Name", updated.Name)
}

func TestUpdateTenant_OwnerCannotChangePlan(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	reqBody := map[string]string{"plan": "enterprise"}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "PATCH", "/tenants/ten_1", body, "ten_1", "ten_1", false)
	handler.UpdateTenant(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "forbidden", resp["error"])
	assert.Contains(t, resp["message"], "plan changes require admin")
}

func TestUpdateTenant_AdminCanChangePlan(t *testing.T) {
	handler, store, _, _ := setupTestHandler()

	reqBody := map[string]string{"plan": "enterprise"}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "PATCH", "/tenants/ten_1", body, "ten_1", "", true)
	handler.UpdateTenant(c)

	assert.Equal(t, http.StatusOK, w.Code)
	updated, _ := store.Get(context.Background(), "ten_1")
	assert.Equal(t, PlanEnterprise, updated.Plan)
	assert.Equal(t, 5000, updated.Settings.RateLimitRPM)
}

func TestUpdateTenant_OwnerCanUpdateAllowedOrigins(t *testing.T) {
	handler, store, _, _ := setupTestHandler()

	reqBody := map[string]interface{}{
		"settings": map[string]interface{}{
			"allowedOrigins": []string{"https://example.com", "https://app.example.com"},
		},
	}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "PATCH", "/tenants/ten_1", body, "ten_1", "ten_1", false)
	handler.UpdateTenant(c)

	assert.Equal(t, http.StatusOK, w.Code)
	updated, _ := store.Get(context.Background(), "ten_1")
	assert.Equal(t, []string{"https://example.com", "https://app.example.com"}, updated.Settings.AllowedOrigins)
}

// --- RegisterAgent (CRITICAL: Plan enforcement) ---

func TestRegisterAgent_Success(t *testing.T) {
	handler, store, _, registryStore := setupTestHandler()

	reqBody := map[string]string{
		"address":     "0xaaaa1234567890123456789012345678901234aa",
		"name":        "Test Agent",
		"description": "A test agent",
	}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "POST", "/tenants/ten_1/agents", body, "ten_1", "ten_1", false)
	handler.RegisterAgent(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	agent := resp["agent"].(map[string]interface{})
	assert.Equal(t, "0xaaaa1234567890123456789012345678901234aa", agent["address"])
	assert.Equal(t, "Test Agent", agent["name"])
	assert.NotEmpty(t, resp["apiKey"])

	regAgent, err := registryStore.GetAgent(context.Background(), "0xaaaa1234567890123456789012345678901234aa")
	require.NoError(t, err)
	assert.Equal(t, "Test Agent", regAgent.Name)

	count, _ := store.CountAgents(context.Background(), "ten_1")
	assert.Equal(t, 1, count)
}

func TestRegisterAgent_PlanEnforcement_FreePlanLimitReached(t *testing.T) {
	handler, store, _, registryStore := setupTestHandler()

	ctx := context.Background()

	// Free plan has MaxAgents = 3. Register 3 agents first.
	for i := 1; i <= 3; i++ {
		addr := "0xaaaa" + string(rune('0'+i)) + "00000000000000000000000000000000000"
		_ = registryStore.CreateAgent(ctx, &registry.Agent{
			Address: addr,
			Name:    "Agent " + string(rune('0'+i)),
		})
		store.BindAgent(addr, "ten_1")
	}

	count, _ := store.CountAgents(ctx, "ten_1")
	require.Equal(t, 3, count)

	// Attempt 4th agent — should fail
	reqBody := map[string]string{
		"address": "0xbbbb400000000000000000000000000000000000",
		"name":    "Fourth Agent",
	}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "POST", "/tenants/ten_1/agents", body, "ten_1", "ten_1", false)
	handler.RegisterAgent(c)

	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "max_agents", resp["error"])

	// Agent should NOT be registered
	_, err := registryStore.GetAgent(ctx, "0xbbbb400000000000000000000000000000000000")
	assert.Error(t, err)
}

func TestRegisterAgent_PlanEnforcement_EnterpriseUnlimited(t *testing.T) {
	handler, store, _, _ := setupTestHandler()

	ctx := context.Background()

	// Upgrade to Enterprise (MaxAgents = 0 = unlimited)
	tenant, _ := store.Get(ctx, "ten_1")
	tenant.Plan = PlanEnterprise
	tenant.Settings = DefaultSettingsForPlan(PlanEnterprise)
	_ = store.Update(ctx, tenant)

	// Register 10 agents using hex digits (0-9, a) for address uniqueness.
	hexDigits := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	for i, d := range hexDigits {
		addr := "0xeeee" + d + "00000000000000000000000000000000000"
		reqBody := map[string]string{
			"address": addr,
			"name":    "Agent " + d,
		}
		body, _ := json.Marshal(reqBody)

		w, c := makeContext(t, "POST", "/tenants/ten_1/agents", body, "ten_1", "ten_1", false)
		handler.RegisterAgent(c)

		assert.Equal(t, http.StatusCreated, w.Code, "Agent %d should be created", i+1)
	}

	count, _ := store.CountAgents(ctx, "ten_1")
	assert.Equal(t, 10, count)
}

func TestRegisterAgent_InvalidAddress(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	reqBody := map[string]string{
		"address": "not-an-eth-address",
		"name":    "Test Agent",
	}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "POST", "/tenants/ten_1/agents", body, "ten_1", "ten_1", false)
	handler.RegisterAgent(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "invalid_address", resp["error"])
}

func TestRegisterAgent_DuplicateAgent(t *testing.T) {
	handler, _, _, regStore := setupTestHandler()

	addr := "0xaaaa1234567890123456789012345678901234aa"
	_ = regStore.CreateAgent(context.Background(), &registry.Agent{
		Address: addr,
		Name:    "Existing Agent",
	})

	reqBody := map[string]string{
		"address": addr,
		"name":    "Duplicate Agent",
	}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "POST", "/tenants/ten_1/agents", body, "ten_1", "ten_1", false)
	handler.RegisterAgent(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "agent_exists", resp["error"])
}

func TestRegisterAgent_NotOwner_Forbidden(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	reqBody := map[string]string{
		"address": "0xaaaa1234567890123456789012345678901234aa",
		"name":    "Test Agent",
	}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "POST", "/tenants/ten_1/agents", body, "ten_1", "ten_other", false)
	handler.RegisterAgent(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- ListAgents ---

func TestListAgents_Success(t *testing.T) {
	handler, store, _, _ := setupTestHandler()

	store.BindAgent("0xdddd000000000000000000000000000000000001", "ten_1")
	store.BindAgent("0xdddd000000000000000000000000000000000002", "ten_1")

	w, c := makeContext(t, "GET", "/tenants/ten_1/agents", nil, "ten_1", "ten_1", false)
	handler.ListAgents(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	agents := resp["agents"].([]interface{})
	assert.Equal(t, 2, len(agents))
	assert.Equal(t, float64(2), resp["count"])
}

func TestListAgents_NotOwner_Forbidden(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	w, c := makeContext(t, "GET", "/tenants/ten_1/agents", nil, "ten_1", "ten_other", false)
	handler.ListAgents(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- CreateKey / ListKeys / RevokeKey ---

func TestCreateKey_Success(t *testing.T) {
	handler, store, _, _ := setupTestHandler()
	store.BindAgent("0xcccc000000000000000000000000000000000001", "ten_1")

	reqBody := map[string]string{
		"agentAddr": "0xcccc000000000000000000000000000000000001",
		"name":      "Test Key",
	}
	body, _ := json.Marshal(reqBody)

	w, c := makeContext(t, "POST", "/tenants/ten_1/keys", body, "ten_1", "ten_1", false)
	handler.CreateKey(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NotEmpty(t, resp["apiKey"])
	assert.NotEmpty(t, resp["keyId"])
	assert.Equal(t, "Test Key", resp["name"])
}

func TestListKeys_Success(t *testing.T) {
	handler, store, authMgr, _ := setupTestHandler()

	ctx := context.Background()
	store.BindAgent("0xdddd000000000000000000000000000000000001", "ten_1")

	_, key, _ := authMgr.GenerateKey(ctx, "0xdddd000000000000000000000000000000000001", "Agent Key")
	key.TenantID = "ten_1"
	_ = authMgr.Store().Update(ctx, key)

	w, c := makeContext(t, "GET", "/tenants/ten_1/keys", nil, "ten_1", "ten_1", false)
	handler.ListKeys(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	keys := resp["keys"].([]interface{})
	assert.Equal(t, 1, len(keys))
}

func TestRevokeKey_Success(t *testing.T) {
	handler, store, authMgr, _ := setupTestHandler()
	store.BindAgent("0xdddd000000000000000000000000000000000001", "ten_1")

	ctx := context.Background()
	_, key, _ := authMgr.GenerateKey(ctx, "0xdddd000000000000000000000000000000000001", "Key to Revoke")
	key.TenantID = "ten_1"
	_ = authMgr.Store().Update(ctx, key)

	w, c := makeContext(t, "DELETE", "/tenants/ten_1/keys/"+key.ID, nil,
		"ten_1", "ten_1", false,
		gin.Param{Key: "keyId", Value: key.ID})
	handler.RevokeKey(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "key revoked", resp["message"])

	keys, _ := authMgr.Store().GetByAgent(ctx, "0xdddd000000000000000000000000000000000001")
	for _, k := range keys {
		if k.ID == key.ID {
			assert.True(t, k.Revoked)
		}
	}
}

// --- GetBilling ---

func TestGetBilling_NotConfigured(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	w, c := makeContext(t, "GET", "/tenants/ten_1/billing", nil, "ten_1", "ten_1", false)
	handler.GetBilling(c)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestGetBilling_Success(t *testing.T) {
	handler, _, _, _ := setupTestHandler()

	mockBilling := &mockBillingProvider{
		summary: &BillingSummary{
			TotalRequests:   100,
			SettledRequests: 90,
			SettledVolume:   "1000.000000",
			FeesCollected:   "5.000000",
		},
	}
	handler.WithBilling(mockBilling)

	w, c := makeContext(t, "GET", "/tenants/ten_1/billing", nil, "ten_1", "ten_1", false)
	handler.GetBilling(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	billing := resp["billing"].(map[string]interface{})
	assert.Equal(t, float64(100), billing["totalRequests"])
	assert.Equal(t, "1000.000000", billing["settledVolume"])
	assert.Equal(t, "free", billing["plan"])
}

// --- Mock BillingProvider ---

type mockBillingProvider struct {
	summary *BillingSummary
	err     error
}

func (m *mockBillingProvider) GetBillingSummary(ctx context.Context, tenantID string) (*BillingSummary, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.summary, nil
}
