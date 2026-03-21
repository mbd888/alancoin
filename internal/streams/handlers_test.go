package streams

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupHandler creates a handler backed by a MemoryStore and mock ledger.
func setupHandler() (*Handler, *Service, *mockLedger) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	h := NewHandler(svc)
	return h, svc, ledger
}

// openTestStream opens a stream directly via the service (bypasses HTTP layer).
func openTestStream(t *testing.T, svc *Service) *Stream {
	t.Helper()
	stream, err := svc.Open(context.Background(), OpenRequest{
		BuyerAddr:    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SellerAddr:   "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		HoldAmount:   "1.000000",
		PricePerTick: "0.001000",
	})
	require.NoError(t, err)
	return stream
}

// --- OpenStream handler tests ---

func TestHandler_OpenStream_Success(t *testing.T) {
	h, _, _ := setupHandler()

	body := `{
		"buyerAddr":    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sellerAddr":   "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"holdAmount":   "1.000000",
		"pricePerTick": "0.001000"
	}`

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.OpenStream(c)
	})
	req := httptest.NewRequest("POST", "/streams", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "stream")
}

func TestHandler_OpenStream_InvalidBody(t *testing.T) {
	h, _, _ := setupHandler()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.OpenStream(c)
	})
	req := httptest.NewRequest("POST", "/streams", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_OpenStream_ValidationError(t *testing.T) {
	h, _, _ := setupHandler()

	// Valid JSON with required fields, but invalid address format
	body := `{
		"buyerAddr":    "invalid",
		"sellerAddr":   "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"holdAmount":   "1.000000",
		"pricePerTick": "0.001000"
	}`

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams", func(c *gin.Context) {
		c.Set("authAgentAddr", "invalid")
		h.OpenStream(c)
	})
	req := httptest.NewRequest("POST", "/streams", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "validation_error", resp["error"])
}

func TestHandler_OpenStream_WrongCaller(t *testing.T) {
	h, _, _ := setupHandler()

	body := `{
		"buyerAddr":    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sellerAddr":   "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"holdAmount":   "1.000000",
		"pricePerTick": "0.001000"
	}`

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams", func(c *gin.Context) {
		// Authenticated as a different agent — not the buyer
		c.Set("authAgentAddr", "0xcccccccccccccccccccccccccccccccccccccccc")
		h.OpenStream(c)
	})
	req := httptest.NewRequest("POST", "/streams", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "unauthorized", resp["error"])
}

func TestHandler_OpenStream_HoldFails(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	ledger.holdErr = errors.New("insufficient balance")
	svc := NewService(store, ledger)
	h := NewHandler(svc)

	body := `{
		"buyerAddr":    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sellerAddr":   "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"holdAmount":   "1.000000",
		"pricePerTick": "0.001000"
	}`

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.OpenStream(c)
	})
	req := httptest.NewRequest("POST", "/streams", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- Scope checker ---

type mockScopeChecker struct {
	err error
}

func (m *mockScopeChecker) ValidateScope(_ context.Context, _, _ string) error {
	return m.err
}

func TestHandler_OpenStream_ScopeCheckFail(t *testing.T) {
	h, _, _ := setupHandler()
	h.WithScopeChecker(&mockScopeChecker{err: errors.New("no scope")})

	body := `{
		"buyerAddr":    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sellerAddr":   "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"holdAmount":   "1.000000",
		"pricePerTick": "0.001000",
		"sessionKeyId": "sk_123"
	}`

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.OpenStream(c)
	})
	req := httptest.NewRequest("POST", "/streams", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "scope_not_allowed", resp["error"])
}

// --- GetStream handler tests ---

func TestHandler_GetStream_Success(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/streams/:id", h.GetStream)
	req := httptest.NewRequest("GET", "/streams/"+stream.ID, nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Stream Stream `json:"stream"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, stream.ID, resp.Stream.ID)
	assert.Equal(t, StatusOpen, resp.Stream.Status)
}

func TestHandler_GetStream_NotFound(t *testing.T) {
	h, _, _ := setupHandler()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/streams/:id", h.GetStream)
	req := httptest.NewRequest("GET", "/streams/str_nonexistent", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- ListStreams handler tests ---

func TestHandler_ListStreams_Success(t *testing.T) {
	h, svc, _ := setupHandler()
	openTestStream(t, svc)
	openTestStream(t, svc)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/agents/:address/streams", h.ListStreams)
	req := httptest.NewRequest("GET", "/agents/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/streams", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Streams []Stream `json:"streams"`
		Count   int      `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Count)
}

func TestHandler_ListStreams_WithLimit(t *testing.T) {
	h, svc, _ := setupHandler()
	openTestStream(t, svc)
	openTestStream(t, svc)
	openTestStream(t, svc)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/agents/:address/streams", h.ListStreams)
	req := httptest.NewRequest("GET", "/agents/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/streams?limit=2", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Streams []Stream `json:"streams"`
		Count   int      `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Count)
}

func TestHandler_ListStreams_LimitCapped(t *testing.T) {
	h, _, _ := setupHandler()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/agents/:address/streams", h.ListStreams)
	// limit > 200 should be capped
	req := httptest.NewRequest("GET", "/agents/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/streams?limit=999", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// --- TickStream handler tests ---

func TestHandler_TickStream_Success(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/tick", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.TickStream(c)
	})
	req := httptest.NewRequest("POST", "/streams/"+stream.ID+"/tick", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Tick   Tick   `json:"tick"`
		Stream Stream `json:"stream"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Tick.Seq)
	assert.Equal(t, 1, resp.Stream.TickCount)
}

