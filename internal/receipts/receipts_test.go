package receipts

import (
	"context"
	"testing"
	"time"
)

const (
	testBuyer  = "0x1111111111111111111111111111111111111111"
	testSeller = "0x2222222222222222222222222222222222222222"
	testSecret = "test-hmac-secret-for-receipts"
)

func newTestService() *Service {
	return NewService(NewMemoryStore(), NewSigner(testSecret))
}

func issueTestReceipt(t *testing.T, svc *Service, path PaymentPath, ref, status string) {
	t.Helper()
	err := svc.IssueReceipt(context.Background(), IssueRequest{
		Path:      path,
		Reference: ref,
		From:      testBuyer,
		To:        testSeller,
		Amount:    "0.005000",
		ServiceID: "svc_translation",
		Status:    status,
		Metadata:  "test receipt",
	})
	if err != nil {
		t.Fatalf("IssueReceipt failed: %v", err)
	}
}

func TestIssueReceipt_Success(t *testing.T) {
	svc := newTestService()
	issueTestReceipt(t, svc, PathGateway, "gw_session123", "confirmed")

	// Verify receipt was persisted
	receipts, err := svc.ListByAgent(context.Background(), testBuyer, 10)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("expected 1 receipt, got %d", len(receipts))
	}

	r := receipts[0]
	if r.PaymentPath != PathGateway {
		t.Errorf("expected path gateway, got %s", r.PaymentPath)
	}
	if r.Reference != "gw_session123" {
		t.Errorf("expected reference gw_session123, got %s", r.Reference)
	}
	if r.From != testBuyer {
		t.Errorf("expected from %s, got %s", testBuyer, r.From)
	}
	if r.Amount != "0.005000" {
		t.Errorf("expected amount 0.005000, got %s", r.Amount)
	}
	if r.Signature == "" {
		t.Error("expected non-empty signature")
	}
	if r.PayloadHash == "" {
		t.Error("expected non-empty payload hash")
	}
	if r.IssuedAt.IsZero() {
		t.Error("expected non-zero issuedAt")
	}
	if r.ExpiresAt.IsZero() {
		t.Error("expected non-zero expiresAt")
	}
	// Should expire ~30 days from now
	expectedExpiry := time.Now().Add(30 * 24 * time.Hour)
	if r.ExpiresAt.Before(expectedExpiry.Add(-time.Minute)) {
		t.Errorf("expiresAt too early: %v", r.ExpiresAt)
	}
}

func TestIssueReceipt_NilSigner(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil) // no signer

	err := svc.IssueReceipt(context.Background(), IssueRequest{
		Path:      PathGateway,
		Reference: "ref123",
		From:      testBuyer,
		To:        testSeller,
		Amount:    "1.000000",
		Status:    "confirmed",
	})
	if err != nil {
		t.Fatalf("expected nil error for nil signer, got %v", err)
	}

	// No receipt should be stored
	receipts, _ := svc.ListByAgent(context.Background(), testBuyer, 10)
	if len(receipts) != 0 {
		t.Errorf("expected 0 receipts with nil signer, got %d", len(receipts))
	}
}

func TestIssueReceipt_NilService(t *testing.T) {
	var svc *Service
	err := svc.IssueReceipt(context.Background(), IssueRequest{
		Path:      PathGateway,
		Reference: "ref123",
		From:      testBuyer,
		To:        testSeller,
		Amount:    "1.000000",
		Status:    "confirmed",
	})
	if err != nil {
		t.Fatalf("expected nil error for nil service, got %v", err)
	}
}

func TestVerify_Valid(t *testing.T) {
	svc := newTestService()
	issueTestReceipt(t, svc, PathStream, "str_abc", "confirmed")

	receipts, _ := svc.ListByAgent(context.Background(), testBuyer, 10)
	if len(receipts) == 0 {
		t.Fatal("no receipts found")
	}

	resp, err := svc.Verify(context.Background(), receipts[0].ID)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if !resp.Valid {
		t.Errorf("expected valid receipt, got invalid: %s", resp.Error)
	}
	if resp.Expired {
		t.Error("expected not expired")
	}
}

func TestVerify_InvalidSignature(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, NewSigner(testSecret))

	issueTestReceipt(t, svc, PathEscrow, "esc_123", "confirmed")

	receipts, _ := svc.ListByAgent(context.Background(), testBuyer, 10)
	if len(receipts) == 0 {
		t.Fatal("no receipts found")
	}

	// Tamper with the signature in the store
	r := receipts[0]
	r.Signature = "deadbeef"
	_ = store.Create(context.Background(), r) // overwrite with tampered sig
	// Need to update the existing receipt - overwrite by re-inserting with same ID
	store.mu.Lock()
	store.receipts[r.ID] = r
	store.mu.Unlock()

	resp, err := svc.Verify(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if resp.Valid {
		t.Error("expected invalid for tampered signature")
	}
}

