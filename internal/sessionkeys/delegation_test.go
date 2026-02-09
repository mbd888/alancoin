package sessionkeys

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

// helper: create a root session key with a real ECDSA keypair
func createRootKey(t *testing.T, mgr *Manager, owner string, maxTotal string) (*SessionKey, *ecdsa.PrivateKey) {
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
	})
	if err != nil {
		t.Fatalf("create root key: %v", err)
	}
	return key, privKey
}

// helper: sign a delegation request
func signDelegation(t *testing.T, parentKey *ecdsa.PrivateKey, childAddr string, maxTotal string, nonce uint64) *DelegateRequest {
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
		Signature: "0x" + bytesToHex(sig),
		AllowAny:  true,
	}
}

// helper: generate a fresh address
func freshAddr(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()
	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return crypto.PubkeyToAddress(privKey.PublicKey).Hex(), privKey
}

// ---------------------------------------------------------------------------
// CreateDelegated tests
// ---------------------------------------------------------------------------

func TestCreateDelegated_Basic(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "10.00")
	childAddr, _ := freshAddr(t)

	req := signDelegation(t, parentPriv, childAddr, "5.00", 1)
	child, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if err != nil {
		t.Fatalf("CreateDelegated: %v", err)
	}

	if child.ParentKeyID != parent.ID {
		t.Errorf("ParentKeyID = %q, want %q", child.ParentKeyID, parent.ID)
	}
	if child.Depth != 1 {
		t.Errorf("Depth = %d, want 1", child.Depth)
	}
	if child.RootKeyID != parent.ID {
		t.Errorf("RootKeyID = %q, want %q", child.RootKeyID, parent.ID)
	}
	if child.Permission.MaxTotal != "5.00" {
		t.Errorf("MaxTotal = %q, want 5.00", child.Permission.MaxTotal)
	}
	if !child.IsActive() {
		t.Error("child should be active")
	}
}

func TestCreateDelegated_BudgetSubset(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "5.00")
	childAddr, _ := freshAddr(t)

	// Child wants more than parent has
	req := signDelegation(t, parentPriv, childAddr, "6.00", 1)
	_, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if !errors.Is(err, ErrChildExceedsParent) {
		t.Errorf("expected ErrChildExceedsParent, got %v", err)
	}
}

func TestCreateDelegated_OverAllocation(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "10.00")

	// Create child A with budget 7.00
	childAddrA, _ := freshAddr(t)
	reqA := signDelegation(t, parentPriv, childAddrA, "7.00", 1)
	_, err := mgr.CreateDelegated(ctx, parent.ID, reqA)
	if err != nil {
		t.Fatalf("child A: %v", err)
	}

	// Try to create child B with 7.00 — should fail because 7 + 7 > 10
	childAddrB, _ := freshAddr(t)
	reqB := signDelegation(t, parentPriv, childAddrB, "7.00", 2)
	_, err = mgr.CreateDelegated(ctx, parent.ID, reqB)
	if !errors.Is(err, ErrChildExceedsParent) {
		t.Errorf("expected ErrChildExceedsParent for over-allocation, got %v", err)
	}

	// Child B with 3.00 should succeed (3 + 7 = 10)
	reqB2 := signDelegation(t, parentPriv, childAddrB, "3.00", 3)
	childB, err := mgr.CreateDelegated(ctx, parent.ID, reqB2)
	if err != nil {
		t.Fatalf("child B (3.00): %v", err)
	}
	if childB.Permission.MaxTotal != "3.00" {
		t.Errorf("child B MaxTotal = %q, want 3.00", childB.Permission.MaxTotal)
	}
}

func TestCreateDelegated_DepthLimit(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	owner := "0x0000000000000000000000000000000000001111"
	current, currentPriv := createRootKey(t, mgr, owner, "100.00")

	// Chain 5 levels deep (depth 0 → 5), which is the max
	for i := 1; i <= MaxDelegationDepth; i++ {
		childAddr, childPriv := freshAddr(t)
		req := signDelegation(t, currentPriv, childAddr, "10.00", 1)
		child, err := mgr.CreateDelegated(ctx, current.ID, req)
		if err != nil {
			t.Fatalf("depth %d: %v", i, err)
		}
		if child.Depth != i {
			t.Errorf("depth %d: got Depth=%d", i, child.Depth)
		}
		current = child
		currentPriv = childPriv
	}

	// Depth 6 should fail
	childAddr, _ := freshAddr(t)
	req := signDelegation(t, currentPriv, childAddr, "1.00", 1)
	_, err := mgr.CreateDelegated(ctx, current.ID, req)
	if !errors.Is(err, ErrMaxDepthExceeded) {
		t.Errorf("expected ErrMaxDepthExceeded at depth %d, got %v", MaxDelegationDepth+1, err)
	}
}

