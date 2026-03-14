package sessionkeys

import (
	"context"
	"crypto/ecdsa"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

// ---------------------------------------------------------------------------
// Integration tests: delegation proof wired into Manager
// ---------------------------------------------------------------------------

func TestCreate_GeneratesRootProof(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()

	key, err := mgr.Create(ctx, "0x0000000000000000000000000000000000001111", &SessionKeyRequest{
		PublicKey: addr,
		MaxTotal:  "50.00",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Root key should have a delegation proof with one caveat
	if key.DelegationProof == nil {
		t.Fatal("root key should have a delegation proof")
	}
	if len(key.DelegationProof.Caveats) != 1 {
		t.Errorf("expected 1 caveat, got %d", len(key.DelegationProof.Caveats))
	}
	if key.DelegationProof.RootKeyID != key.ID {
		t.Errorf("rootKeyId = %q, want %q", key.DelegationProof.RootKeyID, key.ID)
	}
	if key.DelegationProof.Tag == "" {
		t.Error("proof tag should not be empty")
	}

	// Root secret should be stored but not serialized
	if len(key.RootSecret) == 0 {
		t.Error("root secret should be stored on the key")
	}
}

func TestCreate_RootSecretInStore(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()

	key, err := mgr.Create(ctx, "0x0000000000000000000000000000000000001111", &SessionKeyRequest{
		PublicKey: addr,
		MaxTotal:  "50.00",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Root secret should be retrievable from the store
	secret, err := store.GetRootSecret(ctx, key.ID)
	if err != nil {
		t.Fatalf("GetRootSecret: %v", err)
	}
	if len(secret) != RootSecretSize {
		t.Errorf("root secret length = %d, want %d", len(secret), RootSecretSize)
	}
}

func TestCreateDelegated_ExtendsProof(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	parent, parentPriv := createRootKeyWithProof(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	// Verify parent has proof
	if parent.DelegationProof == nil {
		t.Fatal("parent should have delegation proof")
	}

	childAddr, _ := freshAddrProof(t)
	req := signDelegationProof(t, parentPriv, childAddr, "25.00", 1)
	child, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if err != nil {
		t.Fatalf("CreateDelegated: %v", err)
	}

	// Child should have extended proof with 2 caveats
	if child.DelegationProof == nil {
		t.Fatal("child key should have a delegation proof")
	}
	if len(child.DelegationProof.Caveats) != 2 {
		t.Errorf("expected 2 caveats, got %d", len(child.DelegationProof.Caveats))
	}
	if child.DelegationProof.RootKeyID != parent.ID {
		t.Errorf("rootKeyId = %q, want %q", child.DelegationProof.RootKeyID, parent.ID)
	}

	// Verify the proof chain using the root secret
	rootSecret, _ := store.GetRootSecret(ctx, parent.ID)
	if err := VerifyProof(rootSecret, child.DelegationProof); err != nil {
		t.Fatalf("child proof verification failed: %v", err)
	}
}

func TestCreateDelegated_MultiLevelProof(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	root, rootPriv := createRootKeyWithProof(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	// Level 1
	midAddr, midPriv := freshAddrProof(t)
	midReq := signDelegationProof(t, rootPriv, midAddr, "50.00", 1)
	mid, err := mgr.CreateDelegated(ctx, root.ID, midReq)
	if err != nil {
		t.Fatalf("mid: %v", err)
	}

	// Level 2
	leafAddr, _ := freshAddrProof(t)
	leafReq := signDelegationProof(t, midPriv, leafAddr, "20.00", 1)
	leaf, err := mgr.CreateDelegated(ctx, mid.ID, leafReq)
	if err != nil {
		t.Fatalf("leaf: %v", err)
	}

	// Verify full chain
	if leaf.DelegationProof == nil {
		t.Fatal("leaf should have delegation proof")
	}
	if len(leaf.DelegationProof.Caveats) != 3 {
		t.Errorf("expected 3 caveats, got %d", len(leaf.DelegationProof.Caveats))
	}

	rootSecret, _ := store.GetRootSecret(ctx, root.ID)
	if err := VerifyProof(rootSecret, leaf.DelegationProof); err != nil {
		t.Fatalf("leaf proof verification failed: %v", err)
	}
}

func TestVerifyWithProof_Valid(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	root, rootPriv := createRootKeyWithProof(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	childAddr, _ := freshAddrProof(t)
	req := signDelegationProof(t, rootPriv, childAddr, "25.00", 1)
	child, err := mgr.CreateDelegated(ctx, root.ID, req)
	if err != nil {
		t.Fatal(err)
	}

	// O(1) verification with proof — no DB walk
	err = mgr.VerifyWithProof(ctx, child.DelegationProof, "5.00")
	if err != nil {
		t.Fatalf("VerifyWithProof: %v", err)
	}
}

func TestVerifyWithProof_ExceedsBudget(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	root, rootPriv := createRootKeyWithProof(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	childAddr, _ := freshAddrProof(t)
	req := signDelegationProof(t, rootPriv, childAddr, "10.00", 1)
	child, err := mgr.CreateDelegated(ctx, root.ID, req)
	if err != nil {
		t.Fatal(err)
	}

	// Amount exceeds child's maxPerTx (which inherits from parent's 10.00 maxTotal)
	err = mgr.VerifyWithProof(ctx, child.DelegationProof, "15.00")
	if err == nil {
		t.Fatal("expected budget error")
	}
}

func TestVerifyWithProof_TamperedProof(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	root, rootPriv := createRootKeyWithProof(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	childAddr, _ := freshAddrProof(t)
	req := signDelegationProof(t, rootPriv, childAddr, "25.00", 1)
	child, err := mgr.CreateDelegated(ctx, root.ID, req)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper with the proof
	tamperedProof := *child.DelegationProof
	tamperedCaveats := make([]Caveat, len(tamperedProof.Caveats))
	copy(tamperedCaveats, tamperedProof.Caveats)
	tamperedCaveats[1].MaxTotal = "999.00" // inflate budget
	tamperedProof.Caveats = tamperedCaveats

	err = mgr.VerifyWithProof(ctx, &tamperedProof, "5.00")
	if err == nil {
		t.Fatal("expected verification failure for tampered proof")
	}
}

func TestVerifyProofWithSecret_Valid(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	root, rootPriv := createRootKeyWithProof(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	childAddr, _ := freshAddrProof(t)
	req := signDelegationProof(t, rootPriv, childAddr, "25.00", 1)
	child, err := mgr.CreateDelegated(ctx, root.ID, req)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := mgr.VerifyProofWithSecret(ctx, child.DelegationProof, root.ID)
	if err != nil {
		t.Fatalf("VerifyProofWithSecret: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected valid proof, got error: %s", resp.Error)
	}
	if resp.LeafKeyID != child.ID {
		t.Errorf("leafKeyId = %q, want %q", resp.LeafKeyID, child.ID)
	}
	if resp.Depth != 1 {
		t.Errorf("depth = %d, want 1", resp.Depth)
	}
	if resp.MaxTotal != "25.00" {
		t.Errorf("maxTotal = %q, want 25.00", resp.MaxTotal)
	}
}

func TestVerifyProofWithSecret_WrongRootKeyID(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	root, rootPriv := createRootKeyWithProof(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	childAddr, _ := freshAddrProof(t)
	req := signDelegationProof(t, rootPriv, childAddr, "25.00", 1)
	child, err := mgr.CreateDelegated(ctx, root.ID, req)
	if err != nil {
		t.Fatal(err)
	}

	// Provide wrong root key ID
	resp, err := mgr.VerifyProofWithSecret(ctx, child.DelegationProof, "sk_nonexistent")
	if err != nil {
		t.Fatalf("should not error, should return invalid: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected invalid proof with wrong root key ID")
	}
}

func TestVerifyProofWithSecret_RootKeyIDMismatch(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	root1, root1Priv := createRootKeyWithProof(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	// Create a second root key
	privKey2, _ := crypto.GenerateKey()
	addr2 := crypto.PubkeyToAddress(privKey2.PublicKey).Hex()
	root2, err := mgr.Create(ctx, "0x0000000000000000000000000000000000001111", &SessionKeyRequest{
		PublicKey: addr2,
		MaxTotal:  "100.00",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create child of root1
	childAddr, _ := freshAddrProof(t)
	req := signDelegationProof(t, root1Priv, childAddr, "25.00", 1)
	child, err := mgr.CreateDelegated(ctx, root1.ID, req)
	if err != nil {
		t.Fatal(err)
	}

	// Try to verify with root2's ID — should fail because proof's rootKeyId is root1
	resp, err := mgr.VerifyProofWithSecret(ctx, child.DelegationProof, root2.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected invalid: root key ID mismatch")
	}
}

func TestGetRootSecret_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.GetRootSecret(context.Background(), "sk_nonexistent")
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestGetRootSecret_KeyWithoutSecret(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create a key without a root secret (simulate legacy key)
	key := &SessionKey{
		ID:        "sk_legacy",
		OwnerAddr: "0xowner",
		PublicKey: "0xlegacypubkey00000000000000000000000000",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:  "10.00",
			ExpiresAt: time.Now().Add(time.Hour),
		},
		Usage: SessionKeyUsage{
			TotalSpent:   "0",
			SpentToday:   "0",
			LastResetDay: time.Now().Format("2006-01-02"),
		},
	}
	if err := store.Create(ctx, key); err != nil {
		t.Fatal(err)
	}

	secret, err := store.GetRootSecret(ctx, "sk_legacy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != nil {
		t.Error("expected nil secret for key without root secret")
	}
}

// ---------------------------------------------------------------------------
// Helpers (use different names to avoid collisions with delegation_test.go)
// ---------------------------------------------------------------------------

func createRootKeyWithProof(t *testing.T, mgr *Manager, owner, maxTotal string) (*SessionKey, *ecdsa.PrivateKey) {
	t.Helper()
	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	addr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()

	key, err := mgr.Create(context.Background(), owner, &SessionKeyRequest{
		PublicKey: addr,
		MaxTotal:  maxTotal,
		ExpiresIn: "1h",
		AllowAny:  true,
		Scopes:    []string{"spend", "delegate"},
	})
	if err != nil {
		t.Fatalf("create root key: %v", err)
	}
	return key, privKey
}

func signDelegationProof(t *testing.T, parentKey *ecdsa.PrivateKey, childAddr, maxTotal string, nonce uint64) *DelegateRequest {
	t.Helper()
	ts := time.Now().Unix()
	msg := CreateDelegationMessage(childAddr, maxTotal, nonce, ts)
	hash := HashMessage(msg)
	sig, err := crypto.Sign(hash, parentKey)
	if err != nil {
		t.Fatalf("sign delegation: %v", err)
	}
	sig[64] += 27

	return &DelegateRequest{
		PublicKey: childAddr,
		MaxTotal:  maxTotal,
		Nonce:     nonce,
		Timestamp: ts,
		Signature: "0x" + bytesToHexProof(sig),
		AllowAny:  true,
		Scopes:    []string{"spend", "delegate"},
	}
}

func freshAddrProof(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()
	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return crypto.PubkeyToAddress(privKey.PublicKey).Hex(), privKey
}

func bytesToHexProof(b []byte) string {
	const hextable = "0123456789abcdef"
	s := make([]byte, len(b)*2)
	for i, v := range b {
		s[i*2] = hextable[v>>4]
		s[i*2+1] = hextable[v&0x0f]
	}
	return string(s)
}
