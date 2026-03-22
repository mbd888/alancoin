package security

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// ValidateEndpointURL edge cases
// ---------------------------------------------------------------------------

func TestValidateEndpointURL_ValidHTTPS(t *testing.T) {
	// Use a well-known domain that resolves everywhere
	err := ValidateEndpointURL("https://google.com/webhook")
	if err != nil {
		t.Errorf("Expected valid URL to pass, got: %v", err)
	}
}

func TestValidateEndpointURL_ValidHTTP(t *testing.T) {
	err := ValidateEndpointURL("http://google.com/webhook")
	if err != nil {
		t.Errorf("Expected valid HTTP URL to pass, got: %v", err)
	}
}

func TestValidateEndpointURL_InvalidScheme(t *testing.T) {
	tests := []string{
		"ftp://example.com/file",
		"javascript:alert(1)",
		"file:///etc/passwd",
		"data:text/html,<h1>hi</h1>",
	}
	for _, u := range tests {
		err := ValidateEndpointURL(u)
		if err == nil {
			t.Errorf("Expected error for scheme in %q, got nil", u)
		}
		if !strings.Contains(err.Error(), "scheme") {
			t.Errorf("Expected scheme error for %q, got: %v", u, err)
		}
	}
}

func TestValidateEndpointURL_EmptyHost(t *testing.T) {
	err := ValidateEndpointURL("http:///path")
	if err == nil {
		t.Error("Expected error for empty host")
	}
}

func TestValidateEndpointURL_Localhost(t *testing.T) {
	err := ValidateEndpointURL("https://localhost/hook")
	if err == nil {
		t.Error("Expected error for localhost")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("Expected 'not allowed' error, got: %v", err)
	}
}

func TestValidateEndpointURL_LocalhostCaseInsensitive(t *testing.T) {
	err := ValidateEndpointURL("https://LOCALHOST/hook")
	if err == nil {
		t.Error("Expected error for LOCALHOST (case insensitive)")
	}
}

func TestValidateEndpointURL_MetadataHostnames(t *testing.T) {
	hosts := []string{
		"metadata.google.internal",
		"metadata.google",
		"metadata.aws.internal",
	}
	for _, host := range hosts {
		err := ValidateEndpointURL("https://" + host + "/path")
		if err == nil {
			t.Errorf("Expected error for metadata hostname %q", host)
		}
	}
}

func TestValidateEndpointURL_MetadataIPs(t *testing.T) {
	ips := []string{
		"169.254.169.254",
		"100.100.100.200",
		"fd00:ec2::254",
	}
	for _, ip := range ips {
		var u string
		if strings.Contains(ip, ":") {
			u = "https://[" + ip + "]/path"
		} else {
			u = "https://" + ip + "/path"
		}
		err := ValidateEndpointURL(u)
		if err == nil {
			t.Errorf("Expected error for metadata IP %q", ip)
		}
	}
}

func TestValidateEndpointURL_LoopbackIP(t *testing.T) {
	err := ValidateEndpointURL("https://127.0.0.1/hook")
	if err == nil {
		t.Error("Expected error for loopback IP 127.0.0.1")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("Expected loopback error, got: %v", err)
	}
}

func TestValidateEndpointURL_PrivateIP(t *testing.T) {
	tests := []string{
		"https://10.0.0.1/hook",
		"https://192.168.1.1/hook",
		"https://172.16.0.1/hook",
	}
	for _, u := range tests {
		err := ValidateEndpointURL(u)
		if err == nil {
			t.Errorf("Expected error for private IP in %q", u)
		}
		if !strings.Contains(err.Error(), "private") {
			t.Errorf("Expected private error for %q, got: %v", u, err)
		}
	}
}

func TestValidateEndpointURL_LinkLocalIP(t *testing.T) {
	err := ValidateEndpointURL("https://169.254.1.1/hook")
	if err == nil {
		t.Error("Expected error for link-local IP")
	}
	if !strings.Contains(err.Error(), "link-local") {
		t.Errorf("Expected link-local error, got: %v", err)
	}
}

func TestValidateEndpointURL_UnspecifiedIP(t *testing.T) {
	err := ValidateEndpointURL("https://0.0.0.0/hook")
	if err == nil {
		t.Error("Expected error for unspecified IP 0.0.0.0")
	}
	if !strings.Contains(err.Error(), "unspecified") {
		t.Errorf("Expected unspecified error, got: %v", err)
	}
}

func TestValidateEndpointURL_InvalidURL(t *testing.T) {
	err := ValidateEndpointURL("://broken")
	if err == nil {
		t.Error("Expected error for malformed URL")
	}
}

func TestValidateEndpointURL_UnresolvableHost(t *testing.T) {
	err := ValidateEndpointURL("https://this-host-does-not-exist-aabbcc1234.example/hook")
	if err == nil {
		t.Error("Expected error for unresolvable host")
	}
	if !strings.Contains(err.Error(), "resolve") {
		t.Errorf("Expected resolve error, got: %v", err)
	}
}

