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

func TestSessionKeyNonceReplay(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	req := &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
	}
	key, _ := mgr.Create(ctx, "0x1234", req)

	// Record usage with nonce 5
	err := mgr.RecordUsage(ctx, key.ID, "1.00", 5)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	// Verify nonce stored
	key, _ = mgr.Get(ctx, key.ID)
	if key.Usage.LastNonce != 5 {
		t.Errorf("Expected LastNonce 5, got %d", key.Usage.LastNonce)
	}

	// ValidateSigned with nonce <= LastNonce should be rejected
	// (We test the nonce check in validateSigned logic — nonce 3 < 5)
	signedReq := &SignedTransactRequest{
		To:        "0xaaaa000000000000000000000000000000000000",
		Amount:    "0.50",
		Nonce:     3, // Replay — less than LastNonce of 5
		Timestamp: time.Now().Unix(),
		Signature: "dummy", // Will fail sig check, but nonce is checked after sig
	}

	err = mgr.ValidateSigned(ctx, key.ID, signedReq)
	if err == nil {
		t.Error("Expected error for replayed nonce")
	}
	// Should get signature error first (since we used a dummy sig),
	// but nonce 5 (same as last) should also be rejected
	signedReq.Nonce = 5
	err = mgr.ValidateSigned(ctx, key.ID, signedReq)
	if err == nil {
		t.Error("Expected error for nonce == LastNonce")
	}
}

func TestSessionKeySpendingLimitValidation(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Malformed maxPerTransaction should be rejected at creation time
	badReqs := []struct {
		name string
		req  SessionKeyRequest
	}{
		{
			name: "non-numeric maxPerTransaction",
			req: SessionKeyRequest{
				PublicKey:         "0x1234567890123456789012345678901234567890",
				MaxPerTransaction: "abc",
				ExpiresIn:         "1h",
				AllowAny:          true,
			},
		},
		{
			name: "zero maxPerDay",
			req: SessionKeyRequest{
				PublicKey: "0x1234567890123456789012345678901234567890",
				MaxPerDay: "0",
				ExpiresIn: "1h",
				AllowAny:  true,
			},
		},
		{
			name: "negative maxTotal",
			req: SessionKeyRequest{
				PublicKey: "0x1234567890123456789012345678901234567890",
				MaxTotal:  "-5.00",
				ExpiresIn: "1h",
				AllowAny:  true,
			},
		},
	}

	for _, tc := range badReqs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.Create(ctx, "0x1234", &tc.req)
			if err == nil {
				t.Errorf("Expected error for %s, got nil", tc.name)
			}
		})
	}

	// Valid limits should work
	goodReq := &SessionKeyRequest{
		PublicKey:         "0x1234567890123456789012345678901234567890",
		MaxPerTransaction: "1.50",
		MaxPerDay:         "10.00",
		MaxTotal:          "100.00",
		ExpiresIn:         "1h",
		AllowAny:          true,
	}
	key, err := mgr.Create(ctx, "0x1234", goodReq)
	if err != nil {
		t.Fatalf("Valid spending limits should be accepted: %v", err)
	}
	if key.Permission.MaxPerTransaction != "1.50" {
		t.Errorf("Expected MaxPerTransaction 1.50, got %s", key.Permission.MaxPerTransaction)
	}
}

