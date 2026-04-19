package receipts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func issue(t *testing.T, svc *Service, scope, ref string) *Receipt {
	t.Helper()
	err := svc.IssueReceipt(context.Background(), IssueRequest{
		Path:      PathGateway,
		Reference: ref,
		From:      testBuyer,
		To:        testSeller,
		Amount:    "0.010000",
		Status:    "confirmed",
		Scope:     scope,
	})
	if err != nil {
		t.Fatalf("IssueReceipt(%q) failed: %v", ref, err)
	}
	receipts, err := svc.ListByReference(context.Background(), ref)
	if err != nil {
		t.Fatalf("ListByReference(%q) failed: %v", ref, err)
	}
	if len(receipts) != 1 {
		t.Fatalf("expected 1 receipt for %q, got %d", ref, len(receipts))
	}
	return receipts[0]
}

func TestChain_AppendProducesMonotonicIndex(t *testing.T) {
	svc := newTestService()

	r0 := issue(t, svc, "tenant_a", "ref_0")
	r1 := issue(t, svc, "tenant_a", "ref_1")
	r2 := issue(t, svc, "tenant_a", "ref_2")

	if r0.ChainIndex != 0 || r1.ChainIndex != 1 || r2.ChainIndex != 2 {
		t.Fatalf("expected monotonic indices 0,1,2; got %d,%d,%d",
			r0.ChainIndex, r1.ChainIndex, r2.ChainIndex)
	}
	if r0.PrevHash != "" {
		t.Errorf("first receipt PrevHash should be empty, got %q", r0.PrevHash)
	}
	if r1.PrevHash != r0.PayloadHash {
		t.Errorf("r1.PrevHash %q != r0.PayloadHash %q", r1.PrevHash, r0.PayloadHash)
	}
	if r2.PrevHash != r1.PayloadHash {
		t.Errorf("r2.PrevHash %q != r1.PayloadHash %q", r2.PrevHash, r1.PayloadHash)
	}
	if r0.Scope != "tenant_a" {
		t.Errorf("expected scope tenant_a, got %q", r0.Scope)
	}
}

func TestChain_ScopesAreIsolated(t *testing.T) {
	svc := newTestService()

	a0 := issue(t, svc, "tenant_a", "a0")
	b0 := issue(t, svc, "tenant_b", "b0")
	a1 := issue(t, svc, "tenant_a", "a1")
	b1 := issue(t, svc, "tenant_b", "b1")

	if a0.ChainIndex != 0 || a1.ChainIndex != 1 {
		t.Errorf("tenant_a indices wrong: got %d, %d", a0.ChainIndex, a1.ChainIndex)
	}
	if b0.ChainIndex != 0 || b1.ChainIndex != 1 {
		t.Errorf("tenant_b indices wrong: got %d, %d", b0.ChainIndex, b1.ChainIndex)
	}
	if a1.PrevHash != a0.PayloadHash {
		t.Errorf("tenant_a chain not linked")
	}
	if b1.PrevHash != b0.PayloadHash {
		t.Errorf("tenant_b chain not linked")
	}
	if a1.PrevHash == b1.PrevHash {
		t.Errorf("chains should have independent PrevHash values")
	}
}

func TestChain_DefaultScope(t *testing.T) {
	svc := newTestService()

	r0 := issue(t, svc, "", "ref_0") // empty → DefaultScope
	if r0.Scope != DefaultScope {
		t.Errorf("expected DefaultScope, got %q", r0.Scope)
	}
}

func TestVerifyChain_Intact(t *testing.T) {
	svc := newTestService()
	for i := 0; i < 5; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("ref_%d", i))
	}

	report, err := svc.VerifyChain(context.Background(), "tenant_a", 0, -1)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if report.Status != ChainIntact {
		t.Errorf("expected ChainIntact, got %s: %s", report.Status, report.Message)
	}
	if report.Count != 5 {
		t.Errorf("expected count 5, got %d", report.Count)
	}
	if report.LastIndex != 4 {
		t.Errorf("expected lastIndex 4, got %d", report.LastIndex)
	}
}

