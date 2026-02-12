package sessionkeys

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestHasScope(t *testing.T) {
	// Key with explicit scopes
	key := &SessionKey{
		Permission: Permission{
			Scopes: []string{"spend", "read"},
		},
	}
	if !key.HasScope("spend") {
		t.Error("expected key to have 'spend' scope")
	}
	if !key.HasScope("read") {
		t.Error("expected key to have 'read' scope")
	}
	if key.HasScope("streams") {
		t.Error("expected key NOT to have 'streams' scope")
	}
	if key.HasScope("delegate") {
		t.Error("expected key NOT to have 'delegate' scope")
	}
}

func TestHasScope_DefaultScopes(t *testing.T) {
	// Key with no explicit scopes — should default to ["spend", "read"]
	key := &SessionKey{
		Permission: Permission{},
	}
	if !key.HasScope("spend") {
		t.Error("expected default 'spend' scope")
	}
	if !key.HasScope("read") {
		t.Error("expected default 'read' scope")
	}
	if key.HasScope("streams") {
		t.Error("expected default scopes NOT to include 'streams'")
	}
}

func TestCreate_DefaultScopes(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)

	key, err := mgr.Create(context.Background(), "0x1234567890abcdef1234567890abcdef12345678", &SessionKeyRequest{
		PublicKey: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		MaxTotal:  "10.00",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if len(key.Permission.Scopes) != 2 {
		t.Fatalf("expected 2 default scopes, got %d: %v", len(key.Permission.Scopes), key.Permission.Scopes)
	}
	if !key.HasScope("spend") || !key.HasScope("read") {
		t.Errorf("expected default scopes [spend, read], got %v", key.Permission.Scopes)
	}
}

func TestCreate_ExplicitScopes(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)

	key, err := mgr.Create(context.Background(), "0x1234567890abcdef1234567890abcdef12345678", &SessionKeyRequest{
		PublicKey: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		MaxTotal:  "10.00",
		ExpiresIn: "1h",
		AllowAny:  true,
		Scopes:    []string{"spend", "streams", "escrow"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !key.HasScope("spend") || !key.HasScope("streams") || !key.HasScope("escrow") {
		t.Errorf("expected scopes [spend, streams, escrow], got %v", key.Permission.Scopes)
	}
	if key.HasScope("read") {
		t.Error("expected key NOT to have 'read' scope")
	}
}

func TestCreate_InvalidScope(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)

	_, err := mgr.Create(context.Background(), "0x1234567890abcdef1234567890abcdef12345678", &SessionKeyRequest{
		PublicKey: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		MaxTotal:  "10.00",
		ExpiresIn: "1h",
		AllowAny:  true,
		Scopes:    []string{"spend", "teleport"},
	})
	if err == nil {
		t.Fatal("expected error for invalid scope 'teleport'")
	}
	if err != ErrInvalidScope {
		t.Errorf("expected ErrInvalidScope, got %v", err)
	}
}

func TestValidateScope(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)

	key, err := mgr.Create(context.Background(), "0x1234567890abcdef1234567890abcdef12345678", &SessionKeyRequest{
		PublicKey: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		MaxTotal:  "10.00",
		ExpiresIn: "1h",
		AllowAny:  true,
		Scopes:    []string{"spend", "streams"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Valid scope
	if err := mgr.ValidateScope(context.Background(), key.ID, "spend"); err != nil {
		t.Errorf("expected spend scope to be valid: %v", err)
	}
	if err := mgr.ValidateScope(context.Background(), key.ID, "streams"); err != nil {
		t.Errorf("expected streams scope to be valid: %v", err)
	}

	// Missing scope
	err = mgr.ValidateScope(context.Background(), key.ID, "escrow")
	if err != ErrScopeNotAllowed {
		t.Errorf("expected ErrScopeNotAllowed for 'escrow', got %v", err)
	}

	// Non-existent key
	err = mgr.ValidateScope(context.Background(), "sk_nonexistent", "spend")
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestDelegation_ScopeSubsetEnforced(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)

	// Create parent with [spend, read, delegate]
	parentPriv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	parentAddr := crypto.PubkeyToAddress(parentPriv.PublicKey).Hex()

	parent, err := mgr.Create(context.Background(), "0x1234567890abcdef1234567890abcdef12345678", &SessionKeyRequest{
		PublicKey: parentAddr,
		MaxTotal:  "100.00",
		ExpiresIn: "1h",
		AllowAny:  true,
		Scopes:    []string{"spend", "read", "delegate"},
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Create child key pair
	childPriv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate child key: %v", err)
	}
	childAddr := crypto.PubkeyToAddress(childPriv.PublicKey).Hex()

	// Try delegating with "streams" scope that parent doesn't have — should fail
	ts := time.Now().Unix()
	msg := CreateDelegationMessage(childAddr, "10.00", 1, ts)
	hash := HashMessage(msg)
	sig, err := crypto.Sign(hash, parentPriv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig[64] += 27

	_, err = mgr.CreateDelegated(context.Background(), parent.ID, &DelegateRequest{
		PublicKey: childAddr,
		MaxTotal:  "10.00",
		Scopes:    []string{"spend", "streams"},
		Nonce:     1,
		Timestamp: ts,
		Signature: "0x" + encodeHex(sig),
	})
	if err != ErrChildScopeNotAllowed {
		t.Errorf("expected ErrChildScopeNotAllowed, got %v", err)
	}

	// Delegating with valid subset — should succeed
	ts2 := time.Now().Unix()
	msg2 := CreateDelegationMessage(childAddr, "10.00", 2, ts2)
	hash2 := HashMessage(msg2)
	sig2, err := crypto.Sign(hash2, parentPriv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig2[64] += 27

	child, err := mgr.CreateDelegated(context.Background(), parent.ID, &DelegateRequest{
		PublicKey: childAddr,
		MaxTotal:  "10.00",
		Scopes:    []string{"spend", "read"},
		Nonce:     2,
		Timestamp: ts2,
		Signature: "0x" + encodeHex(sig2),
	})
	if err != nil {
		t.Fatalf("expected delegation with valid subset to succeed: %v", err)
	}
	if !child.HasScope("spend") || !child.HasScope("read") {
		t.Errorf("expected child scopes [spend, read], got %v", child.Permission.Scopes)
	}
	if child.HasScope("delegate") {
		t.Error("child should not have 'delegate' scope (not requested)")
	}
}

func TestDelegation_InheritParentScopes(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)

	// Create parent with specific scopes
	parentPriv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	parentAddr := crypto.PubkeyToAddress(parentPriv.PublicKey).Hex()

	parent, err := mgr.Create(context.Background(), "0x1234567890abcdef1234567890abcdef12345678", &SessionKeyRequest{
		PublicKey: parentAddr,
		MaxTotal:  "100.00",
		ExpiresIn: "1h",
		AllowAny:  true,
		Scopes:    []string{"spend", "streams", "delegate"},
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Delegate without specifying scopes — child should inherit parent's
	childPriv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate child key: %v", err)
	}
	childAddr := crypto.PubkeyToAddress(childPriv.PublicKey).Hex()

	ts := time.Now().Unix()
	msg := CreateDelegationMessage(childAddr, "10.00", 1, ts)
	hash := HashMessage(msg)
	sig, err := crypto.Sign(hash, parentPriv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig[64] += 27

	child, err := mgr.CreateDelegated(context.Background(), parent.ID, &DelegateRequest{
		PublicKey: childAddr,
		MaxTotal:  "10.00",
		Nonce:     1,
		Timestamp: ts,
		Signature: "0x" + encodeHex(sig),
	})
	if err != nil {
		t.Fatalf("expected delegation to succeed: %v", err)
	}

	// Child should have parent's scopes
	if !child.HasScope("spend") || !child.HasScope("streams") || !child.HasScope("delegate") {
		t.Errorf("expected inherited scopes [spend, streams, delegate], got %v", child.Permission.Scopes)
	}
}

func encodeHex(b []byte) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, len(b)*2)
	for i, v := range b {
		result[i*2] = hexChars[v>>4]
		result[i*2+1] = hexChars[v&0x0f]
	}
	return string(result)
}