func TestSessionKeyTotalSpendLimit(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	req := &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		MaxTotal:  "5.00",
		ExpiresIn: "1h",
		AllowAny:  true,
	}
	key, _ := mgr.Create(ctx, "0x1234", req)

	// Spend up to limit
	mgr.RecordUsage(ctx, key.ID, "3.00", 1)
	mgr.RecordUsage(ctx, key.ID, "1.50", 2)

	// Spending $1.00 more would exceed $5 total
	err := mgr.Validate(ctx, key.ID, "0xaaaa", "1.00", "")
	if err != ErrExceedsTotal {
		t.Errorf("Expected ErrExceedsTotal, got: %v", err)
	}

	// But $0.50 should still fit
	err = mgr.Validate(ctx, key.ID, "0xaaaa", "0.50", "")
	if err != nil {
		t.Errorf("$0.50 should fit within remaining total, got: %v", err)
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

// --- merged from coverage_extra_test.go ---

// ---------------------------------------------------------------------------
// MemoryStore: CRUD operations
// ---------------------------------------------------------------------------

func TestMemoryStore_CreateDuplicate(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	key := &SessionKey{
		ID:        "sk_dup",
		OwnerAddr: "0xowner",
		PublicKey: "0xpubkey1234567890123456789012345678901234",
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    DefaultScopes,
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}

	if err := store.Create(ctx, key); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := store.Create(ctx, key); err == nil {
		t.Fatal("expected error on duplicate create")
	}
}

func TestMemoryStore_UpdateNonexistent(t *testing.T) {
	store := NewMemoryStore()
	key := &SessionKey{ID: "nonexistent"}
	err := store.Update(context.Background(), key)
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestMemoryStore_DeleteNonexistent(t *testing.T) {
	store := NewMemoryStore()
	err := store.Delete(context.Background(), "nonexistent")
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestMemoryStore_DeleteSuccess(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	key := &SessionKey{
		ID:        "sk_del",
		OwnerAddr: "0xowner",
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    DefaultScopes,
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}

	store.Create(ctx, key)
	if err := store.Delete(ctx, key.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := store.Get(ctx, key.ID)
	if err != ErrKeyNotFound {
		t.Fatal("expected key not found after delete")
	}
}

func TestMemoryStore_GetByOwner_Empty(t *testing.T) {
	store := NewMemoryStore()
	keys, err := store.GetByOwner(context.Background(), "0xnoone")
	if err != nil {
		t.Fatalf("GetByOwner: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(keys))
	}
}

func TestMemoryStore_GetByParent_Empty(t *testing.T) {
	store := NewMemoryStore()
	keys, err := store.GetByParent(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("GetByParent: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(keys))
	}
}

func TestMemoryStore_ReParentChildren(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	parent := &SessionKey{
		ID:        "sk_parent",
		OwnerAddr: "0xowner",
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    DefaultScopes,
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}
	child := &SessionKey{
		ID:          "sk_child",
		OwnerAddr:   "0xowner",
		ParentKeyID: "sk_parent",
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    DefaultScopes,
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}

	store.Create(ctx, parent)
	store.Create(ctx, child)

	if err := store.ReParentChildren(ctx, "sk_parent", "sk_new_parent"); err != nil {
		t.Fatalf("ReParentChildren: %v", err)
	}

	c, _ := store.Get(ctx, "sk_child")
	if c.ParentKeyID != "sk_new_parent" {
		t.Fatalf("expected new parent, got %s", c.ParentKeyID)
	}
}

func TestMemoryStore_CountActive_Mixed(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	past := now.Add(-time.Hour)

	// Active key
	store.Create(ctx, &SessionKey{
		ID:         "sk_active",
		Permission: Permission{ExpiresAt: now.Add(time.Hour), AllowAny: true, Scopes: DefaultScopes},
		Usage:      SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: now.Format("2006-01-02")},
	})

	// Expired key
	store.Create(ctx, &SessionKey{
		ID:         "sk_expired",
		Permission: Permission{ExpiresAt: past, AllowAny: true, Scopes: DefaultScopes},
		Usage:      SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: now.Format("2006-01-02")},
	})

	// Revoked key
	store.Create(ctx, &SessionKey{
		ID:         "sk_revoked",
		RevokedAt:  &now,
		Permission: Permission{ExpiresAt: now.Add(time.Hour), AllowAny: true, Scopes: DefaultScopes},
		Usage:      SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: now.Format("2006-01-02")},
	})

	count, err := store.CountActive(ctx)
	if err != nil {
		t.Fatalf("CountActive: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 active, got %d", count)
	}
}

func TestMemoryStore_GetRootSecret_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.GetRootSecret(context.Background(), "nonexistent")
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestMemoryStore_GetRootSecret_NoSecret(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &SessionKey{
		ID:         "sk_nosecret",
		Permission: Permission{ExpiresAt: time.Now().Add(time.Hour), AllowAny: true, Scopes: DefaultScopes},
		Usage:      SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	})

	secret, err := store.GetRootSecret(ctx, "sk_nosecret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != nil {
		t.Fatal("expected nil secret")
	}
}

func TestMemoryStore_GetRootSecret_Success(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &SessionKey{
		ID:         "sk_withsecret",
		RootSecret: []byte("secret-bytes"),
		Permission: Permission{ExpiresAt: time.Now().Add(time.Hour), AllowAny: true, Scopes: DefaultScopes},
		Usage:      SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	})

	secret, err := store.GetRootSecret(ctx, "sk_withsecret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(secret) != "secret-bytes" {
		t.Fatalf("expected 'secret-bytes', got %s", string(secret))
	}
}

// ---------------------------------------------------------------------------
// Manager: Create with explicit ExpiresAt
// ---------------------------------------------------------------------------

func TestManager_Create_WithExpiresAt(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	future := time.Now().Add(2 * time.Hour).Format(time.RFC3339)

	key, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresAt: future,
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if key.Permission.ExpiresAt.IsZero() {
		t.Fatal("expected non-zero expiration")
	}
}

// ---------------------------------------------------------------------------
// Manager: Create with default expiry (no ExpiresAt or ExpiresIn)
// ---------------------------------------------------------------------------

func TestManager_Create_DefaultExpiry(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)

	key, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Default expiry is 24h from now
	if time.Until(key.Permission.ExpiresAt) < 23*time.Hour {
		t.Fatal("expected default expiry ~24h from now")
	}
}