func TestVerify_NotFound(t *testing.T) {
	svc := newTestService()

	resp, err := svc.Verify(context.Background(), "nonexistent_id")
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if resp.Valid {
		t.Error("expected invalid for not-found receipt")
	}
	if resp.Error != ErrReceiptNotFound.Error() {
		t.Errorf("expected not_found error, got %s", resp.Error)
	}
}

func TestVerify_SigningDisabled(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)

	resp, err := svc.Verify(context.Background(), "any_id")
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if resp.Valid {
		t.Error("expected invalid when signing disabled")
	}
	if resp.Error != ErrSigningDisabled.Error() {
		t.Errorf("expected signing_disabled error, got %s", resp.Error)
	}
}

func TestListByAgent_BothSides(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	// Issue receipt where testBuyer is "from"
	_ = svc.IssueReceipt(ctx, IssueRequest{
		Path: PathGateway, Reference: "ref1",
		From: testBuyer, To: testSeller,
		Amount: "0.001000", Status: "confirmed",
	})

	// Issue receipt where testBuyer is "to" (reversed roles)
	_ = svc.IssueReceipt(ctx, IssueRequest{
		Path: PathEscrow, Reference: "ref2",
		From: testSeller, To: testBuyer,
		Amount: "0.002000", Status: "confirmed",
	})

	receipts, err := svc.ListByAgent(ctx, testBuyer, 10)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(receipts) != 2 {
		t.Errorf("expected 2 receipts for buyer (as from and to), got %d", len(receipts))
	}
}

func TestListByReference(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	_ = svc.IssueReceipt(ctx, IssueRequest{
		Path: PathStream, Reference: "str_shared_ref",
		From: testBuyer, To: testSeller,
		Amount: "0.050000", Status: "confirmed",
	})
	_ = svc.IssueReceipt(ctx, IssueRequest{
		Path: PathStream, Reference: "str_shared_ref",
		From: testBuyer, To: testSeller,
		Amount: "0.010000", Status: "confirmed",
	})
	_ = svc.IssueReceipt(ctx, IssueRequest{
		Path: PathStream, Reference: "str_other_ref",
		From: testBuyer, To: testSeller,
		Amount: "0.003000", Status: "confirmed",
	})

	receipts, err := svc.ListByReference(ctx, "str_shared_ref")
	if err != nil {
		t.Fatalf("ListByReference failed: %v", err)
	}
	if len(receipts) != 2 {
		t.Errorf("expected 2 receipts for shared ref, got %d", len(receipts))
	}
}

func TestListByAgent_Limit(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = svc.IssueReceipt(ctx, IssueRequest{
			Path: PathGateway, Reference: "ref",
			From: testBuyer, To: testSeller,
			Amount: "0.001000", Status: "confirmed",
		})
	}

	receipts, err := svc.ListByAgent(ctx, testBuyer, 3)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(receipts) != 3 {
		t.Errorf("expected 3 receipts (limited), got %d", len(receipts))
	}
}

func TestSigner_SignAndVerify(t *testing.T) {
	s := NewSigner(testSecret)

	payload := map[string]string{"key": "value"}
	sig, issuedAt, expiresAt, err := s.Sign(payload)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	if sig == "" || issuedAt == "" || expiresAt == "" {
		t.Fatal("expected non-empty signature, issuedAt, expiresAt")
	}

	if !s.Verify(payload, sig) {
		t.Error("expected Verify to return true for valid signature")
	}

	if s.Verify(payload, "wrong_signature") {
		t.Error("expected Verify to return false for wrong signature")
	}

	// Tampered payload
	if s.Verify(map[string]string{"key": "tampered"}, sig) {
		t.Error("expected Verify to return false for tampered payload")
	}
}

func TestSigner_Nil(t *testing.T) {
	s := NewSigner("")
	if s != nil {
		t.Error("expected nil signer for empty secret")
	}

	sig, _, _, err := s.Sign(map[string]string{"key": "value"})
	if err != nil {
		t.Errorf("expected nil error for nil signer, got %v", err)
	}
	if sig != "" {
		t.Error("expected empty signature for nil signer")
	}

	if s.Verify(map[string]string{"key": "value"}, "anything") {
		t.Error("expected Verify to return false for nil signer")
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newTestService()

	_, err := svc.Get(context.Background(), "nonexistent")
	if err != ErrReceiptNotFound {
		t.Errorf("expected ErrReceiptNotFound, got %v", err)
	}
}

func TestAllPaymentPaths(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	paths := []PaymentPath{PathGateway, PathStream, PathSessionKey, PathEscrow}
	for _, path := range paths {
		err := svc.IssueReceipt(ctx, IssueRequest{
			Path: path, Reference: "ref_" + string(path),
			From: testBuyer, To: testSeller,
			Amount: "0.001000", Status: "confirmed",
		})
		if err != nil {
			t.Errorf("IssueReceipt failed for path %s: %v", path, err)
		}
	}

	receipts, _ := svc.ListByAgent(ctx, testBuyer, 10)
	if len(receipts) != 4 {
		t.Errorf("expected 4 receipts (one per path), got %d", len(receipts))
	}
}
