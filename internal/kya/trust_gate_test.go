package kya

import (
	"context"
	"log/slog"
	"testing"
)

// TestTrustGateAllowsValidCert verifies that a valid certificate passes the trust check.
func TestTrustGateAllowsValidCert(t *testing.T) {
	svc := newTestService(&mockReputationProvider{score: 85, successRate: 0.97, disputeRate: 0.01, txCount: 120})
	ctx := context.Background()

	// Issue a cert (tier AA)
	_, err := svc.Issue(ctx, "0xSeller", OrgBinding{
		TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{}, 90)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Check trust
	cert, err := svc.GetByAgent(ctx, "0xSeller")
	if err != nil {
		t.Fatalf("GetByAgent: %v", err)
	}
	if !cert.IsValid() {
		t.Error("expected valid cert")
	}
	if cert.Reputation.TrustTier == TierD {
		t.Error("expected tier better than D")
	}
}

// TestTrustGateBlocksRevokedCert verifies that revoked certs are caught.
func TestTrustGateBlocksRevokedCert(t *testing.T) {
	svc := newTestService(nil)
	ctx := context.Background()

	cert, _ := svc.Issue(ctx, "0xSeller", OrgBinding{
		TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{}, 90)

	svc.Revoke(ctx, cert.ID, "suspicious activity")

	got, err := svc.GetByAgent(ctx, "0xSeller")
	if err == nil && got.IsValid() {
		t.Error("expected revoked cert to be invalid")
	}
}

// TestTrustGateTierDBlocked verifies tier D agents are flagged.
func TestTrustGateTierDBlocked(t *testing.T) {
	// Agent with very few transactions → tier D
	rep := &mockReputationProvider{score: 80, successRate: 0.95, disputeRate: 0.0, txCount: 3}
	svc := NewService(NewMemoryStore(), &mockAgentProvider{}, rep,
		[]byte("test-hmac-secret-key-32-bytes!!"), slog.Default())
	ctx := context.Background()

	cert, _ := svc.Issue(ctx, "0xNewAgent", OrgBinding{
		TenantID: "ten_1", OrgName: "Test", AuthorizedBy: "0x1", AuthMethod: "api_key",
	}, PermissionScope{}, 90)

	if cert.Reputation.TrustTier != TierD {
		t.Errorf("tier = %q, want D (txCount=3)", cert.Reputation.TrustTier)
	}
}