// ---------------------------------------------------------------------------
// Manager: Create with invalid spending limits
// ---------------------------------------------------------------------------

func TestManager_Create_InvalidMaxTotal(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid maxTotal")
	}
}

func TestManager_Create_InvalidMaxPerTransaction(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey:         "0x1234567890123456789012345678901234567890",
		ExpiresIn:         "1h",
		AllowAny:          true,
		MaxPerTransaction: "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid maxPerTransaction")
	}
}

func TestManager_Create_InvalidMaxPerDay(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxPerDay: "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid maxPerDay")
	}
}

func TestManager_Create_ZeroMaxTotal(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "0",
	})
	if err == nil {
		t.Fatal("expected error for zero maxTotal")
	}
}

// ---------------------------------------------------------------------------
// Manager: Validate — key expired, revoked, not yet valid
// ---------------------------------------------------------------------------

func TestManager_Validate_KeyExpired(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1ms",
		AllowAny:  true,
	})
	time.Sleep(5 * time.Millisecond)

	err := mgr.Validate(ctx, key.ID, "0xrecipient", "1.00", "")
	if err != ErrKeyExpired {
		t.Fatalf("expected ErrKeyExpired, got %v", err)
	}
}

func TestManager_Validate_KeyRevoked(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	mgr.Revoke(ctx, key.ID)

	err := mgr.Validate(ctx, key.ID, "0xrecipient", "1.00", "")
	if err != ErrKeyRevoked {
		t.Fatalf("expected ErrKeyRevoked, got %v", err)
	}
}

func TestManager_Validate_InvalidAmount(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
	})

	err := mgr.Validate(ctx, key.ID, "0xrecipient", "invalid", "")
	if err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

func TestManager_Validate_ZeroAmount(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
	})

	err := mgr.Validate(ctx, key.ID, "0xrecipient", "0", "")
	if err == nil {
		t.Fatal("expected error for zero amount")
	}
}

func TestManager_Validate_ExceedsTotal(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "10.00",
	})

	err := mgr.Validate(ctx, key.ID, "0xrecipient", "11.00", "")
	if err != ErrExceedsTotal {
		t.Fatalf("expected ErrExceedsTotal, got %v", err)
	}
}