func TestVerifyChain_EmptyScope(t *testing.T) {
	svc := newTestService()
	report, err := svc.VerifyChain(context.Background(), "nothing_here", 0, -1)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if report.Status != ChainEmpty {
		t.Errorf("expected ChainEmpty, got %s", report.Status)
	}
}

func TestVerifyChain_DetectsTamperedPayloadHash(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, NewSigner(testSecret))
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}

	// Flip the PayloadHash on the middle receipt. Signature verification
	// would catch it anyway, but this exercises the hash-mismatch branch.
	rx, _ := store.ListByChain(context.Background(), "tenant_a", 1, 1)
	if len(rx) != 1 {
		t.Fatal("expected 1 receipt at index 1")
	}
	store.mu.Lock()
	stored := store.receipts[rx[0].ID]
	stored.PayloadHash = strings.Repeat("a", 64)
	store.mu.Unlock()

	report, err := svc.VerifyChain(context.Background(), "tenant_a", 0, -1)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if report.Status != ChainHashMismatch {
		t.Errorf("expected ChainHashMismatch, got %s", report.Status)
	}
	if report.BreakAtIndex == nil || *report.BreakAtIndex != 1 {
		t.Errorf("expected break at index 1, got %v", report.BreakAtIndex)
	}
}

func TestVerifyChain_DetectsTamperedPrevHash(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, NewSigner(testSecret))
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}

	rx, _ := store.ListByChain(context.Background(), "tenant_a", 2, 2)
	if len(rx) != 1 {
		t.Fatal("expected 1 receipt at index 2")
	}
	store.mu.Lock()
	stored := store.receipts[rx[0].ID]
	stored.PrevHash = strings.Repeat("b", 64)
	store.mu.Unlock()

	report, err := svc.VerifyChain(context.Background(), "tenant_a", 0, -1)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if report.Status != ChainLinkageBad {
		t.Errorf("expected ChainLinkageBad, got %s", report.Status)
	}
	if report.BreakAtIndex == nil || *report.BreakAtIndex != 2 {
		t.Errorf("expected break at index 2, got %v", report.BreakAtIndex)
	}
}

func TestVerifyChain_DetectsTamperedSignature(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, NewSigner(testSecret))
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}

	rx, _ := store.ListByChain(context.Background(), "tenant_a", 1, 1)
	store.mu.Lock()
	stored := store.receipts[rx[0].ID]
	// Recompute a matching PayloadHash so the hash-mismatch branch doesn't
	// trigger first; then flip only the signature to exercise the
	// ChainSignatureBad branch.
	payload := payloadFromReceipt(stored)
	data, _ := json.Marshal(payload)
	h := sha256.Sum256(data)
	stored.PayloadHash = hex.EncodeToString(h[:])
	stored.Signature = strings.Repeat("0", 64)
	store.mu.Unlock()

	report, err := svc.VerifyChain(context.Background(), "tenant_a", 0, -1)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if report.Status != ChainSignatureBad {
		t.Errorf("expected ChainSignatureBad, got %s: %s", report.Status, report.Message)
	}
}

func TestMerkleRoot_ReproducibleAndOrderDependent(t *testing.T) {
	svc := newTestService()
	for i := 0; i < 4; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}
	store := svc.store.(ChainStore)
	receipts, _ := store.ListByChain(context.Background(), "tenant_a", 0, -1)

	root1, err := MerkleRoot(receipts)
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}
	root2, err := MerkleRoot(receipts)
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}
	if root1 != root2 || root1 == "" {
		t.Errorf("expected reproducible non-empty Merkle root, got %q / %q", root1, root2)
	}

	swapped := []*Receipt{receipts[1], receipts[0], receipts[2], receipts[3]}
	rootSwapped, _ := MerkleRoot(swapped)
	if rootSwapped == root1 {
		t.Errorf("Merkle root should be order-dependent")
	}
}

