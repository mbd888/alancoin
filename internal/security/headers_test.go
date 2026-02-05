package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHeadersMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(HeadersMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Check security headers
	headers := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-XSS-Protection":       "1; mode=block",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}

	for header, expected := range headers {
		if got := w.Header().Get(header); got != expected {
			t.Errorf("%s = %q, want %q", header, got, expected)
		}
	}

	// Check CSP is set
	if csp := w.Header().Get("Content-Security-Policy"); csp == "" {
		t.Error("Content-Security-Policy header not set")
	}
}

func TestCORSMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		allowedOrigins []string
		requestOrigin  string
		expectHeader   bool
	}{
		{
			name:           "allowed origin",
			allowedOrigins: []string{"https://example.com"},
			requestOrigin:  "https://example.com",
			expectHeader:   true,
		},
		{
			name:           "wildcard allows all",
			allowedOrigins: []string{"*"},
			requestOrigin:  "https://anything.com",
			expectHeader:   true,
		},
		{
			name:           "disallowed origin",
			allowedOrigins: []string{"https://example.com"},
			requestOrigin:  "https://evil.com",
			expectHeader:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(CORSMiddleware(tc.allowedOrigins))
			router.GET("/test", func(c *gin.Context) {
				c.String(200, "ok")
			})

			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("Origin", tc.requestOrigin)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			hasHeader := w.Header().Get("Access-Control-Allow-Origin") != ""
			if hasHeader != tc.expectHeader {
				t.Errorf("CORS header present = %v, want %v", hasHeader, tc.expectHeader)
			}
		})
	}
}

func TestCORSPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(CORSMiddleware([]string{"*"}))
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Preflight status = %d, want %d", w.Code, http.StatusNoContent)
	}

	if methods := w.Header().Get("Access-Control-Allow-Methods"); methods == "" {
		t.Error("Access-Control-Allow-Methods not set")
	}
}