func TestCreateDelegated_ServiceTypeIntersection(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Create parent with restricted service types
	parentPriv, _ := crypto.GenerateKey()
	parentAddr := crypto.PubkeyToAddress(parentPriv.PublicKey).Hex()
	parent, _ := mgr.Create(ctx, "0x0000000000000000000000000000000000001111", &SessionKeyRequest{
		PublicKey:           parentAddr,
		MaxTotal:            "10.00",
		ExpiresIn:           "1h",
		AllowedServiceTypes: []string{"translation", "inference"},
	})

	// Child requests service type that parent allows
	childAddr, _ := freshAddr(t)
	req := signDelegation(t, parentPriv, childAddr, "5.00", 1)
	req.AllowAny = false
	req.AllowedServiceTypes = []string{"translation"}
	child, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if err != nil {
		t.Fatalf("intersection should work: %v", err)
	}
	if len(child.Permission.AllowedServiceTypes) != 1 || child.Permission.AllowedServiceTypes[0] != "translation" {
		t.Errorf("expected [translation], got %v", child.Permission.AllowedServiceTypes)
	}

	// Child requests service type parent doesn't allow
	childAddr2, _ := freshAddr(t)
	req2 := signDelegation(t, parentPriv, childAddr2, "5.00", 2)
	req2.AllowAny = false
	req2.AllowedServiceTypes = []string{"code_review"}
	_, err = mgr.CreateDelegated(ctx, parent.ID, req2)
	if !errors.Is(err, ErrChildServiceNotAllowed) {
		t.Errorf("expected ErrChildServiceNotAllowed, got %v", err)
	}
}

func TestCreateDelegated_MaxPerDayValidation(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	parentPriv, _ := crypto.GenerateKey()
	parentAddr := crypto.PubkeyToAddress(parentPriv.PublicKey).Hex()
	parent, _ := mgr.Create(ctx, "0x0000000000000000000000000000000000001111", &SessionKeyRequest{
		PublicKey: parentAddr,
		MaxTotal:  "10.00",
		MaxPerDay: "2.00",
		ExpiresIn: "1h",
		AllowAny:  true,
	})

	// Child with higher daily limit should fail
	childAddr, _ := freshAddr(t)
	req := signDelegation(t, parentPriv, childAddr, "5.00", 1)
	req.MaxPerDay = "5.00"
	_, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if !errors.Is(err, ErrChildExceedsParent) {
		t.Errorf("expected ErrChildExceedsParent for daily limit, got %v", err)
	}

	// Child with equal or lower daily limit should work
	req2 := signDelegation(t, parentPriv, childAddr, "5.00", 2)
	req2.MaxPerDay = "1.50"
	child, err := mgr.CreateDelegated(ctx, parent.ID, req2)
	if err != nil {
		t.Fatalf("lower daily limit should work: %v", err)
	}
	if child.Permission.MaxPerDay != "1.50" {
		t.Errorf("MaxPerDay = %q, want 1.50", child.Permission.MaxPerDay)
	}
}

func TestCreateDelegated_ParentNotActive(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "10.00")

	// Revoke the parent
	if err := mgr.Revoke(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}

	childAddr, _ := freshAddr(t)
	req := signDelegation(t, parentPriv, childAddr, "5.00", 1)
	_, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if !errors.Is(err, ErrParentNotActive) {
		t.Errorf("expected ErrParentNotActive, got %v", err)
	}
}

func TestCreateDelegated_WrongSignature(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, _ := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "10.00")

	// Sign with a different key
	_, wrongPriv := freshAddr(t)
	childAddr, _ := freshAddr(t)
	req := signDelegation(t, wrongPriv, childAddr, "5.00", 1)
	_, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("expected ErrSignatureMismatch, got %v", err)
	}
}

