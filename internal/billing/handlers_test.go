package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
	os.Setenv("DEMO_MODE", "true")
}

// handlerMockProvider is a configurable mock Provider for handler tests.
type handlerMockProvider struct {
	createCustomerFn     func(ctx context.Context, tenantID, name, email string) (string, error)
	createSubscriptionFn func(ctx context.Context, customerID string, plan tenant.Plan) (string, error)
	updateSubscriptionFn func(ctx context.Context, subscriptionID string, newPlan tenant.Plan) error
	cancelSubscriptionFn func(ctx context.Context, subscriptionID string) error
	getSubscriptionFn    func(ctx context.Context, subscriptionID string) (*Subscription, error)
	reportUsageFn        func(ctx context.Context, customerID string, requests int64, volumeUSDC int64) error
}

func newDefaultHandlerMock() *handlerMockProvider {
	return &handlerMockProvider{
		createCustomerFn: func(_ context.Context, _, _, _ string) (string, error) {
			return "cus_test_123", nil
		},
		createSubscriptionFn: func(_ context.Context, _ string, _ tenant.Plan) (string, error) {
			return "sub_test_123", nil
		},
		updateSubscriptionFn: func(_ context.Context, _ string, _ tenant.Plan) error {
			return nil
		},
		cancelSubscriptionFn: func(_ context.Context, _ string) error {
			return nil
		},
		getSubscriptionFn: func(_ context.Context, _ string) (*Subscription, error) {
			return &Subscription{
				ID:                 "sub_test_123",
				CustomerID:         "cus_test_123",
				Plan:               "starter",
				Status:             "active",
				CurrentPeriodStart: time.Now().AddDate(0, -1, 0),
				CurrentPeriodEnd:   time.Now().AddDate(0, 1, 0),
			}, nil
		},
		reportUsageFn: func(_ context.Context, _ string, _ int64, _ int64) error {
			return nil
		},
	}
}

func (m *handlerMockProvider) CreateCustomer(ctx context.Context, tenantID, name, email string) (string, error) {
	return m.createCustomerFn(ctx, tenantID, name, email)
}
func (m *handlerMockProvider) CreateSubscription(ctx context.Context, customerID string, plan tenant.Plan) (string, error) {
	return m.createSubscriptionFn(ctx, customerID, plan)
}
func (m *handlerMockProvider) UpdateSubscription(ctx context.Context, subscriptionID string, newPlan tenant.Plan) error {
	return m.updateSubscriptionFn(ctx, subscriptionID, newPlan)
}
func (m *handlerMockProvider) CancelSubscription(ctx context.Context, subscriptionID string) error {
	return m.cancelSubscriptionFn(ctx, subscriptionID)
}
func (m *handlerMockProvider) GetSubscription(ctx context.Context, subscriptionID string) (*Subscription, error) {
	return m.getSubscriptionFn(ctx, subscriptionID)
}
func (m *handlerMockProvider) ReportUsage(ctx context.Context, customerID string, requests int64, volumeUSDC int64) error {
	return m.reportUsageFn(ctx, customerID, requests, volumeUSDC)
}

// setupHandlerTest creates a billing Handler with mock provider and memory tenant store,
// and seeds a default tenant.
func setupHandlerTest(t *testing.T) (*Handler, *handlerMockProvider, *tenant.MemoryStore) {
	t.Helper()
	mock := newDefaultHandlerMock()
	ts := tenant.NewMemoryStore()
	h := NewHandler(mock, ts)

	err := ts.Create(context.Background(), &tenant.Tenant{
		ID:       "ten_1",
		Name:     "Acme Corp",
		Slug:     "acme",
		Plan:     tenant.PlanStarter,
		Status:   tenant.StatusActive,
		Settings: tenant.DefaultSettingsForPlan(tenant.PlanStarter),
	})
	require.NoError(t, err)

	return h, mock, ts
}

// makeHandlerContext creates a gin.Context for direct handler testing.
func makeHandlerContext(t *testing.T, method, path string, body []byte, tenantParam, callerTenantID string, isAdmin bool) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: tenantParam}}

	if body != nil {
		c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
	} else {
		c.Request = httptest.NewRequest(method, path, nil)
	}

	if isAdmin {
		t.Setenv("DEMO_MODE", "true")
		t.Setenv("ADMIN_SECRET", "")
		c.Set(auth.ContextKeyAPIKey, &auth.APIKey{ID: "test-admin-key", AgentAddr: "0xadmin"})
	} else if callerTenantID != "" {
		c.Set(auth.ContextKeyTenantID, callerTenantID)
	}

	return w, c
}

func parseJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
	return result
}

// --- Subscribe ---

