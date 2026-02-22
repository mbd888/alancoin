package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/gateway"
	"github.com/mbd888/alancoin/internal/tenant"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
	os.Setenv("DEMO_MODE", "true") // Allow admin requests in tests
}

// setupTestHandler creates a handler with in-memory stores populated with test data.
func setupTestHandler() (*Handler, *gateway.MemoryStore, *tenant.MemoryStore) {
	gwStore := gateway.NewMemoryStore()
	tenantStore := tenant.NewMemoryStore()

	// Create two tenants
	tenantA := &tenant.Tenant{
		ID:     "ten_a",
		Name:   "Tenant A",
		Slug:   "tenant-a",
		Plan:   tenant.PlanStarter,
		Status: tenant.StatusActive,
		Settings: tenant.Settings{
			RateLimitRPM: 300,
			MaxAgents:    10,
			TakeRateBPS:  50,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	tenantB := &tenant.Tenant{
		ID:     "ten_b",
		Name:   "Tenant B",
		Slug:   "tenant-b",
		Plan:   tenant.PlanFree,
		Status: tenant.StatusActive,
		Settings: tenant.Settings{
			RateLimitRPM: 60,
			MaxAgents:    3,
			TakeRateBPS:  0,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = tenantStore.Create(context.Background(), tenantA)
	_ = tenantStore.Create(context.Background(), tenantB)

	handler := NewHandler(gwStore, tenantStore)
	return handler, gwStore, tenantStore
}

// makeRequest creates a test context and calls the handler.
func makeRequest(t *testing.T, handler gin.HandlerFunc, tenantParam, callerTenantID string, isAdmin bool) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: tenantParam}}
	c.Request = httptest.NewRequest("GET", "/test", nil)

	if isAdmin {
		c.Request.Header.Set("X-Admin-Secret", "test-secret")
	} else if callerTenantID != "" {
		c.Set(auth.ContextKeyTenantID, callerTenantID)
	}

	handler(c)
	return w
}

// --- Overview endpoint ---

func TestOverview_Success(t *testing.T) {
	handler, gwStore, _ := setupTestHandler()

	// Create sessions and request logs for tenant A
	ctx := context.Background()
	_ = gwStore.CreateSession(ctx, &gateway.Session{
		ID: "sess_1", TenantID: "ten_a", AgentAddr: "0xAgent1",
		Status: gateway.StatusActive, CreatedAt: time.Now(),
	})
	_ = gwStore.CreateSession(ctx, &gateway.Session{
		ID: "sess_2", TenantID: "ten_a", AgentAddr: "0xAgent2",
		Status: gateway.StatusClosed, CreatedAt: time.Now(),
	})
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_1", SessionID: "sess_1", TenantID: "ten_a",
		Status: "success", Amount: "10.000000", FeeAmount: "0.050000", CreatedAt: time.Now(),
	})
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_2", SessionID: "sess_2", TenantID: "ten_a",
		Status: "success", Amount: "20.000000", FeeAmount: "0.100000", CreatedAt: time.Now(),
	})

	w := makeRequest(t, handler.Overview, "ten_a", "ten_a", false)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "ten_a", resp["tenant"].(map[string]interface{})["id"])

	billing := resp["billing"].(map[string]interface{})
	assert.Equal(t, float64(2), billing["totalRequests"])
	assert.Equal(t, float64(2), billing["settledRequests"])
	assert.Equal(t, "30.000000", billing["settledVolume"])
	assert.Equal(t, float64(1), resp["activeSessions"]) // 1 active, 1 closed
}

func TestOverview_CrossTenantIsolation(t *testing.T) {
	handler, _, _ := setupTestHandler()

	// Tenant A tries to access tenant B's dashboard
	w := makeRequest(t, handler.Overview, "ten_b", "ten_a", false)
	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "forbidden", resp["error"])
	assert.Equal(t, "not your tenant", resp["message"])
}

