package receipts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

// Service implements receipt business logic.
type Service struct {
	store  Store
	signer *Signer
}

// NewService creates a new receipt service.
// If signer is nil, IssueReceipt is a no-op (signing disabled).
func NewService(store Store, signer *Signer) *Service {
	return &Service{
		store:  store,
		signer: signer,
	}
}

// IssueReceipt signs and persists a receipt. Nil-safe: returns nil if service or signer is nil.
func (s *Service) IssueReceipt(ctx context.Context, req IssueRequest) error {
	if s == nil || s.signer == nil {
		return nil
	}

	payload := receiptPayload{
		Amount:    req.Amount,
		From:      strings.ToLower(req.From),
		Path:      string(req.Path),
		Reference: req.Reference,
		ServiceID: req.ServiceID,
		Status:    req.Status,
		To:        strings.ToLower(req.To),
	}

	// Compute payload hash
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("receipts: failed to marshal payload: %w", err)
	}
	hash := sha256.Sum256(data)
	payloadHash := fmt.Sprintf("%x", hash)

	// Sign
	sig, issuedAtStr, expiresAtStr, err := s.signer.Sign(payload)
	if err != nil {
		return fmt.Errorf("receipts: failed to sign: %w", err)
	}

	issuedAt, _ := time.Parse(time.RFC3339, issuedAtStr)
	expiresAt, _ := time.Parse(time.RFC3339, expiresAtStr)

	receipt := &Receipt{
		ID:          idgen.WithPrefix("rcpt_"),
		PaymentPath: req.Path,
		Reference:   req.Reference,
		From:        strings.ToLower(req.From),
		To:          strings.ToLower(req.To),
		Amount:      req.Amount,
		ServiceID:   req.ServiceID,
		Status:      req.Status,
		PayloadHash: payloadHash,
		Signature:   sig,
		IssuedAt:    issuedAt,
		ExpiresAt:   expiresAt,
		Metadata:    req.Metadata,
		CreatedAt:   time.Now(),
	}

	return s.store.Create(ctx, receipt)
}

// Get returns a receipt by ID.
func (s *Service) Get(ctx context.Context, id string) (*Receipt, error) {
	return s.store.Get(ctx, id)
}

// ListByAgent returns receipts where the agent is either buyer or seller.
func (s *Service) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Receipt, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListByAgent(ctx, strings.ToLower(agentAddr), limit)
}

// ListByReference returns receipts for a given reference (e.g. session ID, stream ID).
func (s *Service) ListByReference(ctx context.Context, reference string) ([]*Receipt, error) {
	return s.store.ListByReference(ctx, reference)
}

// Verify checks whether a receipt's signature is valid.
func (s *Service) Verify(ctx context.Context, receiptID string) (*VerifyResponse, error) {
	if s.signer == nil {
		return &VerifyResponse{
			Valid:     false,
			ReceiptID: receiptID,
			Error:     ErrSigningDisabled.Error(),
		}, nil
	}

	receipt, err := s.store.Get(ctx, receiptID)
	if err != nil {
		return &VerifyResponse{
			Valid:     false,
			ReceiptID: receiptID,
			Error:     ErrReceiptNotFound.Error(),
		}, nil
	}

	payload := receiptPayload{
		Amount:    receipt.Amount,
		From:      receipt.From,
		Path:      string(receipt.PaymentPath),
		Reference: receipt.Reference,
		ServiceID: receipt.ServiceID,
		Status:    receipt.Status,
		To:        receipt.To,
	}

	valid := s.signer.Verify(payload, receipt.Signature)

	resp := &VerifyResponse{
		Valid:     valid,
		ReceiptID: receiptID,
	}

	if valid && time.Now().After(receipt.ExpiresAt) {
		resp.Expired = true
	}

	if !valid {
		resp.Error = "signature verification failed"
	}

	return resp, nil
}
