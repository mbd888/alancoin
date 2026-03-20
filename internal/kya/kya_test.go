package kya

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// --- Mock providers ---

type mockAgentProvider struct{}

func (m *mockAgentProvider) GetAgentName(_ context.Context, _ string) (string, error) {
	return "TestAgent", nil
}
func (m *mockAgentProvider) GetAgentCreatedAt(_ context.Context, _ string) (time.Time, error) {
	return time.Now().AddDate(0, -3, 0), nil // 3 months ago
}

type mockReputationProvider struct {
	score       float64
	successRate float64
	disputeRate float64
	txCount     int
}

func (m *mockReputationProvider) GetScore(_ context.Context, _ string) (float64, error) {
	return m.score, nil
}
func (m *mockReputationProvider) GetSuccessRate(_ context.Context, _ string) (float64, error) {
	return m.successRate, nil
}
func (m *mockReputationProvider) GetDisputeRate(_ context.Context, _ string) (float64, error) {
	return m.disputeRate, nil
}
func (m *mockReputationProvider) GetTxCount(_ context.Context, _ string) (int, error) {
	return m.txCount, nil
}

func newTestService(rep *mockReputationProvider) *Service {
	if rep == nil {
		rep = &mockReputationProvider{score: 85, successRate: 0.97, disputeRate: 0.01, txCount: 120}
	}
	return NewService(
		NewMemoryStore(),
		&mockAgentProvider{},
		rep,
		[]byte("test-hmac-secret-key-32-bytes!!"),
		slog.Default(),
	)
}

func TestIssue(t *testing.T) {
	svc := newTestService(nil)
	ctx := context.Background()

	cert, err := svc.Issue(ctx, "0xAgent1", OrgBinding{
		TenantID:     "ten_1",
		OrgName:      "Acme Corp",
		Department:   "Engineering",
		AuthorizedBy: "0xOwner",
		AuthMethod:   "api_key",
	}, PermissionScope{
		MaxSpendPerDay: "100.00",
		AllowedAPIs:    []string{"inference", "translation"},
	}, 90)

	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	if cert.ID == "" {
		t.Fatal("cert ID empty")
	}
	if cert.DID != "did:alancoin:0xAgent1" {
		t.Errorf("DID = %q, want did:alancoin:0xAgent1", cert.DID)
	}
	if cert.Status != CertActive {
		t.Errorf("status = %q, want active", cert.Status)
	}
	if cert.Signature == "" {
		t.Error("signature empty")
	}
	if cert.Reputation.TrustTier != TierAA {
		t.Errorf("tier = %q, want AA (score=85, txCount=120, disputeRate=0.01)", cert.Reputation.TrustTier)
	}
}

func TestIssueReplacesPreviousCert(t *testing.T) {
	svc := newTestService(nil)
	ctx := context.Background()
	org := OrgBinding{TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key"}

	cert1, _ := svc.Issue(ctx, "0xAgent1", org, PermissionScope{}, 90)
	cert2, _ := svc.Issue(ctx, "0xAgent1", org, PermissionScope{}, 90)

	// cert1 should be revoked
	got, _ := svc.store.Get(ctx, cert1.ID)
	if got.Status != CertRevoked {
		t.Errorf("first cert status = %q, want revoked", got.Status)
	}

	// cert2 should be active
	got2, _ := svc.store.Get(ctx, cert2.ID)
	if got2.Status != CertActive {
		t.Errorf("second cert status = %q, want active", got2.Status)
	}
}

func TestVerify(t *testing.T) {
	svc := newTestService(nil)
	ctx := context.Background()
	org := OrgBinding{TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key"}

	cert, _ := svc.Issue(ctx, "0xAgent1", org, PermissionScope{}, 90)

	verified, err := svc.Verify(ctx, cert.ID)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if verified.ID != cert.ID {
		t.Error("cert ID mismatch")
	}
}

func TestVerifyRevoked(t *testing.T) {
	svc := newTestService(nil)
	ctx := context.Background()
	org := OrgBinding{TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key"}

	cert, _ := svc.Issue(ctx, "0xAgent1", org, PermissionScope{}, 90)
	_ = svc.Revoke(ctx, cert.ID, "test revocation")

	_, err := svc.Verify(ctx, cert.ID)
	if err != ErrCertRevoked {
		t.Errorf("Verify err = %v, want ErrCertRevoked", err)
	}
}

func TestVerifyExpired(t *testing.T) {
	svc := newTestService(nil)
	ctx := context.Background()
	org := OrgBinding{TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key"}

	// Issue with 0 days validity
	cert, _ := svc.Issue(ctx, "0xAgent1", org, PermissionScope{}, 0)

	// Force expiry
	cert.ExpiresAt = time.Now().Add(-time.Hour)
	_ = svc.store.Update(ctx, cert)

	_, err := svc.Verify(ctx, cert.ID)
	if err != ErrCertExpired {
		t.Errorf("Verify err = %v, want ErrCertExpired", err)
	}
}

func TestComplianceExport(t *testing.T) {
	svc := newTestService(nil)
	ctx := context.Background()
	org := OrgBinding{TenantID: "ten_1", OrgName: "Acme", AuthorizedBy: "0x1", AuthMethod: "api_key"}

	cert, _ := svc.Issue(ctx, "0xAgent1", org, PermissionScope{MaxSpendPerDay: "500.00"}, 365)
	report, err := svc.ComplianceExport(ctx, cert.ID)
	if err != nil {
		t.Fatalf("ComplianceExport failed: %v", err)
	}
	if report.Standard != "EU AI Act Article 12 — Technical Documentation" {
		t.Errorf("standard = %q", report.Standard)
	}
	if !report.SignatureValid {
		t.Error("signature invalid in compliance export")
	}
	if report.AgentDID != "did:alancoin:0xAgent1" {
		t.Errorf("DID = %q", report.AgentDID)
	}
}

func TestTierComputation(t *testing.T) {
	tests := []struct {
		name        string
		score       float64
		txCount     int
		disputeRate float64
		want        TrustTier
	}{
		{"AAA", 95, 200, 0.005, TierAAA},
		{"AA", 82, 60, 0.015, TierAA},
		{"A", 70, 30, 0.03, TierA},
		{"B", 50, 10, 0.05, TierB},
		{"C low score", 30, 10, 0.05, TierC},
		{"D new agent", 80, 3, 0.0, TierD},
		{"D high dispute", 80, 50, 0.15, TierD},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeTier(tt.score, tt.txCount, tt.disputeRate)
			if got != tt.want {
				t.Errorf("computeTier(%v, %v, %v) = %v, want %v", tt.score, tt.txCount, tt.disputeRate, got, tt.want)
			}
		})
	}
}

func TestListByTenant(t *testing.T) {
	svc := newTestService(nil)
	ctx := context.Background()

	org1 := OrgBinding{TenantID: "ten_1", OrgName: "Org1", AuthorizedBy: "0x1", AuthMethod: "api_key"}
	org2 := OrgBinding{TenantID: "ten_2", OrgName: "Org2", AuthorizedBy: "0x2", AuthMethod: "api_key"}

	svc.Issue(ctx, "0xA1", org1, PermissionScope{}, 90)
	svc.Issue(ctx, "0xA2", org1, PermissionScope{}, 90)
	svc.Issue(ctx, "0xA3", org2, PermissionScope{}, 90)

	certs, err := svc.store.ListByTenant(ctx, "ten_1", 100)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(certs) != 2 {
		t.Errorf("got %d certs for ten_1, want 2", len(certs))
	}
}
