package kya

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupRouter() (*gin.Engine, *Service) {
	rep := &mockReputationProvider{score: 85, successRate: 0.97, disputeRate: 0.01, txCount: 120}
	svc := NewService(NewMemoryStore(), &mockAgentProvider{}, rep,
		[]byte("test-hmac-secret-key-32-bytes!!"), slog.Default())

	r := gin.New()
	h := NewHandler(svc)

	// Simulate auth middleware setting tenant (must match auth.ContextKey* constants)
	authed := r.Group("/v1", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xCaller")
		c.Set("authTenantID", "ten_1")
		c.Next()
	})
	h.RegisterRoutes(authed)
	h.RegisterProtectedRoutes(authed)

	return r, svc
}

func TestHandlerIssueCertificate(t *testing.T) {
	r, _ := setupRouter()

	body := map[string]interface{}{
		"agentAddr": "0xAgent1",
		"org": map[string]string{
			"tenantId":     "ten_1",
			"orgName":      "Acme",
			"authorizedBy": "0xOwner",
			"authMethod":   "api_key",
		},
		"permissions": map[string]interface{}{
			"maxSpendPerDay": "100.00",
		},
		"validDays": 90,
	}
	b, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/kya/certificates", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Certificate Certificate `json:"certificate"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Certificate.DID != "did:alancoin:0xAgent1" {
		t.Errorf("DID = %q", resp.Certificate.DID)
	}
	if resp.Certificate.Status != CertActive {
		t.Errorf("status = %q", resp.Certificate.Status)
	}
}

func TestHandlerGetCertificate(t *testing.T) {
	r, svc := setupRouter()

	// Issue a cert first
	cert, _ := svc.Issue(nil, "0xAgent1", OrgBinding{
		TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{}, 90)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/kya/certificates/"+cert.ID, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHandlerGetCertificateNotFound(t *testing.T) {
	r, _ := setupRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/kya/certificates/nonexistent", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandlerVerifyCertificate(t *testing.T) {
	r, svc := setupRouter()

	cert, _ := svc.Issue(nil, "0xAgent1", OrgBinding{
		TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{}, 90)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/kya/certificates/"+cert.ID+"/verify", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Valid bool `json:"valid"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Valid {
		t.Error("expected valid=true")
	}
}

func TestHandlerVerifyRevokedCertificate(t *testing.T) {
	r, svc := setupRouter()

	cert, _ := svc.Issue(nil, "0xAgent1", OrgBinding{
		TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{}, 90)
	svc.Revoke(nil, cert.ID, "test")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/kya/certificates/"+cert.ID+"/verify", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	var resp struct {
		Valid bool `json:"valid"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Valid {
		t.Error("expected valid=false for revoked cert")
	}
}

func TestHandlerRevokeCertificate(t *testing.T) {
	r, svc := setupRouter()

	cert, _ := svc.Issue(nil, "0xAgent1", OrgBinding{
		TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{}, 90)

	body, _ := json.Marshal(map[string]string{"reason": "test revocation"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/kya/certificates/"+cert.ID+"/revoke", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHandlerComplianceExport(t *testing.T) {
	r, svc := setupRouter()

	cert, _ := svc.Issue(nil, "0xAgent1", OrgBinding{
		TenantID: "ten_1", OrgName: "Acme Corp", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{MaxSpendPerDay: "1000.00"}, 365)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/kya/certificates/"+cert.ID+"/compliance", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Report ComplianceReport `json:"report"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Report.Standard != "EU AI Act Article 12 — Technical Documentation" {
		t.Errorf("standard = %q", resp.Report.Standard)
	}
	if !resp.Report.SignatureValid {
		t.Error("signature should be valid")
	}
}

func TestHandlerGetByAgent(t *testing.T) {
	r, svc := setupRouter()

	svc.Issue(nil, "0xAgent1", OrgBinding{
		TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{}, 90)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/kya/agents/0xAgent1", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHandlerListByTenantEnforcesTenantIsolation(t *testing.T) {
	r, svc := setupRouter()

	svc.Issue(nil, "0xA1", OrgBinding{
		TenantID: "ten_1", OrgName: "T1", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{}, 90)
	svc.Issue(nil, "0xA2", OrgBinding{
		TenantID: "ten_2", OrgName: "T2", AuthorizedBy: "0x2", AuthMethod: "api_key",
	}, PermissionScope{}, 90)

	// Own tenant — should succeed
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/kya/tenants/ten_1/certificates", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("own tenant: status = %d, want 200", w.Code)
	}

	// Other tenant — should be forbidden
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/kya/tenants/ten_2/certificates", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("other tenant: status = %d, want 403", w2.Code)
	}
}

// Use nil context for test convenience (memory store doesn't use it).
func init() {
	_ = time.Now // suppress unused import
}
