package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newKYAServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/kya/certificates", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"certificate": map[string]any{
				"id":        "kya_test123",
				"agentAddr": "0xAgent1",
				"did":       "did:alancoin:0xAgent1",
				"status":    "active",
				"reputation": map[string]any{
					"trustTier":      "AA",
					"traceRankScore": 85.0,
				},
			},
		})
	})

	mux.HandleFunc("GET /v1/kya/certificates/kya_test123", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"certificate": map[string]any{
				"id":     "kya_test123",
				"did":    "did:alancoin:0xAgent1",
				"status": "active",
			},
		})
	})

	mux.HandleFunc("GET /v1/kya/certificates/kya_test123/verify", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"valid": true,
			"certificate": map[string]any{
				"id":     "kya_test123",
				"status": "active",
			},
		})
	})

	mux.HandleFunc("POST /v1/kya/certificates/kya_test123/revoke", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "revoked"})
	})

	mux.HandleFunc("GET /v1/kya/agents/0xAgent1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"certificate": map[string]any{
				"id":     "kya_test123",
				"status": "active",
			},
		})
	})

	mux.HandleFunc("GET /v1/kya/certificates/kya_test123/compliance", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"report": map[string]any{
				"certificateId":  "kya_test123",
				"standard":       "EU AI Act Article 12",
				"signatureValid": true,
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestKYAIssueCertificate(t *testing.T) {
	srv := newKYAServer(t)
	defer srv.Close()
	c := NewClient(srv.URL, WithAPIKey("test"))

	cert, err := c.KYAIssueCertificate(context.Background(), KYAIssueRequest{
		AgentAddr: "0xAgent1",
		Org:       KYAOrgBinding{TenantID: "ten_1", OrgName: "Test"},
		ValidDays: 90,
	})
	if err != nil {
		t.Fatalf("KYAIssueCertificate: %v", err)
	}
	if cert.ID != "kya_test123" {
		t.Errorf("id = %q", cert.ID)
	}
	if cert.DID != "did:alancoin:0xAgent1" {
		t.Errorf("did = %q", cert.DID)
	}
}

func TestKYAVerifyCertificate(t *testing.T) {
	srv := newKYAServer(t)
	defer srv.Close()
	c := NewClient(srv.URL, WithAPIKey("test"))

	valid, cert, err := c.KYAVerifyCertificate(context.Background(), "kya_test123")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !valid {
		t.Error("expected valid=true")
	}
	if cert.Status != "active" {
		t.Errorf("status = %q", cert.Status)
	}
}

func TestKYARevokeCertificate(t *testing.T) {
	srv := newKYAServer(t)
	defer srv.Close()
	c := NewClient(srv.URL, WithAPIKey("test"))

	if err := c.KYARevokeCertificate(context.Background(), "kya_test123", "test"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
}

func TestKYAGetByAgent(t *testing.T) {
	srv := newKYAServer(t)
	defer srv.Close()
	c := NewClient(srv.URL, WithAPIKey("test"))

	cert, err := c.KYAGetByAgent(context.Background(), "0xAgent1")
	if err != nil {
		t.Fatalf("GetByAgent: %v", err)
	}
	if cert.ID != "kya_test123" {
		t.Errorf("id = %q", cert.ID)
	}
}

func TestKYAComplianceExport(t *testing.T) {
	srv := newKYAServer(t)
	defer srv.Close()
	c := NewClient(srv.URL, WithAPIKey("test"))

	report, err := c.KYAComplianceExport(context.Background(), "kya_test123")
	if err != nil {
		t.Fatalf("ComplianceExport: %v", err)
	}
	if !report.SignatureValid {
		t.Error("expected signatureValid=true")
	}
}