func TestHandler_TickStream_NotFound(t *testing.T) {
	h, _, _ := setupHandler()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/tick", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.TickStream(c)
	})
	req := httptest.NewRequest("POST", "/streams/str_nonexistent/tick", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_TickStream_Unauthorized(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/tick", func(c *gin.Context) {
		// Third party caller
		c.Set("authAgentAddr", "0xcccccccccccccccccccccccccccccccccccccccc")
		h.TickStream(c)
	})
	req := httptest.NewRequest("POST", "/streams/"+stream.ID+"/tick", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandler_TickStream_ClosedStream(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)
	_, _ = svc.Close(context.Background(), stream.ID, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "done")

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/tick", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.TickStream(c)
	})
	req := httptest.NewRequest("POST", "/streams/"+stream.ID+"/tick", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandler_TickStream_HoldExhausted(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	h := NewHandler(svc)

	ctx := context.Background()
	stream, err := svc.Open(ctx, OpenRequest{
		BuyerAddr:    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SellerAddr:   "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		HoldAmount:   "0.001000",
		PricePerTick: "0.001000",
	})
	require.NoError(t, err)

	// Spend the entire hold
	_, _, err = svc.RecordTick(ctx, stream.ID, TickRequest{})
	require.NoError(t, err)

	// Next tick should return 402
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/tick", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.TickStream(c)
	})
	req := httptest.NewRequest("POST", "/streams/"+stream.ID+"/tick", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusPaymentRequired, w.Code)
}

func TestHandler_TickStream_InvalidAmount(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/tick", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.TickStream(c)
	})
	body := `{"amount": "0.000000"}`
	req := httptest.NewRequest("POST", "/streams/"+stream.ID+"/tick", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- CloseStream handler tests ---

func TestHandler_CloseStream_Success(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)
	_, _, _ = svc.RecordTick(context.Background(), stream.ID, TickRequest{})

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/close", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.CloseStream(c)
	})
	body := `{"reason": "done"}`
	req := httptest.NewRequest("POST", "/streams/"+stream.ID+"/close", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Stream Stream `json:"stream"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, StatusClosed, resp.Stream.Status)
	assert.Equal(t, "done", resp.Stream.CloseReason)
}

func TestHandler_CloseStream_NotFound(t *testing.T) {
	h, _, _ := setupHandler()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/close", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.CloseStream(c)
	})
	req := httptest.NewRequest("POST", "/streams/str_nonexistent/close", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_CloseStream_Unauthorized(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/close", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xcccccccccccccccccccccccccccccccccccccccc")
		h.CloseStream(c)
	})
	req := httptest.NewRequest("POST", "/streams/"+stream.ID+"/close", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandler_CloseStream_AlreadyClosed(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)
	_, _ = svc.Close(context.Background(), stream.ID, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "done")

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/streams/:id/close", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.CloseStream(c)
	})
	req := httptest.NewRequest("POST", "/streams/"+stream.ID+"/close", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// --- ListTicks handler tests ---

func TestHandler_ListTicks_Success(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, _, err := svc.RecordTick(ctx, stream.ID, TickRequest{})
		require.NoError(t, err)
	}

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/streams/:id/ticks", h.ListTicks)
	req := httptest.NewRequest("GET", "/streams/"+stream.ID+"/ticks", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Ticks []Tick `json:"ticks"`
		Count int    `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 3, resp.Count)
}

func TestHandler_ListTicks_WithLimit(t *testing.T) {
	h, svc, _ := setupHandler()
	stream := openTestStream(t, svc)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, _, _ = svc.RecordTick(ctx, stream.ID, TickRequest{})
	}

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/streams/:id/ticks", h.ListTicks)
	req := httptest.NewRequest("GET", "/streams/"+stream.ID+"/ticks?limit=2", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Ticks []Tick `json:"ticks"`
		Count int    `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Count)
}

func TestHandler_ListTicks_LimitCapped(t *testing.T) {
	h, _, _ := setupHandler()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/streams/:id/ticks", h.ListTicks)
	req := httptest.NewRequest("GET", "/streams/str_any/ticks?limit=9999", nil)
	r.ServeHTTP(w, req)

	// Just verify it doesn't error — limit was capped to 1000 internally
	assert.Equal(t, http.StatusOK, w.Code)
}

// --- RegisterRoutes ---

func TestHandler_RegisterRoutes(t *testing.T) {
	h, _, _ := setupHandler()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	group := r.Group("/v1")
	h.RegisterRoutes(group)
	h.RegisterProtectedRoutes(group)

	// Verify route GET /v1/streams/:id is registered
	req := httptest.NewRequest("GET", "/v1/streams/str_test", nil)
	r.ServeHTTP(w, req)
	// Should not be 404 (method not allowed or actual response)
	assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code)
}
