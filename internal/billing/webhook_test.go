package billing

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
)

func setupWebhookTest(t *testing.T) (*WebhookHandler, *tenant.MemoryStore) {
	t.Helper()
	ts := tenant.NewMemoryStore()
	wh := NewWebhookHandler("whsec_test", ts, slog.Default())

	err := ts.Create(context.Background(), &tenant.Tenant{
		ID:                   "ten_1",
		Name:                 "Acme Corp",
		Slug:                 "acme",
		Plan:                 tenant.PlanStarter,
		Status:               tenant.StatusActive,
		StripeCustomerID:     "cus_stripe_1",
		StripeSubscriptionID: "sub_stripe_1",
		Settings:             tenant.DefaultSettingsForPlan(tenant.PlanStarter),
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	})
	require.NoError(t, err)

	return wh, ts
}

func makeWebhookContext(t *testing.T, body string) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/webhooks/stripe", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return w, c
}

// --- HandleWebhook entry point ---

func TestHandleWebhook_InvalidSignature(t *testing.T) {
	wh, _ := setupWebhookTest(t)

	w, c := makeWebhookContext(t, `{"type": "test"}`)
	c.Request.Header.Set("Stripe-Signature", "t=123,v1=invalid")
	wh.HandleWebhook(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleWebhook_EmptyBody(t *testing.T) {
	wh, _ := setupWebhookTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/webhooks/stripe", strings.NewReader(""))
	c.Request.Header.Set("Stripe-Signature", "t=123,v1=invalid")
	wh.HandleWebhook(c)

	// Should fail signature verification, not crash.
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- handleSubscriptionDeleted ---

func TestHandleSubscriptionDeleted_Success(t *testing.T) {
	wh, ts := setupWebhookTest(t)

	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"customer": "cus_stripe_1",
			},
		},
	}

	_, c := makeWebhookContext(t, "")
	wh.handleSubscriptionDeleted(c, event)

	// Verify tenant was cancelled.
	updated, err := ts.Get(context.Background(), "ten_1")
	require.NoError(t, err)
	assert.Equal(t, tenant.StatusCancelled, updated.Status)
	assert.Empty(t, updated.StripeSubscriptionID)
}

func TestHandleSubscriptionDeleted_EmptyCustomer(t *testing.T) {
	wh, ts := setupWebhookTest(t)

	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"customer": "",
			},
		},
	}

	_, c := makeWebhookContext(t, "")
	wh.handleSubscriptionDeleted(c, event)

	// Tenant should be unchanged.
	ten, _ := ts.Get(context.Background(), "ten_1")
	assert.Equal(t, tenant.StatusActive, ten.Status)
}

func TestHandleSubscriptionDeleted_UnknownCustomer(t *testing.T) {
	wh, ts := setupWebhookTest(t)

	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"customer": "cus_unknown",
			},
		},
	}

	_, c := makeWebhookContext(t, "")
	wh.handleSubscriptionDeleted(c, event)

	// Tenant should be unchanged (no match).
	ten, _ := ts.Get(context.Background(), "ten_1")
	assert.Equal(t, tenant.StatusActive, ten.Status)
}

// --- handlePaymentFailed ---

func TestHandlePaymentFailed_Success(t *testing.T) {
	wh, ts := setupWebhookTest(t)

	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"customer": "cus_stripe_1",
			},
		},
	}

	_, c := makeWebhookContext(t, "")
	wh.handlePaymentFailed(c, event)

	updated, err := ts.Get(context.Background(), "ten_1")
	require.NoError(t, err)
	assert.Equal(t, tenant.StatusSuspended, updated.Status)
}

func TestHandlePaymentFailed_EmptyCustomer(t *testing.T) {
	wh, ts := setupWebhookTest(t)

	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"customer": "",
			},
		},
	}

	_, c := makeWebhookContext(t, "")
	wh.handlePaymentFailed(c, event)

	ten, _ := ts.Get(context.Background(), "ten_1")
	assert.Equal(t, tenant.StatusActive, ten.Status)
}

// --- handleInvoicePaid ---

func TestHandleInvoicePaid_ReactivatesSuspendedTenant(t *testing.T) {
	wh, ts := setupWebhookTest(t)

	// Suspend the tenant first.
	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.Status = tenant.StatusSuspended
	_ = ts.Update(context.Background(), ten)

	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"customer": "cus_stripe_1",
			},
		},
	}

	_, c := makeWebhookContext(t, "")
	wh.handleInvoicePaid(c, event)

	updated, err := ts.Get(context.Background(), "ten_1")
	require.NoError(t, err)
	assert.Equal(t, tenant.StatusActive, updated.Status)
}

func TestHandleInvoicePaid_ActiveTenantUnchanged(t *testing.T) {
	wh, ts := setupWebhookTest(t)

	// Tenant is already active — should remain so without errors.
	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"customer": "cus_stripe_1",
			},
		},
	}

	_, c := makeWebhookContext(t, "")
	wh.handleInvoicePaid(c, event)

	updated, _ := ts.Get(context.Background(), "ten_1")
	assert.Equal(t, tenant.StatusActive, updated.Status)
}

func TestHandleInvoicePaid_EmptyCustomer(t *testing.T) {
	wh, ts := setupWebhookTest(t)

	ten, _ := ts.Get(context.Background(), "ten_1")
	ten.Status = tenant.StatusSuspended
	_ = ts.Update(context.Background(), ten)

	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"customer": "",
			},
		},
	}

	_, c := makeWebhookContext(t, "")
	wh.handleInvoicePaid(c, event)

	// Should remain suspended — no customer ID to look up.
	updated, _ := ts.Get(context.Background(), "ten_1")
	assert.Equal(t, tenant.StatusSuspended, updated.Status)
}

// --- handleSubscriptionUpdated ---

func TestHandleSubscriptionUpdated_ValidID(t *testing.T) {
	wh, _ := setupWebhookTest(t)

	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"id": "sub_stripe_1",
			},
		},
	}

	// Should not panic — it just logs.
	_, c := makeWebhookContext(t, "")
	wh.handleSubscriptionUpdated(c, event)
}

func TestHandleSubscriptionUpdated_MissingID(t *testing.T) {
	wh, _ := setupWebhookTest(t)

	event := &stripe.Event{
		Data: &stripe.EventData{
			Object: map[string]interface{}{},
		},
	}

	// Should return early, not panic.
	_, c := makeWebhookContext(t, "")
	wh.handleSubscriptionUpdated(c, event)
}

// --- findTenantByCustomer ---

func TestFindTenantByCustomer_Found(t *testing.T) {
	wh, _ := setupWebhookTest(t)

	_, c := makeWebhookContext(t, "")
	result := wh.findTenantByCustomer(c, "cus_stripe_1")
	require.NotNil(t, result)
	assert.Equal(t, "ten_1", result.ID)
}

func TestFindTenantByCustomer_NotFound(t *testing.T) {
	wh, _ := setupWebhookTest(t)

	_, c := makeWebhookContext(t, "")
	result := wh.findTenantByCustomer(c, "cus_nonexistent")
	assert.Nil(t, result)
}

// --- RegisterRoute ---

func TestWebhookRegisterRoute(t *testing.T) {
	wh, _ := setupWebhookTest(t)

	router := gin.New()
	g := router.Group("/v1")
	wh.RegisterRoute(g)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/webhooks/stripe", strings.NewReader("{}"))
	router.ServeHTTP(w, req)

	// Route exists (should not be 404).
	assert.NotEqual(t, http.StatusNotFound, w.Code)
}