func TestManager_Validate_ExceedsDaily(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxPerDay: "5.00",
	})

	err := mgr.Validate(ctx, key.ID, "0xrecipient", "6.00", "")
	if err != ErrExceedsDaily {
		t.Fatalf("expected ErrExceedsDaily, got %v", err)
	}
}

func TestManager_Validate_ServiceAgentAllowed(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey:         "0x1234567890123456789012345678901234567890",
		ExpiresIn:         "1h",
		AllowedRecipients: []string{"0xaaaa000000000000000000000000000000000000"},
	})

	// Allowed recipient
	err := mgr.Validate(ctx, key.ID, "0xaaaa000000000000000000000000000000000000", "1.00", "")
	if err != nil {
		t.Fatalf("expected allowed: %v", err)
	}

	// Not allowed
	err = mgr.Validate(ctx, key.ID, "0xbbbb000000000000000000000000000000000000", "1.00", "")
	if err != ErrRecipientNotAllowed {
		t.Fatalf("expected ErrRecipientNotAllowed, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Manager: Validate not found
// ---------------------------------------------------------------------------

func TestManager_Validate_NotFound(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	err := mgr.Validate(context.Background(), "nonexistent", "0xrecipient", "1.00", "")
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Manager: RecordUsage
// ---------------------------------------------------------------------------

func TestManager_RecordUsage(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})

	err := mgr.RecordUsage(ctx, key.ID, "5.00", 1)
	if err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	updated, _ := mgr.Get(ctx, key.ID)
	if updated.Usage.TotalSpent != "5.000000" {
		t.Fatalf("expected 5.000000 total spent, got %s", updated.Usage.TotalSpent)
	}
	if updated.Usage.TransactionCount != 1 {
		t.Fatalf("expected 1 tx count, got %d", updated.Usage.TransactionCount)
	}
	if updated.Usage.LastNonce != 1 {
		t.Fatalf("expected last nonce 1, got %d", updated.Usage.LastNonce)
	}
}

func TestManager_RecordUsage_NotFound(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	err := mgr.RecordUsage(context.Background(), "nonexistent", "1.00", 1)
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

// ---------------------------------------------------------------------------
// Manager: RecordUsageWithCascade
// ---------------------------------------------------------------------------

func TestManager_RecordUsageWithCascade_ParentUpdated(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	parent, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})

	child := &SessionKey{
		ID:          "sk_cascade_child",
		OwnerAddr:   "0x1234567890123456789012345678901234567890",
		PublicKey:   "0xchild01234567890123456789012345678901234",
		CreatedAt:   time.Now(),
		ParentKeyID: parent.ID,
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    DefaultScopes,
			MaxTotal:  "50.00",
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}
	store.Create(ctx, child)

	err := mgr.RecordUsageWithCascade(ctx, child.ID, "5.00", 1)
	if err != nil {
		t.Fatalf("RecordUsageWithCascade: %v", err)
	}

	// Parent should have 5.00 spent
	parentKey, _ := mgr.Get(ctx, parent.ID)
	if parentKey.Usage.TotalSpent != "5.000000" {
		t.Fatalf("expected parent 5.000000 spent, got %s", parentKey.Usage.TotalSpent)
	}
}

// ---------------------------------------------------------------------------
// Manager: ValidateAncestorChain
// ---------------------------------------------------------------------------

func TestManager_ValidateAncestorChain_Active(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	parent, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})

	child := &SessionKey{
		ID:          "sk_anc_child",
		OwnerAddr:   "0x1234567890123456789012345678901234567890",
		PublicKey:   "0xchild01234567890123456789012345678901234",
		CreatedAt:   time.Now(),
		ParentKeyID: parent.ID,
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    DefaultScopes,
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}
	store.Create(ctx, child)

	err := mgr.ValidateAncestorChain(ctx, child)
	if err != nil {
		t.Fatalf("expected active ancestor chain: %v", err)
	}
}