func TestCreateDelegated_ChildExpiresWithParent(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "10.00")

	// Create child with longer expiry — should be capped to parent's
	childAddr, _ := freshAddr(t)
	req := signDelegation(t, parentPriv, childAddr, "5.00", 1)
	req.ExpiresIn = "48h"
	child, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Child should not outlive parent
	if child.Permission.ExpiresAt.After(parent.Permission.ExpiresAt) {
		t.Error("child expiry should not exceed parent's")
	}
}

// ---------------------------------------------------------------------------
// RecordUsageWithCascade tests
// ---------------------------------------------------------------------------

func TestRecordUsageWithCascade(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "10.00")

	childAddr, _ := freshAddr(t)
	req := signDelegation(t, parentPriv, childAddr, "5.00", 1)
	child, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if err != nil {
		t.Fatal(err)
	}

	// Record usage on child, should cascade to parent
	err = mgr.RecordUsageWithCascade(ctx, child.ID, "2.00", 1)
	if err != nil {
		t.Fatalf("RecordUsageWithCascade: %v", err)
	}

	// Check child's usage
	child, _ = mgr.Get(ctx, child.ID)
	if child.Usage.TotalSpent != "2.000000" {
		t.Errorf("child TotalSpent = %q, want 2.000000", child.Usage.TotalSpent)
	}

	// Check parent's usage was cascaded
	parent, _ = mgr.Get(ctx, parent.ID)
	if parent.Usage.TotalSpent != "2.000000" {
		t.Errorf("parent TotalSpent = %q, want 2.000000", parent.Usage.TotalSpent)
	}
	if parent.Usage.TransactionCount != 1 {
		t.Errorf("parent TransactionCount = %d, want 1", parent.Usage.TransactionCount)
	}
}

func TestRecordUsageWithCascade_ThreeLevels(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	root, rootPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	// Level 1
	midAddr, midPriv := freshAddr(t)
	midReq := signDelegation(t, rootPriv, midAddr, "50.00", 1)
	mid, _ := mgr.CreateDelegated(ctx, root.ID, midReq)

	// Level 2
	leafAddr, _ := freshAddr(t)
	leafReq := signDelegation(t, midPriv, leafAddr, "20.00", 1)
	leaf, _ := mgr.CreateDelegated(ctx, mid.ID, leafReq)

	// Spend at leaf level
	err := mgr.RecordUsageWithCascade(ctx, leaf.ID, "5.00", 1)
	if err != nil {
		t.Fatal(err)
	}

	// All three levels should reflect the spend
	leaf, _ = mgr.Get(ctx, leaf.ID)
	mid, _ = mgr.Get(ctx, mid.ID)
	root, _ = mgr.Get(ctx, root.ID)

	if leaf.Usage.TotalSpent != "5.000000" {
		t.Errorf("leaf = %q", leaf.Usage.TotalSpent)
	}
	if mid.Usage.TotalSpent != "5.000000" {
		t.Errorf("mid = %q", mid.Usage.TotalSpent)
	}
	if root.Usage.TotalSpent != "5.000000" {
		t.Errorf("root = %q", root.Usage.TotalSpent)
	}
}

func TestRecordUsageWithCascade_AncestorBudgetEnforced(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	// Parent has 5.00 budget. Two children: 3.00 and 2.00.
	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "5.00")

	childAddr, _ := freshAddr(t)
	req := signDelegation(t, parentPriv, childAddr, "3.00", 1)
	child, err := mgr.CreateDelegated(ctx, parent.ID, req)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	child2Addr, _ := freshAddr(t)
	req2 := signDelegation(t, parentPriv, child2Addr, "2.00", 2)
	child2, err := mgr.CreateDelegated(ctx, parent.ID, req2)
	if err != nil {
		t.Fatalf("create child2: %v", err)
	}

	// Spend 3.00 on child1 — ok (child: 3.00 ≤ 3.00, parent: 3.00 ≤ 5.00)
	err = mgr.RecordUsageWithCascade(ctx, child.ID, "3.00", 1)
	if err != nil {
		t.Fatalf("child1 spend 3.00: %v", err)
	}

	// child2 tries to spend 2.00 — child2 budget ok (2.00 ≤ 2.00),
	// parent: 3.00 + 2.00 = 5.00 ≤ 5.00, ok
	err = mgr.RecordUsageWithCascade(ctx, child2.ID, "2.00", 1)
	if err != nil {
		t.Fatalf("child2 spend 2.00: %v", err)
	}

	parent, _ = mgr.Get(ctx, parent.ID)
	if parent.Usage.TotalSpent != "5.000000" {
		t.Errorf("parent TotalSpent = %q, want 5.000000", parent.Usage.TotalSpent)
	}
}

