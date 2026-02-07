package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestStatusBucket(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{100, "1xx"},
		{200, "2xx"},
		{201, "2xx"},
		{301, "3xx"},
		{400, "4xx"},
		{404, "4xx"},
		{500, "5xx"},
		{503, "5xx"},
	}

	for _, tt := range tests {
		if got := statusBucket(tt.code); got != tt.want {
			t.Errorf("statusBucket(%d) = %s, want %s", tt.code, got, tt.want)
		}
	}
}

func TestMetricsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/metrics", Handler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("Expected non-empty metrics response")
	}

	// Gauges always appear; counters/histograms only after first observation.
	// Check gauges are present (always exported with default 0 value)
	for _, name := range []string{
		"alancoin_active_session_keys",
		"alancoin_active_websocket_clients",
	} {
		if !contains(body, name) {
			t.Errorf("Expected metrics output to contain %s", name)
		}
	}

	// Trigger a counter so we can verify it appears
	TransactionsTotal.WithLabelValues("confirmed").Inc()

	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w, req)
	body = w.Body.String()

	if !contains(body, "alancoin_transactions_total") {
		t.Error("Expected alancoin_transactions_total after incrementing")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestMiddleware_RecordsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
}