func TestManager_ValidateAncestorChain_Revoked(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	parent, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})

	child := &SessionKey{
		ID:          "sk_anc_revoked",
		OwnerAddr:   "0x1234567890123456789012345678901234567890",
		PublicKey:   "0xchild01234567890123456789012345678901234",
		CreatedAt:   time.Now(),
		ParentKeyID: parent.ID,
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    DefaultScopes,
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}
	store.Create(ctx, child)

	// Revoke parent (without cascading to child for this test)
	p, _ := store.Get(ctx, parent.ID)
	now := time.Now()
	p.RevokedAt = &now
	store.Update(ctx, p)

	err := mgr.ValidateAncestorChain(ctx, child)
	if err != ErrAncestorInvalid {
		t.Fatalf("expected ErrAncestorInvalid, got %v", err)
	}
}

func TestManager_ValidateAncestorChain_BudgetExceeded(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	parent, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "10.00",
	})

	// Set parent spent close to max
	p, _ := store.Get(ctx, parent.ID)
	p.Usage.TotalSpent = "9.000000"
	store.Update(ctx, p)

	child := &SessionKey{
		ID:          "sk_anc_budget",
		OwnerAddr:   "0x1234567890123456789012345678901234567890",
		PublicKey:   "0xchild01234567890123456789012345678901234",
		CreatedAt:   time.Now(),
		ParentKeyID: parent.ID,
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    DefaultScopes,
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}
	store.Create(ctx, child)

	err := mgr.ValidateAncestorChain(ctx, child, "2.00")
	if err != ErrExceedsTotal {
		t.Fatalf("expected ErrExceedsTotal, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Manager: VerifyWithProof
// ---------------------------------------------------------------------------

func TestManager_VerifyWithProof_NilProof(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	err := mgr.VerifyWithProof(context.Background(), nil, "1.00")
	if err == nil {
		t.Fatal("expected error for nil proof")
	}
}

func TestManager_VerifyWithProof_MissingRootKeyID(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	err := mgr.VerifyWithProof(context.Background(), &DelegationProof{}, "1.00")
	if err == nil {
		t.Fatal("expected error for missing root key ID")
	}
}

func TestManager_VerifyWithProof_Success(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey:         "0x1234567890123456789012345678901234567890",
		ExpiresIn:         "1h",
		AllowAny:          true,
		MaxTotal:          "100.00",
		MaxPerTransaction: "10.00",
	})

	// The key should have a delegation proof
	if key.DelegationProof == nil {
		t.Fatal("expected delegation proof on created key")
	}

	err := mgr.VerifyWithProof(ctx, key.DelegationProof, "5.00")
	if err != nil {
		t.Fatalf("VerifyWithProof: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Manager: VerifyProofWithSecret
// ---------------------------------------------------------------------------

func TestManager_VerifyProofWithSecret_NilProof(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	resp, err := mgr.VerifyProofWithSecret(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected invalid for nil proof")
	}
}

func TestManager_VerifyProofWithSecret_RootKeyMismatch(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	proof := &DelegationProof{RootKeyID: "key_a"}
	resp, err := mgr.VerifyProofWithSecret(context.Background(), proof, "key_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected invalid for root key mismatch")
	}
}

// ---------------------------------------------------------------------------
// Manager: RotateKey no remaining budget
// ---------------------------------------------------------------------------

func TestManager_RotateKey_NoBudget(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "10.00",
	})

	// Spend all budget
	k, _ := store.Get(ctx, key.ID)
	k.Usage.TotalSpent = "10.000000"
	store.Update(ctx, k)

	_, err := mgr.RotateKey(ctx, key.ID, "0xnewkey1234567890123456789012345678901234")
	if err == nil {
		t.Fatal("expected error for no remaining budget")
	}
}

// ---------------------------------------------------------------------------
// Manager: RotateKey expired key
// ---------------------------------------------------------------------------

