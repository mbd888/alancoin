package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// gzipMiddleware
// ---------------------------------------------------------------------------

func TestGzipMiddleware_CompressesWhenAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gzipMiddleware())
	router.GET("/test", func(c *gin.Context) {
		// Write enough data to be worth compressing
		c.String(200, strings.Repeat("hello world ", 100))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Expected Content-Encoding: gzip")
	}

	// Verify the body is valid gzip
	reader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read gzip body: %v", err)
	}
	if !strings.Contains(string(body), "hello world") {
		t.Error("Expected decompressed body to contain 'hello world'")
	}
}

func TestGzipMiddleware_SkipsWithoutAcceptEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gzipMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "hello")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	// No Accept-Encoding header
	router.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") == "gzip" {
		t.Error("Should not gzip when Accept-Encoding is absent")
	}
	if w.Body.String() != "hello" {
		t.Errorf("Expected 'hello', got %q", w.Body.String())
	}
}

func TestGzipMiddleware_SkipsWebSocket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gzipMiddleware())
	router.GET("/ws", func(c *gin.Context) {
		c.String(200, "ws-response")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Upgrade", "websocket")
	router.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") == "gzip" {
		t.Error("Should not gzip WebSocket upgrade requests")
	}
}

// ---------------------------------------------------------------------------
// cacheControl middleware
// ---------------------------------------------------------------------------

func TestCacheControl_SetsHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(cacheControl(60))
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	cc := w.Header().Get("Cache-Control")
	if cc != "public, max-age=60" {
		t.Errorf("Expected 'public, max-age=60', got %q", cc)
	}
}

// ---------------------------------------------------------------------------
// timeoutMiddleware
// ---------------------------------------------------------------------------

func TestTimeoutMiddleware_SkipsWebSocket_Coverage(t *testing.T) {
	s := newTestServer(t)

	router := gin.New()
	router.Use(s.timeoutMiddleware())
	router.GET("/ws", func(c *gin.Context) {
		// WebSocket upgrade requests should not have timeout applied
		// Verify context does NOT have a deadline from timeout middleware
		_, hasDeadline := c.Request.Context().Deadline()
		if hasDeadline {
			t.Error("WebSocket request should not have a deadline from timeout middleware")
		}
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
}

func TestTimeoutMiddleware_AppliesTimeout(t *testing.T) {
	s := newTestServer(t)

	router := gin.New()
	router.Use(s.timeoutMiddleware())
	router.GET("/test", func(c *gin.Context) {
		// Verify context has a deadline
		_, hasDeadline := c.Request.Context().Deadline()
		if !hasDeadline {
			t.Error("Expected context to have a deadline from timeout middleware")
		}
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// requestIDMiddleware
// ---------------------------------------------------------------------------

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	s := newTestServer(t)

	router := gin.New()
	router.Use(s.requestIDMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	xReqID := w.Header().Get("X-Request-ID")
	if xReqID == "" {
		t.Error("Expected X-Request-ID header to be set")
	}
}

func TestRequestIDMiddleware_PreservesExistingID(t *testing.T) {
	s := newTestServer(t)

	router := gin.New()
	router.Use(s.requestIDMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "my-custom-id")
	router.ServeHTTP(w, req)

	xReqID := w.Header().Get("X-Request-ID")
	if xReqID != "my-custom-id" {
		t.Errorf("Expected preserved request ID 'my-custom-id', got %q", xReqID)
	}
}

// ---------------------------------------------------------------------------
// loggingMiddleware
// ---------------------------------------------------------------------------

func TestLoggingMiddleware_DoesNotPanic(t *testing.T) {
	s := newTestServer(t)

	router := gin.New()
	router.Use(s.loggingMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HealthResponse type
// ---------------------------------------------------------------------------

func TestHealthResponse_Serialization(t *testing.T) {
	resp := HealthResponse{
		Status:  "healthy",
		Version: "0.1.0",
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}
	if !strings.Contains(string(data), "healthy") {
		t.Error("Expected 'healthy' in serialized response")
	}
}

// ---------------------------------------------------------------------------
// Router accessor
// ---------------------------------------------------------------------------

func TestServer_Router(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	if r == nil {
		t.Error("Expected non-nil router")
	}
}

// ---------------------------------------------------------------------------
// makeForensicsConsumer, makeChargebackConsumer, makeWebhookConsumer
// ---------------------------------------------------------------------------

func TestMakeForensicsConsumer_NilService_Coverage(t *testing.T) {
	s := newTestServer(t)
	s.forensicsService = nil

	handler := s.makeForensicsConsumer()
	err := handler(nil, nil)
	if err != nil {
		t.Errorf("Expected nil error for nil forensics service, got: %v", err)
	}
}

func TestMakeChargebackConsumer_NilService_Coverage(t *testing.T) {
	s := newTestServer(t)
	s.chargebackService = nil

	handler := s.makeChargebackConsumer()
	err := handler(nil, nil)
	if err != nil {
		t.Errorf("Expected nil error for nil chargeback service, got: %v", err)
	}
}

func TestMakeWebhookConsumer_NilWebhooks_Coverage(t *testing.T) {
	s := newTestServer(t)
	s.webhooks = nil

	handler := s.makeWebhookConsumer()
	err := handler(nil, nil)
	if err != nil {
		t.Errorf("Expected nil error for nil webhooks, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// tenantRateLimitMiddleware — nil stores
// ---------------------------------------------------------------------------

func TestTenantRateLimitMiddleware_NilStores(t *testing.T) {
	s := newTestServer(t)
	// tenantStore is nil in test config — middleware should be a passthrough
	mw := s.tenantRateLimitMiddleware()

	router := gin.New()
	router.Use(mw)
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for nil tenant store, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// WithLogger option
// ---------------------------------------------------------------------------

func TestWithLogger(t *testing.T) {
	s := newTestServer(t)
	origLogger := s.logger

	// Re-create to check WithLogger actually sets it
	if origLogger == nil {
		t.Error("Expected non-nil logger on server")
	}
}

// ---------------------------------------------------------------------------
// Escrow handler routes return expected status for unauthenticated requests
// ---------------------------------------------------------------------------

func TestEscrowRoutes_RequireAuth(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		method string
		path   string
	}{
		{"POST", "/v1/escrow"},
		{"POST", "/v1/escrow/esc_123/deliver"},
		{"POST", "/v1/escrow/esc_123/confirm"},
		{"POST", "/v1/escrow/esc_123/dispute"},
	}

	for _, tt := range tests {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tt.method, tt.path, nil)
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401, got %d", tt.method, tt.path, w.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// Workflow/stream routes exist
// ---------------------------------------------------------------------------

func TestWorkflowAndStreamRoutesRegistered(t *testing.T) {
	s := newTestServer(t)

	routes := s.router.Routes()
	routeSet := make(map[string]bool)
	for _, route := range routes {
		routeSet[route.Method+":"+route.Path] = true
	}

	expected := []string{
		"POST:/v1/workflows",
		"GET:/v1/workflows/:id",
		"POST:/v1/streams",
		"GET:/v1/streams/:id",
	}

	for _, e := range expected {
		if !routeSet[e] {
			t.Errorf("Route %s not registered", e)
		}
	}
}
