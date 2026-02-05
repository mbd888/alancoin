package auth

import (
	"context"
	"strings"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store)
	ctx := context.Background()

	rawKey, key, err := mgr.GenerateKey(ctx, "0x1234567890123456789012345678901234567890", "Test key")
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	// Check raw key format
	if !strings.HasPrefix(rawKey, "sk_") {
		t.Errorf("Expected raw key to start with sk_, got %s", rawKey[:10])
	}
	if len(rawKey) != 67 { // "sk_" + 64 hex chars
		t.Errorf("Expected raw key length 67, got %d", len(rawKey))
	}

	// Check key metadata
	if !strings.HasPrefix(key.ID, "ak_") {
		t.Errorf("Expected key ID to start with ak_, got %s", key.ID)
	}
	if key.AgentAddr != "0x1234567890123456789012345678901234567890" {
		t.Errorf("Expected agent addr to match")
	}
	if key.Name != "Test key" {
		t.Errorf("Expected name 'Test key', got %s", key.Name)
	}
}

func TestValidateKey(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store)
	ctx := context.Background()

	// Generate a key
	rawKey, _, err := mgr.GenerateKey(ctx, "0xAgent123", "Primary")
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	// Validate with correct key
	key, err := mgr.ValidateKey(ctx, rawKey)
	if err != nil {
		t.Errorf("ValidateKey failed for valid key: %v", err)
	}
	if key.AgentAddr != "0xagent123" { // lowercased
		t.Errorf("Expected agent addr 0xagent123, got %s", key.AgentAddr)
	}

	// Validate with Bearer prefix
	key, err = mgr.ValidateKey(ctx, "Bearer "+rawKey)
	if err != nil {
		t.Errorf("ValidateKey failed with Bearer prefix: %v", err)
	}

	// Validate with wrong key
	_, err = mgr.ValidateKey(ctx, "sk_wrongkey12345678901234567890123456789012345678901234567890")
	if err != ErrInvalidAPIKey {
		t.Errorf("Expected ErrInvalidAPIKey for wrong key, got: %v", err)
	}

	// Validate with empty key
	_, err = mgr.ValidateKey(ctx, "")
	if err != ErrNoAPIKey {
		t.Errorf("Expected ErrNoAPIKey for empty key, got: %v", err)
	}

	// Validate with malformed key
	_, err = mgr.ValidateKey(ctx, "not_a_valid_key")
	if err != ErrInvalidAPIKey {
		t.Errorf("Expected ErrInvalidAPIKey for malformed key, got: %v", err)
	}
}

func TestListKeys(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store)
	ctx := context.Background()

	// Generate multiple keys for same agent
	mgr.GenerateKey(ctx, "0xAgent1", "Key 1")
	mgr.GenerateKey(ctx, "0xAgent1", "Key 2")
	mgr.GenerateKey(ctx, "0xAgent2", "Key 3")

	// List for agent 1
	keys, err := mgr.ListKeys(ctx, "0xAgent1")
	if err != nil {
		t.Fatalf("ListKeys failed: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("Expected 2 keys for Agent1, got %d", len(keys))
	}

	// List for agent 2
	keys, err = mgr.ListKeys(ctx, "0xAgent2")
	if err != nil {
		t.Fatalf("ListKeys failed: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("Expected 1 key for Agent2, got %d", len(keys))
	}
}

func TestRevokeKey(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store)
	ctx := context.Background()

	rawKey, key, _ := mgr.GenerateKey(ctx, "0xAgent1", "To revoke")

	// Validate before revoke
	_, err := mgr.ValidateKey(ctx, rawKey)
	if err != nil {
		t.Errorf("Key should be valid before revoke")
	}

	// Revoke
	err = mgr.RevokeKey(ctx, key.ID, "0xAgent1")
	if err != nil {
		t.Fatalf("RevokeKey failed: %v", err)
	}

	// Validate after revoke - should fail
	_, err = mgr.ValidateKey(ctx, rawKey)
	if err != ErrInvalidAPIKey {
		t.Errorf("Expected ErrInvalidAPIKey after revoke, got: %v", err)
	}
}

func TestKeyHashNotExposed(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store)
	ctx := context.Background()

	rawKey, _, _ := mgr.GenerateKey(ctx, "0xAgent1", "Test")

	// Get key via ValidateKey
	key, _ := mgr.ValidateKey(ctx, rawKey)

	// Hash should not equal raw key
	if key.Hash == rawKey {
		t.Error("Hash should not equal raw key")
	}

	// Hash should be set
	if key.Hash == "" {
		t.Error("Hash should be set")
	}
}