func TestValidateEndpointURL_IPv6Loopback(t *testing.T) {
	err := ValidateEndpointURL("https://[::1]/hook")
	if err == nil {
		t.Error("Expected error for IPv6 loopback")
	}
}

// ---------------------------------------------------------------------------
// HeadersMiddleware in production mode (HSTS)
// ---------------------------------------------------------------------------

func TestHeadersMiddleware_ProductionHSTS(t *testing.T) {
	// Set production env
	old := os.Getenv("ENV")
	os.Setenv("ENV", "production")
	defer os.Setenv("ENV", old)

	router := gin.New()
	router.Use(HeadersMiddleware())
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	hsts := w.Header().Get("Strict-Transport-Security")
	if hsts == "" {
		t.Error("Expected HSTS header in production mode")
	}
	if !strings.Contains(hsts, "max-age=31536000") {
		t.Errorf("Expected max-age=31536000, got %q", hsts)
	}
	if !strings.Contains(hsts, "includeSubDomains") {
		t.Errorf("Expected includeSubDomains, got %q", hsts)
	}
}

func TestHeadersMiddleware_NonProductionNoHSTS(t *testing.T) {
	old := os.Getenv("ENV")
	os.Setenv("ENV", "development")
	defer os.Setenv("ENV", old)

	router := gin.New()
	router.Use(HeadersMiddleware())
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	hsts := w.Header().Get("Strict-Transport-Security")
	if hsts != "" {
		t.Errorf("Expected no HSTS header in development mode, got %q", hsts)
	}
}

func TestHeadersMiddleware_PermissionsPolicy(t *testing.T) {
	router := gin.New()
	router.Use(HeadersMiddleware())
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	pp := w.Header().Get("Permissions-Policy")
	if pp == "" {
		t.Error("Expected Permissions-Policy header")
	}
	if !strings.Contains(pp, "geolocation=()") {
		t.Errorf("Expected geolocation=() in Permissions-Policy, got %q", pp)
	}
}

// ---------------------------------------------------------------------------
// CORSMiddleware additional edge cases
// ---------------------------------------------------------------------------

func TestCORSMiddleware_EmptyAllowedOrigins(t *testing.T) {
	router := gin.New()
	router.Use(CORSMiddleware([]string{}))
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://anything.com")
	router.ServeHTTP(w, req)

	// Empty allowed origins list means allow all
	if w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("Expected CORS header for empty allowed origins list")
	}
}

func TestCORSMiddleware_NoOriginHeader(t *testing.T) {
	router := gin.New()
	router.Use(CORSMiddleware([]string{"https://example.com"}))
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	// No Origin header
	router.ServeHTTP(w, req)

	// Should not set ACAO when origin is empty
	acao := w.Header().Get("Access-Control-Allow-Origin")
	if acao != "" {
		t.Errorf("Expected no ACAO for request without Origin, got %q", acao)
	}
}

func TestCORSMiddleware_WildcardNoCredentials(t *testing.T) {
	router := gin.New()
	router.Use(CORSMiddleware([]string{"*"}))
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://anything.com")
	router.ServeHTTP(w, req)

	// Wildcard should NOT set Allow-Credentials
	if w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("Wildcard origin should not set Allow-Credentials")
	}
}

func TestCORSMiddleware_SpecificOriginWithCredentials(t *testing.T) {
	router := gin.New()
	router.Use(CORSMiddleware([]string{"https://myapp.com"}))
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://myapp.com")
	router.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("Specific origin should set Allow-Credentials to true")
	}
}

func TestCORSMiddleware_VaryOriginAlwaysSet(t *testing.T) {
	router := gin.New()
	router.Use(CORSMiddleware([]string{"https://example.com"}))
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://notallowed.com")
	router.ServeHTTP(w, req)

	if w.Header().Get("Vary") != "Origin" {
		t.Errorf("Expected Vary: Origin, got %q", w.Header().Get("Vary"))
	}
}

func TestCORSMiddleware_PreflightMaxAge(t *testing.T) {
	router := gin.New()
	router.Use(CORSMiddleware([]string{"*"}))
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected 204, got %d", w.Code)
	}

	maxAge := w.Header().Get("Access-Control-Max-Age")
	if maxAge != "86400" {
		t.Errorf("Expected Max-Age 86400, got %q", maxAge)
	}
}

// ---------------------------------------------------------------------------
// checkIP edge cases
// ---------------------------------------------------------------------------

func TestCheckIP_PublicIPPasses(t *testing.T) {
	// Test a known public IP (8.8.8.8 = Google DNS)
	err := ValidateEndpointURL("https://8.8.8.8/hook")
	if err != nil {
		t.Errorf("Expected public IP to pass, got: %v", err)
	}
}

func TestValidateEndpointURL_HostWithPort(t *testing.T) {
	// google.com with explicit port
	err := ValidateEndpointURL("https://google.com:443/hook")
	if err != nil {
		t.Errorf("Expected URL with port to pass, got: %v", err)
	}
}