func TestMerkleRoot_OddCountDuplicatesLast(t *testing.T) {
	svc := newTestService()
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}
	store := svc.store.(ChainStore)
	receipts, _ := store.ListByChain(context.Background(), "tenant_a", 0, -1)

	root, err := MerkleRoot(receipts)
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}
	if len(root) != 64 {
		t.Errorf("expected 64-hex-char root, got %q (len=%d)", root, len(root))
	}
}

func TestExportBundle_RoundTrip(t *testing.T) {
	svc := newTestService()
	for i := 0; i < 4; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}

	bundle, err := svc.ExportBundle(context.Background(), "tenant_a", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	if bundle.Format != BundleFormat {
		t.Errorf("bad format: %q", bundle.Format)
	}
	if bundle.Manifest.ReceiptCount != 4 {
		t.Errorf("expected 4 receipts, got %d", bundle.Manifest.ReceiptCount)
	}
	if bundle.Manifest.MerkleRoot == "" {
		t.Error("empty Merkle root")
	}
	if bundle.Manifest.Signature == "" {
		t.Error("empty manifest signature")
	}

	report, err := svc.VerifyBundle(bundle)
	if err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	if report.Status != ChainIntact {
		t.Errorf("expected ChainIntact, got %s: %s", report.Status, report.Message)
	}
	if report.Count != 4 {
		t.Errorf("expected count 4, got %d", report.Count)
	}
}

func TestVerifyBundle_DetectsManifestTamper(t *testing.T) {
	svc := newTestService()
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}
	bundle, err := svc.ExportBundle(context.Background(), "tenant_a", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}

	bundle.Manifest.MerkleRoot = strings.Repeat("c", 64)
	if _, err := svc.VerifyBundle(bundle); err == nil {
		t.Error("expected manifest signature verification to fail after root tamper")
	}
}

func TestVerifyBundle_DetectsReceiptTamper(t *testing.T) {
	svc := newTestService()
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}
	bundle, err := svc.ExportBundle(context.Background(), "tenant_a", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}

	// Tamper with a receipt inside the bundle. The manifest's Merkle root
	// will no longer match the recomputed one from the (altered) receipts.
	bundle.Receipts[1].Amount = "999.000000"
	if _, err := svc.VerifyBundle(bundle); err == nil {
		t.Error("expected bundle verification to fail after receipt tamper")
	}
}

func TestExportBundle_TimeRangeFilters(t *testing.T) {
	svc := newTestService()

	issue(t, svc, "tenant_a", "r0")
	time.Sleep(10 * time.Millisecond)
	midpoint := time.Now().UTC()
	time.Sleep(10 * time.Millisecond)
	issue(t, svc, "tenant_a", "r1")
	issue(t, svc, "tenant_a", "r2")

	bundle, err := svc.ExportBundle(context.Background(), "tenant_a", midpoint, time.Time{})
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	if bundle.Manifest.ReceiptCount != 2 {
		t.Errorf("expected 2 receipts after midpoint, got %d", bundle.Manifest.ReceiptCount)
	}
}

func TestChain_ConcurrentAppendsProduceNoForks(t *testing.T) {
	svc := newTestService()
	// Kept below Service.maxAttempts (32) to give the retry loop room even
	// when every goroutine synchronizes on the same mutex boundary.
	const total = 20

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := svc.IssueReceipt(context.Background(), IssueRequest{
				Path: PathGateway, Reference: fmt.Sprintf("cc_%d", i),
				From: testBuyer, To: testSeller,
				Amount: "0.001000", Status: "confirmed",
				Scope: "tenant_concurrent",
			})
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("concurrent IssueReceipt errors: %v", errs)
	}

	report, err := svc.VerifyChain(context.Background(), "tenant_concurrent", 0, -1)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if report.Status != ChainIntact {
		t.Fatalf("chain not intact under concurrent append: %s (%s)", report.Status, report.Message)
	}
	if report.Count != total {
		t.Fatalf("expected %d receipts, got %d", total, report.Count)
	}
}