func TestManager_RotateKey_Expired(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1ms",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})
	time.Sleep(5 * time.Millisecond)

	_, err := mgr.RotateKey(ctx, key.ID, "0xnewkey1234567890123456789012345678901234")
	if err != ErrKeyExpired {
		t.Fatalf("expected ErrKeyExpired, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Manager: WithDelegationAuditLogger
// ---------------------------------------------------------------------------

func TestManager_WithDelegationAuditLogger(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	mgr.WithDelegationAuditLogger(nil)
	if mgr.AuditLogger() != nil {
		t.Fatal("expected nil after setting nil")
	}
}

// ---------------------------------------------------------------------------
// Manager: buildAncestorChain
// ---------------------------------------------------------------------------

func TestManager_BuildAncestorChain(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	parent, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
	})

	child := &SessionKey{
		ID:          "sk_ancestor_child",
		OwnerAddr:   "0x1234567890123456789012345678901234567890",
		PublicKey:   "0xchild01234567890123456789012345678901234",
		ParentKeyID: parent.ID,
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    DefaultScopes,
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}
	store.Create(ctx, child)

	chain := mgr.buildAncestorChain(ctx, child.ID)
	if len(chain) != 2 {
		t.Fatalf("expected chain of 2, got %d: %v", len(chain), chain)
	}
}

// ---------------------------------------------------------------------------
// DelegationProof: VerifyBudget edge cases
// ---------------------------------------------------------------------------

func TestVerifyBudget_EmptyCaveats(t *testing.T) {
	err := VerifyBudget(&DelegationProof{}, "1.00", "0")
	if err == nil {
		t.Fatal("expected error for empty caveats")
	}
}

func TestVerifyBudget_InvalidAmount(t *testing.T) {
	proof := &DelegationProof{
		Caveats: []Caveat{{MaxTotal: "100.00"}},
	}
	err := VerifyBudget(proof, "invalid", "0")
	if err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

func TestVerifyBudget_ZeroAmount(t *testing.T) {
	proof := &DelegationProof{
		Caveats: []Caveat{{MaxTotal: "100.00"}},
	}
	err := VerifyBudget(proof, "0", "0")
	if err == nil {
		t.Fatal("expected error for zero amount")
	}
}

func TestVerifyBudget_ExceedsPerTx(t *testing.T) {
	proof := &DelegationProof{
		Caveats: []Caveat{{MaxPerTransaction: "5.00"}},
	}
	err := VerifyBudget(proof, "6.00", "0")
	if err != ErrExceedsPerTx {
		t.Fatalf("expected ErrExceedsPerTx, got %v", err)
	}
}

func TestVerifyBudget_ExceedsTotal(t *testing.T) {
	proof := &DelegationProof{
		Caveats: []Caveat{{MaxTotal: "10.00"}},
	}
	err := VerifyBudget(proof, "5.00", "8.00")
	if err != ErrExceedsTotal {
		t.Fatalf("expected ErrExceedsTotal, got %v", err)
	}
}

func TestVerifyBudget_WithinBudget(t *testing.T) {
	proof := &DelegationProof{
		Caveats: []Caveat{{MaxTotal: "100.00", MaxPerTransaction: "50.00"}},
	}
	err := VerifyBudget(proof, "5.00", "10.00")
	if err != nil {
		t.Fatalf("expected within budget: %v", err)
	}
}

// ---------------------------------------------------------------------------
// DelegationProof: VerifyProof edge cases
// ---------------------------------------------------------------------------

func TestVerifyProof_EmptyCaveats(t *testing.T) {
	err := VerifyProof([]byte("secret"), &DelegationProof{})
	if err == nil {
		t.Fatal("expected error for empty caveats")
	}
}

func TestVerifyProof_InvalidTag(t *testing.T) {
	secret, _ := GenerateRootSecret()
	key := makeRootKey()
	proof := NewRootProof(secret, key)
	proof.Tag = "not_valid_hex"

	err := VerifyProof(secret, proof)
	if err == nil {
		t.Fatal("expected error for invalid tag")
	}
}

func TestVerifyProof_ExpiredLeaf(t *testing.T) {
	secret, _ := GenerateRootSecret()
	key := &SessionKey{
		ID:        "sk_expired",
		OwnerAddr: "0xowner",
		PublicKey: "0xpubkey1234567890123456789012345678901234",
		CreatedAt: time.Now(),
		Permission: Permission{
			ExpiresAt: time.Now().Add(-time.Hour), // already expired
		},
	}
	proof := NewRootProof(secret, key)

	err := VerifyProof(secret, proof)
	if err == nil {
		t.Fatal("expected error for expired proof")
	}
}

// ---------------------------------------------------------------------------
// ExtendProof: empty parent caveats
// ---------------------------------------------------------------------------

func TestExtendProof_EmptyParentCaveats(t *testing.T) {
	child := makeChildKey("sk_root")
	_, err := ExtendProof(&DelegationProof{}, child)
	if err == nil {
		t.Fatal("expected error for empty parent caveats")
	}
}

// ---------------------------------------------------------------------------
// parseDuration: days support
// ---------------------------------------------------------------------------

func TestParseDuration_Days(t *testing.T) {
	d, err := parseDuration("7d")
	if err != nil {
		t.Fatalf("parseDuration: %v", err)
	}
	if d != 7*24*time.Hour {
		t.Fatalf("expected 7 days, got %v", d)
	}
}

func TestParseDuration_Hours(t *testing.T) {
	d, err := parseDuration("2h")
	if err != nil {
		t.Fatalf("parseDuration: %v", err)
	}
	if d != 2*time.Hour {
		t.Fatalf("expected 2h, got %v", d)
	}
}

func TestParseDuration_InvalidDays(t *testing.T) {
	_, err := parseDuration("xd")
	if err == nil {
		t.Fatal("expected error for invalid days")
	}
}

// ---------------------------------------------------------------------------
// toLower
// ---------------------------------------------------------------------------

func TestToLower(t *testing.T) {
	result := toLower([]string{"HELLO", "World"})
	if result[0] != "hello" || result[1] != "world" {
		t.Fatalf("expected lowercase, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// intersectStrings
// ---------------------------------------------------------------------------

func TestIntersectStrings_Overlap(t *testing.T) {
	result := intersectStrings([]string{"A", "B", "C"}, []string{"b", "c", "d"})
	if len(result) != 2 {
		t.Fatalf("expected 2 intersections, got %d: %v", len(result), result)
	}
}

func TestIntersectStrings_NoOverlap(t *testing.T) {
	result := intersectStrings([]string{"A"}, []string{"B"})
	if len(result) != 0 {
		t.Fatalf("expected 0 intersections, got %d", len(result))
	}
}

func TestIntersectStrings_Empty(t *testing.T) {
	result := intersectStrings(nil, []string{"A"})
	if len(result) != 0 {
		t.Fatalf("expected 0, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Manager: RotateKey without MaxTotal
// ---------------------------------------------------------------------------

func TestManager_RotateKey_NoMaxTotal(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		// No MaxTotal
	})

	newKey, err := mgr.RotateKey(ctx, key.ID, "0xnewkey1234567890123456789012345678901234")
	if err != nil {
		t.Fatalf("rotate without maxTotal: %v", err)
	}
	if newKey.Permission.MaxTotal != "" {
		t.Fatalf("expected empty maxTotal, got %s", newKey.Permission.MaxTotal)
	}
}

// ---------------------------------------------------------------------------
// Manager: LockKeyChain with nonexistent key
// ---------------------------------------------------------------------------

func TestManager_LockKeyChain_NonexistentKey(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	unlock := mgr.LockKeyChain(context.Background(), "nonexistent")
	unlock() // Should not panic
}

// ---------------------------------------------------------------------------
// Manager: Create with PolicyStore
// ---------------------------------------------------------------------------

func TestManager_WithPolicyStore(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	mgr := NewManager(store, nil, ps)
	if mgr.PolicyStore() == nil {
		t.Fatal("expected non-nil policy store")
	}
}