// ---------------------------------------------------------------------------
// ValidateAncestorChain tests
// ---------------------------------------------------------------------------

func TestValidateAncestorChain_RevokedAncestor(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	root, rootPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	childAddr, _ := freshAddr(t)
	req := signDelegation(t, rootPriv, childAddr, "50.00", 1)
	child, _ := mgr.CreateDelegated(ctx, root.ID, req)

	// Revoke root
	mgr.Revoke(ctx, root.ID)

	// Ancestor chain validation should fail for child
	child, _ = mgr.Get(ctx, child.ID)
	err := mgr.ValidateAncestorChain(ctx, child)
	if !errors.Is(err, ErrAncestorInvalid) {
		t.Errorf("expected ErrAncestorInvalid, got %v", err)
	}
}

func TestValidateAncestorChain_WithBudgetCheck(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "5.00")

	childAddr, _ := freshAddr(t)
	req := signDelegation(t, parentPriv, childAddr, "5.00", 1)
	child, _ := mgr.CreateDelegated(ctx, parent.ID, req)

	// Spend 4.00 on parent directly
	mgr.RecordUsage(ctx, parent.ID, "4.00", 1)

	// Ancestor chain check with amount 2.00 should fail (4 + 2 = 6 > 5)
	child, _ = mgr.Get(ctx, child.ID)
	err := mgr.ValidateAncestorChain(ctx, child, "2.00")
	if !errors.Is(err, ErrExceedsTotal) {
		t.Errorf("expected ErrExceedsTotal, got %v", err)
	}

	// Amount 1.00 should pass (4 + 1 = 5 ≤ 5)
	err = mgr.ValidateAncestorChain(ctx, child, "1.00")
	if err != nil {
		t.Errorf("1.00 should be within budget: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cascading Revocation tests
// ---------------------------------------------------------------------------

func TestRevoke_CascadesToChildren(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	root, rootPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	// Create child
	childAddr, childPriv := freshAddr(t)
	childReq := signDelegation(t, rootPriv, childAddr, "50.00", 1)
	child, _ := mgr.CreateDelegated(ctx, root.ID, childReq)

	// Create grandchild
	gcAddr, _ := freshAddr(t)
	gcReq := signDelegation(t, childPriv, gcAddr, "20.00", 1)
	grandchild, _ := mgr.CreateDelegated(ctx, child.ID, gcReq)

	// All should be active
	if !root.IsActive() || !child.IsActive() || !grandchild.IsActive() {
		t.Fatal("all keys should start active")
	}

	// Revoke root
	err := mgr.Revoke(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}

	// All should be revoked
	root, _ = mgr.Get(ctx, root.ID)
	child, _ = mgr.Get(ctx, child.ID)
	grandchild, _ = mgr.Get(ctx, grandchild.ID)

	if root.RevokedAt == nil {
		t.Error("root should be revoked")
	}
	if child.RevokedAt == nil {
		t.Error("child should be revoked (cascade)")
	}
	if grandchild.RevokedAt == nil {
		t.Error("grandchild should be revoked (cascade)")
	}
}

func TestRevoke_OnlyAffectsDescendants(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	root, rootPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	// Create two children
	childA_Addr, _ := freshAddr(t)
	childA_Req := signDelegation(t, rootPriv, childA_Addr, "40.00", 1)
	childA, _ := mgr.CreateDelegated(ctx, root.ID, childA_Req)

	childB_Addr, _ := freshAddr(t)
	childB_Req := signDelegation(t, rootPriv, childB_Addr, "40.00", 2)
	childB, _ := mgr.CreateDelegated(ctx, root.ID, childB_Req)

	// Revoke only child A
	mgr.Revoke(ctx, childA.ID)

	childA, _ = mgr.Get(ctx, childA.ID)
	childB, _ = mgr.Get(ctx, childB.ID)
	root, _ = mgr.Get(ctx, root.ID)

	if childA.RevokedAt == nil {
		t.Error("childA should be revoked")
	}
	if childB.RevokedAt != nil {
		t.Error("childB should NOT be revoked")
	}
	if root.RevokedAt != nil {
		t.Error("root should NOT be revoked")
	}
}

// ---------------------------------------------------------------------------
// LockKeyChain tests
// ---------------------------------------------------------------------------

func TestLockKeyChain_NoDeadlock(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	root, rootPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "100.00")

	childAddr, _ := freshAddr(t)
	req := signDelegation(t, rootPriv, childAddr, "50.00", 1)
	child, _ := mgr.CreateDelegated(ctx, root.ID, req)

	// Lock and unlock should not deadlock
	done := make(chan bool, 1)
	go func() {
		unlock := mgr.LockKeyChain(ctx, child.ID)
		unlock()
		done <- true
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("LockKeyChain deadlocked")
	}
}

func TestLockKeyChain_SerializesSiblings(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "10.00")

	childA_Addr, _ := freshAddr(t)
	childA_Req := signDelegation(t, parentPriv, childA_Addr, "5.00", 1)
	childA, _ := mgr.CreateDelegated(ctx, parent.ID, childA_Req)

	childB_Addr, _ := freshAddr(t)
	childB_Req := signDelegation(t, parentPriv, childB_Addr, "5.00", 2)
	childB, _ := mgr.CreateDelegated(ctx, parent.ID, childB_Req)

	// Lock chain for childA (locks childA + parent)
	unlockA := mgr.LockKeyChain(ctx, childA.ID)

	// Try to lock chain for childB — should block because parent is locked
	blocked := make(chan bool, 1)
	go func() {
		unlockB := mgr.LockKeyChain(ctx, childB.ID)
		blocked <- true
		unlockB()
	}()

	// Give goroutine time to attempt lock
	time.Sleep(50 * time.Millisecond)

	select {
	case <-blocked:
		t.Error("childB should be blocked while childA holds parent lock")
	default:
		// expected: childB is blocked
	}

	// Release childA's locks
	unlockA()

	// childB should now complete
	select {
	case <-blocked:
		// ok, childB acquired and released
	case <-time.After(2 * time.Second):
		t.Fatal("childB never acquired lock after childA released")
	}
}

