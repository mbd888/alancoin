package sessionkeys

import (
	"context"
	"testing"
	"time"
)

func TestRotateKeyBasic(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Create a session key with budget
	key, err := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey:           "0xaaaa0e567890123456789012345678901234abcd",
		MaxTotal:            "100.00",
		MaxPerTransaction:   "10.00",
		ExpiresIn:           "24h",
		AllowedServiceTypes: []string{"translation"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Spend some budget
	if err := mgr.RecordUsage(ctx, key.ID, "30.00", 1); err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	// Rotate
	newKey, err := mgr.RotateKey(ctx, key.ID, "0xdddd057890123456789012345678901234ef0012")
	if err != nil {
		t.Fatalf("RotateKey failed: %v", err)
	}

	// New key should have remaining budget
	if newKey.Permission.MaxTotal != "70.000000" {
		t.Errorf("expected remaining budget 70.000000, got %s", newKey.Permission.MaxTotal)
	}

	// New key should be active
	if !newKey.IsActive() {
		t.Error("new key should be active")
	}

	// New key should reference old key
	if newKey.RotatedFromID != key.ID {
		t.Errorf("expected RotatedFromID=%s, got %s", key.ID, newKey.RotatedFromID)
	}

	// Old key should reference new key
	oldKey, _ := mgr.Get(ctx, key.ID)
	if oldKey.RotatedToID != newKey.ID {
		t.Errorf("expected RotatedToID=%s, got %s", newKey.ID, oldKey.RotatedToID)
	}

	// Old key should have grace period
	if oldKey.RotationGraceEnd == nil {
		t.Error("old key should have grace period set")
	}

	// New key should inherit permissions
	if newKey.Permission.MaxPerTransaction != key.Permission.MaxPerTransaction {
		t.Errorf("expected MaxPerTransaction=%s, got %s", key.Permission.MaxPerTransaction, newKey.Permission.MaxPerTransaction)
	}
	if len(newKey.Permission.AllowedServiceTypes) != 1 || newKey.Permission.AllowedServiceTypes[0] != "translation" {
		t.Errorf("expected AllowedServiceTypes=[translation], got %v", newKey.Permission.AllowedServiceTypes)
	}
}

func TestRotateKeyReParentsChildren(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Create parent key
	parent, err := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xaaaa067890123456789012345678901234abcdef",
		MaxTotal:  "100.00",
		ExpiresIn: "24h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	// Manually create child keys (bypass delegation sig verification)
	child1 := &SessionKey{
		ID:        "child1",
		OwnerAddr: "0x1234567890123456789012345678901234567890",
		PublicKey: "0xbbbb167890123456789012345678901234abcdef",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:  "20.00",
			ExpiresAt: time.Now().Add(24 * time.Hour),
			AllowAny:  true,
		},
		Usage:       SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
		ParentKeyID: parent.ID,
		Depth:       1,
		RootKeyID:   parent.ID,
	}
	child2 := &SessionKey{
		ID:        "child2",
		OwnerAddr: "0x1234567890123456789012345678901234567890",
		PublicKey: "0xbbbb267890123456789012345678901234abcdef",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:  "30.00",
			ExpiresAt: time.Now().Add(24 * time.Hour),
			AllowAny:  true,
		},
		Usage:       SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
		ParentKeyID: parent.ID,
		Depth:       1,
		RootKeyID:   parent.ID,
	}
	store.Create(ctx, child1)
	store.Create(ctx, child2)

	// Rotate parent
	newParent, err := mgr.RotateKey(ctx, parent.ID, "0xcccc067890123456789012345678901234abcdef")
	if err != nil {
		t.Fatalf("RotateKey failed: %v", err)
	}

	// Both children should now point to the new parent
	c1, _ := store.Get(ctx, "child1")
	c2, _ := store.Get(ctx, "child2")

	if c1.ParentKeyID != newParent.ID {
		t.Errorf("child1 parent should be %s, got %s", newParent.ID, c1.ParentKeyID)
	}
	if c2.ParentKeyID != newParent.ID {
		t.Errorf("child2 parent should be %s, got %s", newParent.ID, c2.ParentKeyID)
	}
}

func TestRotateKeyGracePeriod(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, err := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xoldadd567890123456789012345678901234abcd",
		MaxTotal:  "50.00",
		ExpiresIn: "24h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = mgr.RotateKey(ctx, key.ID, "0xnewadd567890123456789012345678901234abcd")
	if err != nil {
		t.Fatalf("RotateKey failed: %v", err)
	}

	// Old key should still be active during grace period
	oldKey, _ := mgr.Get(ctx, key.ID)
	if !oldKey.IsActive() {
		t.Error("old key should be active during grace period")
	}

	// Simulate past grace period
	pastGrace := time.Now().Add(-time.Minute)
	oldKey.RotationGraceEnd = &pastGrace
	store.Update(ctx, oldKey)

	oldKey, _ = mgr.Get(ctx, key.ID)
	if oldKey.IsActive() {
		t.Error("old key should be inactive after grace period")
	}
}

func TestRotateKeyAlreadyRotated(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, err := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xoldadd567890123456789012345678901234abcd",
		MaxTotal:  "50.00",
		ExpiresIn: "24h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// First rotation succeeds
	_, err = mgr.RotateKey(ctx, key.ID, "0xnewke1567890123456789012345678901234abcd")
	if err != nil {
		t.Fatalf("first RotateKey failed: %v", err)
	}

	// Second rotation should fail
	_, err = mgr.RotateKey(ctx, key.ID, "0xnewke2567890123456789012345678901234abcd")
	if err != ErrKeyAlreadyRotated {
		t.Errorf("expected ErrKeyAlreadyRotated, got %v", err)
	}
}

func TestRotateRevokedKey(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, err := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xoldadd567890123456789012345678901234abcd",
		MaxTotal:  "50.00",
		ExpiresIn: "24h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Revoke
	mgr.Revoke(ctx, key.ID)

	// Rotation should fail
	_, err = mgr.RotateKey(ctx, key.ID, "0xnewadd567890123456789012345678901234abcd")
	if err != ErrKeyRevoked {
		t.Errorf("expected ErrKeyRevoked, got %v", err)
	}
}

func TestRotateKeyHTTPEndpoint(t *testing.T) {
	// This tests the handler structure — actual HTTP testing requires a gin test context.
	// Verify the handler exists and has the right method signature.
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	handler := NewHandler(mgr, nil)

	// Verify WithReceiptIssuer returns the handler (builder pattern)
	h := handler.WithReceiptIssuer(nil)
	if h != handler {
		t.Error("WithReceiptIssuer should return the same handler")
	}
}