func TestOverview_AdminCanAccessAnyTenant(t *testing.T) {
	handler, _, _ := setupTestHandler()

	w := makeRequest(t, handler.Overview, "ten_a", "", true)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOverview_EmptyTenant_ReturnsZeroValues(t *testing.T) {
	handler, _, _ := setupTestHandler()

	w := makeRequest(t, handler.Overview, "ten_a", "ten_a", false)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	billing := resp["billing"].(map[string]interface{})
	assert.Equal(t, float64(0), billing["totalRequests"])
	assert.Equal(t, "0", billing["settledVolume"])
	assert.Equal(t, float64(0), resp["activeSessions"])
}

func TestOverview_TenantNotFound(t *testing.T) {
	handler, _, _ := setupTestHandler()

	w := makeRequest(t, handler.Overview, "ten_nonexistent", "ten_nonexistent", false)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestOverview_UnauthenticatedRequest(t *testing.T) {
	handler, _, _ := setupTestHandler()

	w := makeRequest(t, handler.Overview, "ten_a", "", false) // no caller tenant
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- Usage endpoint ---

func TestUsage_Success(t *testing.T) {
	handler, gwStore, _ := setupTestHandler()

	ctx := context.Background()
	now := time.Now()
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_1", SessionID: "sess_1", TenantID: "ten_a",
		Status: "success", Amount: "5.000000", CreatedAt: now.Add(-24 * time.Hour),
	})
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_2", SessionID: "sess_2", TenantID: "ten_a",
		Status: "success", Amount: "10.000000", CreatedAt: now,
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "ten_a"}}
	c.Request = httptest.NewRequest("GET", "/test?interval=day", nil)
	c.Set(auth.ContextKeyTenantID, "ten_a")
	handler.Usage(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "day", resp["interval"])
}

func TestUsage_InvalidInterval(t *testing.T) {
	handler, _, _ := setupTestHandler()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "ten_a"}}
	c.Request = httptest.NewRequest("GET", "/test?interval=invalid", nil)
	c.Set(auth.ContextKeyTenantID, "ten_a")
	handler.Usage(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUsage_CrossTenantIsolation(t *testing.T) {
	handler, _, _ := setupTestHandler()

	w := makeRequest(t, handler.Usage, "ten_b", "ten_a", false)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- TopServices endpoint ---

func TestTopServices_Success(t *testing.T) {
	handler, gwStore, _ := setupTestHandler()

	ctx := context.Background()
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_1", SessionID: "sess_1", TenantID: "ten_a",
		ServiceType: "llm", Status: "success", Amount: "10.000000", CreatedAt: time.Now(),
	})
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_2", SessionID: "sess_2", TenantID: "ten_a",
		ServiceType: "llm", Status: "success", Amount: "20.000000", CreatedAt: time.Now(),
	})
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_3", SessionID: "sess_3", TenantID: "ten_a",
		ServiceType: "vision", Status: "success", Amount: "5.000000", CreatedAt: time.Now(),
	})

	w := makeRequest(t, handler.TopServices, "ten_a", "ten_a", false)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	services := resp["services"].([]interface{})
	assert.Equal(t, 2, len(services))

	// llm should be first (2 requests > 1)
	first := services[0].(map[string]interface{})
	assert.Equal(t, "llm", first["serviceType"])
	assert.Equal(t, float64(2), first["requests"])
}

func TestTopServices_CrossTenantIsolation(t *testing.T) {
	handler, gwStore, _ := setupTestHandler()

	ctx := context.Background()
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_b", SessionID: "sess_b", TenantID: "ten_b",
		ServiceType: "secret", Status: "success", Amount: "1000.000000", CreatedAt: time.Now(),
	})

	w := makeRequest(t, handler.TopServices, "ten_a", "ten_a", false)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// Should not see tenant B's data
	if resp["services"] != nil {
		services := resp["services"].([]interface{})
		assert.Equal(t, 0, len(services))
	} else {
		assert.Equal(t, float64(0), resp["count"])
	}
}

// --- Denials endpoint ---

func TestDenials_Success(t *testing.T) {
	handler, gwStore, _ := setupTestHandler()

	ctx := context.Background()
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_deny1", SessionID: "sess_1", TenantID: "ten_a",
		Status: "policy_denied", CreatedAt: time.Now(),
	})
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_deny2", SessionID: "sess_2", TenantID: "ten_a",
		Status: "policy_denied", CreatedAt: time.Now().Add(-1 * time.Hour),
	})

	w := makeRequest(t, handler.Denials, "ten_a", "ten_a", false)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	denials := resp["denials"].([]interface{})
	assert.Equal(t, 2, len(denials))
}

func TestDenials_CrossTenantIsolation(t *testing.T) {
	handler, gwStore, _ := setupTestHandler()

	ctx := context.Background()
	_ = gwStore.CreateLog(ctx, &gateway.RequestLog{
		ID: "log_deny_b", SessionID: "sess_b", TenantID: "ten_b",
		Status: "policy_denied", CreatedAt: time.Now(),
	})

	w := makeRequest(t, handler.Denials, "ten_a", "ten_a", false)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// Denials might be nil or empty slice depending on implementation
	if resp["denials"] != nil {
		denials := resp["denials"].([]interface{})
		assert.Equal(t, 0, len(denials))
	} else {
		assert.Equal(t, float64(0), resp["count"])
	}
}

