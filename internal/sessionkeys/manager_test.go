package sessionkeys

import (
	"context"
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

func TestSessionKeyCreate(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Create a session key
	req := &SessionKeyRequest{
		PublicKey:           "0xSessionKey1234567890123456789012345678ab",
		MaxPerDay:           "10.00",
		MaxPerTransaction:   "1.00",
		ExpiresIn:           "24h",
		AllowedServiceTypes: []string{"translation"},
		Label:               "Test key",
	}

	key, err := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", req)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if key.ID == "" {
		t.Error("Expected key ID")
	}
	if key.Permission.MaxPerDay != "10.00" {
		t.Errorf("Expected MaxPerDay 10.00, got %s", key.Permission.MaxPerDay)
	}
	if key.PublicKey != "0xsessionkey1234567890123456789012345678ab" {
		t.Errorf("Expected PublicKey to be stored lowercase")
	}
	if !key.IsActive() {
		t.Error("Expected key to be active")
	}
}

func TestSessionKeyCreateRequiresPublicKey(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Create without publicKey should fail
	req := &SessionKeyRequest{
		MaxPerDay: "10.00",
		ExpiresIn: "24h",
		AllowAny:  true,
	}

	_, err := mgr.Create(ctx, "0x1234", req)
	if err == nil {
		t.Error("Expected error when publicKey is missing")
	}
}

func TestSessionKeyValidation(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Create a key with limits
	req := &SessionKeyRequest{
		PublicKey:         "0x1234567890123456789012345678901234567890",
		MaxPerTransaction: "1.00",
		MaxPerDay:         "5.00",
		MaxTotal:          "10.00",
		ExpiresIn:         "1h",
		AllowedRecipients: []string{"0xaaaa"},
	}

	key, _ := mgr.Create(ctx, "0x1234", req)

	// Test valid transaction
	err := mgr.Validate(ctx, key.ID, "0xaaaa", "0.50", "")
	if err != nil {
		t.Errorf("Expected valid, got: %v", err)
	}

	// Test exceeds per-transaction limit
	err = mgr.Validate(ctx, key.ID, "0xaaaa", "2.00", "")
	if err != ErrExceedsPerTx {
		t.Errorf("Expected ErrExceedsPerTx, got: %v", err)
	}

	// Test recipient not allowed
	err = mgr.Validate(ctx, key.ID, "0xbbbb", "0.50", "")
	if err != ErrRecipientNotAllowed {
		t.Errorf("Expected ErrRecipientNotAllowed, got: %v", err)
	}
}

func TestSessionKeyRevocation(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	req := &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
	}

	key, _ := mgr.Create(ctx, "0x1234", req)

	// Verify active
	if !key.IsActive() {
		t.Error("Expected active")
	}

	// Revoke
	err := mgr.Revoke(ctx, key.ID)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// Verify revoked
	key, _ = mgr.Get(ctx, key.ID)
	if key.RevokedAt == nil {
		t.Error("Expected RevokedAt to be set")
	}
	if key.IsActive() {
		t.Error("Expected not active after revocation")
	}

	// Validate should fail
	err = mgr.Validate(ctx, key.ID, "0xaaaa", "0.50", "")
	if err != ErrKeyRevoked {
		t.Errorf("Expected ErrKeyRevoked, got: %v", err)
	}
}

func TestSessionKeyExpiration(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Create key that expires immediately
	req := &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1ms",
		AllowAny:  true,
	}

	key, _ := mgr.Create(ctx, "0x1234", req)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Validate should fail
	err := mgr.Validate(ctx, key.ID, "0xaaaa", "0.50", "")
	if err != ErrKeyExpired {
		t.Errorf("Expected ErrKeyExpired, got: %v", err)
	}
}

func TestSessionKeyUsageTracking(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	req := &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		MaxPerDay: "10.00",
		MaxTotal:  "100.00",
		ExpiresIn: "1h",
		AllowAny:  true,
	}

	key, _ := mgr.Create(ctx, "0x1234", req)

	// Record some usage (with nonce)
	mgr.RecordUsage(ctx, key.ID, "2.50", 1)
	mgr.RecordUsage(ctx, key.ID, "1.50", 2)

	// Check usage
	key, _ = mgr.Get(ctx, key.ID)
	if key.Usage.TotalSpent != "4.000000" {
		t.Errorf("Expected TotalSpent 4.000000, got %s", key.Usage.TotalSpent)
	}
	if key.Usage.TransactionCount != 2 {
		t.Errorf("Expected 2 transactions, got %d", key.Usage.TransactionCount)
	}
	if key.Usage.LastNonce != 2 {
		t.Errorf("Expected LastNonce 2, got %d", key.Usage.LastNonce)
	}

	// Try to spend more than daily limit
	err := mgr.Validate(ctx, key.ID, "0xaaaa", "7.00", "")
	if err != ErrExceedsDaily {
		t.Errorf("Expected ErrExceedsDaily, got: %v", err)
	}
}

func TestParseUSDC(t *testing.T) {
	tests := []struct {
		input    string
		expected string // as formatted output
	}{
		{"1.00", "1.000000"},
		{"0.50", "0.500000"},
		{"0.001", "0.001000"},
		{"100", "100.000000"},
		{"0.000001", "0.000001"},
	}

	for _, tc := range tests {
		big, ok := usdc.Parse(tc.input)
		if !ok {
			t.Errorf("Failed to parse %s", tc.input)
			continue
		}
		result := usdc.Format(big)
		if result != tc.expected {
			t.Errorf("usdc.Parse(%s) -> formatUSDC = %s, expected %s", tc.input, result, tc.expected)
		}
	}
}