// ---------------------------------------------------------------------------
// Integration: concurrent siblings can't exceed parent budget
// ---------------------------------------------------------------------------

func TestConcurrentSiblings_BudgetEnforced(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	ctx := context.Background()

	parent, parentPriv := createRootKey(t, mgr, "0x0000000000000000000000000000000000001111", "5.00")

	// Create two children, each with 4.00 budget
	// Over-allocation is now prevented, so we need children with budgets that fit
	childA_Addr, _ := freshAddr(t)
	childA_Req := signDelegation(t, parentPriv, childA_Addr, "3.00", 1)
	childA, _ := mgr.CreateDelegated(ctx, parent.ID, childA_Req)

	childB_Addr, _ := freshAddr(t)
	childB_Req := signDelegation(t, parentPriv, childB_Addr, "2.00", 2)
	childB, _ := mgr.CreateDelegated(ctx, parent.ID, childB_Req)

	// Concurrently spend on both children
	var wg sync.WaitGroup
	errA := make(chan error, 1)
	errB := make(chan error, 1)

	wg.Add(2)
	go func() {
		defer wg.Done()
		// Lock chain, record usage, unlock
		unlock := mgr.LockKeyChain(ctx, childA.ID)
		defer unlock()
		errA <- mgr.RecordUsageWithCascade(ctx, childA.ID, "3.00", 1)
	}()
	go func() {
		defer wg.Done()
		unlock := mgr.LockKeyChain(ctx, childB.ID)
		defer unlock()
		errB <- mgr.RecordUsageWithCascade(ctx, childB.ID, "2.00", 1)
	}()
	wg.Wait()

	eA := <-errA
	eB := <-errB

	// Both should succeed since 3 + 2 = 5 ≤ 5
	if eA != nil {
		t.Errorf("childA spend failed: %v", eA)
	}
	if eB != nil {
		t.Errorf("childB spend failed: %v", eB)
	}

	parent, _ = mgr.Get(ctx, parent.ID)
	if parent.Usage.TotalSpent != "5.000000" {
		t.Errorf("parent TotalSpent = %q, want 5.000000", parent.Usage.TotalSpent)
	}
}