// --- Sessions endpoint ---

func TestSessions_Success(t *testing.T) {
	handler, gwStore, _ := setupTestHandler()

	ctx := context.Background()
	_ = gwStore.CreateSession(ctx, &gateway.Session{
		ID: "sess_1", TenantID: "ten_a", Status: gateway.StatusActive, CreatedAt: time.Now(),
	})
	_ = gwStore.CreateSession(ctx, &gateway.Session{
		ID: "sess_2", TenantID: "ten_a", Status: gateway.StatusClosed, CreatedAt: time.Now(),
	})

	w := makeRequest(t, handler.Sessions, "ten_a", "ten_a", false)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	sessions := resp["sessions"].([]interface{})
	assert.Equal(t, 2, len(sessions))
}

func TestSessions_StatusFilter(t *testing.T) {
	handler, gwStore, _ := setupTestHandler()

	ctx := context.Background()
	_ = gwStore.CreateSession(ctx, &gateway.Session{
		ID: "sess_active", TenantID: "ten_a", Status: gateway.StatusActive, CreatedAt: time.Now(),
	})
	_ = gwStore.CreateSession(ctx, &gateway.Session{
		ID: "sess_closed", TenantID: "ten_a", Status: gateway.StatusClosed, CreatedAt: time.Now(),
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "ten_a"}}
	c.Request = httptest.NewRequest("GET", "/test?status=active", nil)
	c.Set(auth.ContextKeyTenantID, "ten_a")
	handler.Sessions(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	sessions := resp["sessions"].([]interface{})
	assert.Equal(t, 1, len(sessions))
}

func TestSessions_CrossTenantIsolation(t *testing.T) {
	handler, gwStore, _ := setupTestHandler()

	ctx := context.Background()
	_ = gwStore.CreateSession(ctx, &gateway.Session{
		ID: "sess_b", TenantID: "ten_b", Status: gateway.StatusActive, CreatedAt: time.Now(),
	})

	w := makeRequest(t, handler.Sessions, "ten_a", "ten_a", false)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["sessions"] != nil {
		sessions := resp["sessions"].([]interface{})
		assert.Equal(t, 0, len(sessions))
	} else {
		assert.Equal(t, float64(0), resp["count"])
	}
}

// --- checkOwnership helper ---

func TestCheckOwnership_OwnerCanAccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test", nil)
	c.Set(auth.ContextKeyTenantID, "ten_a")

	result := checkOwnership(c, "ten_a")
	assert.True(t, result)
}

func TestCheckOwnership_NonOwnerGets403(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test", nil)
	c.Set(auth.ContextKeyTenantID, "ten_a")

	result := checkOwnership(c, "ten_b")
	assert.False(t, result)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCheckOwnership_AdminCanAccessAny(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test", nil)
	c.Request.Header.Set("X-Admin-Secret", "test-secret")

	result := checkOwnership(c, "ten_b")
	assert.True(t, result)
}

// --- parseLimit helper ---

func TestParseLimit_DefaultAndCustom(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{"no query", "", 10},
		{"custom value", "limit=25", 25},
		{"caps at max", "limit=200", 100},
		{"invalid", "limit=abc", 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/test?"+tt.query, nil)

			result := parseLimit(c, 10, 100)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- parseTimeRange helper ---

func TestParseTimeRange_DefaultsToLast30Days(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test", nil)

	from, to := parseTimeRange(c)
	assert.WithinDuration(t, time.Now(), to, 5*time.Second)
	assert.WithinDuration(t, time.Now().AddDate(0, 0, -30), from, 5*time.Second)
}

func TestParseTimeRange_CustomRange(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	fromStr := "2026-01-01T00:00:00Z"
	toStr := "2026-01-31T23:59:59Z"
	c.Request = httptest.NewRequest("GET", "/test?from="+fromStr+"&to="+toStr, nil)

	from, to := parseTimeRange(c)

	expectedFrom, _ := time.Parse(time.RFC3339, fromStr)
	expectedTo, _ := time.Parse(time.RFC3339, toStr)

	assert.Equal(t, expectedFrom, from)
	assert.Equal(t, expectedTo, to)
}