func TestSubscribe_Success(t *testing.T) {
	h, _, ts := setupHandlerTest(t)

	// Tenant has PlanStarter, no existing subscription, no customer ID yet.
	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/subscribe", nil, "ten_1", "ten_1", false)
	h.Subscribe(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "sub_test_123", resp["subscriptionId"])
	assert.Equal(t, "active", resp["status"])

	// Verify tenant was updated with Stripe IDs.
	updated, err := ts.Get(context.Background(), "ten_1")
	require.NoError(t, err)
	assert.Equal(t, "cus_test_123", updated.StripeCustomerID)
	assert.Equal(t, "sub_test_123", updated.StripeSubscriptionID)
}

func TestSubscribe_ExistingCustomerID(t *testing.T) {
	h, _, ts := setupHandlerTest(t)

	// Set customer ID so CreateCustomer is NOT called.
	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeCustomerID = "cus_existing"
	_ = ts.Update(context.Background(), ten)

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/subscribe", nil, "ten_1", "ten_1", false)
	h.Subscribe(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	updated, _ := ts.Get(context.Background(), "ten_1")
	assert.Equal(t, "cus_existing", updated.StripeCustomerID) // unchanged
}

func TestSubscribe_FreePlanRejected(t *testing.T) {
	h, _, ts := setupHandlerTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.Plan = tenant.PlanFree
	_ = ts.Update(context.Background(), ten)

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/subscribe", nil, "ten_1", "ten_1", false)
	h.Subscribe(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "invalid_plan", resp["error"])
}

func TestSubscribe_AlreadySubscribed(t *testing.T) {
	h, _, ts := setupHandlerTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeSubscriptionID = "sub_existing"
	_ = ts.Update(context.Background(), ten)

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/subscribe", nil, "ten_1", "ten_1", false)
	h.Subscribe(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "already_subscribed", resp["error"])
}

func TestSubscribe_TenantNotFound(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_nonexistent/billing/subscribe", nil, "ten_nonexistent", "ten_nonexistent", false)
	h.Subscribe(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSubscribe_Forbidden(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	// Caller's tenant is ten_other, but trying to act on ten_1.
	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/subscribe", nil, "ten_1", "ten_other", false)
	h.Subscribe(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSubscribe_CreateCustomerError(t *testing.T) {
	h, mock, _ := setupHandlerTest(t)

	mock.createCustomerFn = func(_ context.Context, _, _, _ string) (string, error) {
		return "", errors.New("stripe down")
	}

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/subscribe", nil, "ten_1", "ten_1", false)
	h.Subscribe(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "billing_error", resp["error"])
}

func TestSubscribe_CreateSubscriptionError(t *testing.T) {
	h, mock, _ := setupHandlerTest(t)

	mock.createSubscriptionFn = func(_ context.Context, _ string, _ tenant.Plan) (string, error) {
		return "", errors.New("stripe error")
	}

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/subscribe", nil, "ten_1", "ten_1", false)
	h.Subscribe(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "billing_error", resp["error"])
}

func TestSubscribe_AdminBypass(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	// Admin can subscribe any tenant regardless of caller tenant.
	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/subscribe", nil, "ten_1", "", true)
	h.Subscribe(c)

	assert.Equal(t, http.StatusCreated, w.Code)
}

// --- Upgrade ---

func TestUpgrade_Success(t *testing.T) {
	h, _, ts := setupHandlerTest(t)

	// Give tenant an existing subscription.
	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeSubscriptionID = "sub_existing"
	ten.StripeCustomerID = "cus_existing"
	_ = ts.Update(context.Background(), ten)

	body, _ := json.Marshal(map[string]string{"plan": "growth"})
	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/upgrade", body, "ten_1", "ten_1", false)
	h.Upgrade(c)

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "growth", resp["plan"])
	assert.Equal(t, "sub_existing", resp["subscriptionId"])

	// Verify tenant plan was updated.
	updated, _ := ts.Get(context.Background(), "ten_1")
	assert.Equal(t, tenant.PlanGrowth, updated.Plan)
}

func TestUpgrade_MissingPlan(t *testing.T) {
	h, _, ts := setupHandlerTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeSubscriptionID = "sub_existing"
	_ = ts.Update(context.Background(), ten)

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/upgrade", []byte(`{}`), "ten_1", "ten_1", false)
	h.Upgrade(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpgrade_InvalidPlan(t *testing.T) {
	h, _, ts := setupHandlerTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeSubscriptionID = "sub_existing"
	_ = ts.Update(context.Background(), ten)

	body, _ := json.Marshal(map[string]string{"plan": "platinum"})
	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/upgrade", body, "ten_1", "ten_1", false)
	h.Upgrade(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "invalid_plan", resp["error"])
}

func TestUpgrade_NoSubscription(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	// Tenant has no StripeSubscriptionID.
	body, _ := json.Marshal(map[string]string{"plan": "growth"})
	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/upgrade", body, "ten_1", "ten_1", false)
	h.Upgrade(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "no_subscription", resp["error"])
}

func TestUpgrade_TenantNotFound(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	body, _ := json.Marshal(map[string]string{"plan": "growth"})
	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_nonexistent/billing/upgrade", body, "ten_nonexistent", "ten_nonexistent", false)
	h.Upgrade(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpgrade_Forbidden(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	body, _ := json.Marshal(map[string]string{"plan": "growth"})
	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/upgrade", body, "ten_1", "ten_other", false)
	h.Upgrade(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUpgrade_ProviderError(t *testing.T) {
	h, mock, ts := setupHandlerTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeSubscriptionID = "sub_existing"
	_ = ts.Update(context.Background(), ten)

	mock.updateSubscriptionFn = func(_ context.Context, _ string, _ tenant.Plan) error {
		return errors.New("stripe error")
	}

	body, _ := json.Marshal(map[string]string{"plan": "growth"})
	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/upgrade", body, "ten_1", "ten_1", false)
	h.Upgrade(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "billing_error", resp["error"])
}

// --- Cancel ---

func TestCancel_Success(t *testing.T) {
	h, _, ts := setupHandlerTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeSubscriptionID = "sub_existing"
	_ = ts.Update(context.Background(), ten)

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/cancel", nil, "ten_1", "ten_1", false)
	h.Cancel(c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify tenant was downgraded.
	updated, _ := ts.Get(context.Background(), "ten_1")
	assert.Equal(t, tenant.PlanFree, updated.Plan)
	assert.Equal(t, tenant.StatusCancelled, updated.Status)
	assert.Empty(t, updated.StripeSubscriptionID)
}

func TestCancel_NoSubscription(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/cancel", nil, "ten_1", "ten_1", false)
	h.Cancel(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "no_subscription", resp["error"])
}

func TestCancel_TenantNotFound(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_nonexistent/billing/cancel", nil, "ten_nonexistent", "ten_nonexistent", false)
	h.Cancel(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCancel_Forbidden(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/cancel", nil, "ten_1", "ten_other", false)
	h.Cancel(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCancel_ProviderError(t *testing.T) {
	h, mock, ts := setupHandlerTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeSubscriptionID = "sub_existing"
	_ = ts.Update(context.Background(), ten)

	mock.cancelSubscriptionFn = func(_ context.Context, _ string) error {
		return errors.New("stripe error")
	}

	w, c := makeHandlerContext(t, "POST", "/v1/tenants/ten_1/billing/cancel", nil, "ten_1", "ten_1", false)
	h.Cancel(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "billing_error", resp["error"])
}

// --- GetSubscription ---

func TestGetSubscription_WithSubscription(t *testing.T) {
	h, _, ts := setupHandlerTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeSubscriptionID = "sub_existing"
	_ = ts.Update(context.Background(), ten)

	w, c := makeHandlerContext(t, "GET", "/v1/tenants/ten_1/billing/subscription", nil, "ten_1", "ten_1", false)
	h.GetSubscription(c)

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseJSON(t, w)
	assert.NotNil(t, resp["subscription"])
	assert.Equal(t, "starter", resp["plan"])
}

func TestGetSubscription_NoSubscription(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	// Tenant has no StripeSubscriptionID.
	w, c := makeHandlerContext(t, "GET", "/v1/tenants/ten_1/billing/subscription", nil, "ten_1", "ten_1", false)
	h.GetSubscription(c)

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseJSON(t, w)
	assert.Nil(t, resp["subscription"])
	assert.Equal(t, "no active subscription", resp["message"])
}

func TestGetSubscription_TenantNotFound(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	w, c := makeHandlerContext(t, "GET", "/v1/tenants/ten_nonexistent/billing/subscription", nil, "ten_nonexistent", "ten_nonexistent", false)
	h.GetSubscription(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetSubscription_Forbidden(t *testing.T) {
	h, _, _ := setupHandlerTest(t)

	w, c := makeHandlerContext(t, "GET", "/v1/tenants/ten_1/billing/subscription", nil, "ten_1", "ten_other", false)
	h.GetSubscription(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetSubscription_ProviderError(t *testing.T) {
	h, mock, ts := setupHandlerTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.StripeSubscriptionID = "sub_existing"
	_ = ts.Update(context.Background(), ten)

	mock.getSubscriptionFn = func(_ context.Context, _ string) (*Subscription, error) {
		return nil, errors.New("stripe error")
	}

	w, c := makeHandlerContext(t, "GET", "/v1/tenants/ten_1/billing/subscription", nil, "ten_1", "ten_1", false)
	h.GetSubscription(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	resp := parseJSON(t, w)
	assert.Equal(t, "billing_error", resp["error"])
}

// --- RegisterRoutes ---

func TestRegisterRoutes(t *testing.T) {
	mock := newDefaultHandlerMock()
	ts := tenant.NewMemoryStore()
	h := NewHandler(mock, ts)

	router := gin.New()
	g := router.Group("/v1")
	h.RegisterRoutes(g)

	// Verify routes are registered by making a request.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/tenants/ten_1/billing/subscription", nil)
	router.ServeHTTP(w, req)

	// Should not be 404 (route exists even if handler returns an error due to missing auth).
	assert.NotEqual(t, http.StatusNotFound, w.Code)
}
