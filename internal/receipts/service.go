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

// IssueReceipt signs and persists a receipt, linking it into the scope's
// append-only chain when the underlying store implements ChainStore.
// Nil-safe: returns nil if service or signer is nil.
//
// Concurrency: retries once on ErrChainHeadStale so two callers racing on
// the same scope converge without surfacing the race to business logic.
func (s *Service) IssueReceipt(ctx context.Context, req IssueRequest) error {
	if s == nil || s.signer == nil {
		return nil
	}

	scope := req.Scope
	if scope == "" {
		scope = DefaultScope
	}

	chainStore, chainable := s.store.(ChainStore)
	if !chainable {
		return s.issueUnchained(ctx, req, scope)
	}

	// Each retry pays one extra signer+marshal round trip. 32 is chosen so
	// that ~32 concurrent writers on a single scope can all converge without
	// any individual caller surfacing a spurious contention error; one loser
	// per round means the last-arriving writer needs ~N attempts.
	const maxAttempts = 32
	for attempt := 0; attempt < maxAttempts; attempt++ {
		head, err := chainStore.GetChainHead(ctx, scope)
		if err != nil {
			return fmt.Errorf("receipts: read chain head: %w", err)
		}

		receipt, buildErr := s.buildReceipt(req, scope, head.HeadHash, head.HeadIndex+1)
		if buildErr != nil {
			return buildErr
		}

		appendErr := chainStore.AppendReceipt(ctx, receipt)
		if appendErr == nil {
			return nil
		}
		if appendErr == ErrChainHeadStale {
			continue
		}
		return appendErr
	}
	return fmt.Errorf("receipts: chain head contention exceeded retry budget")
}

// issueUnchained persists a receipt without chain linkage. Used when the
// underlying store does not implement ChainStore (e.g. legacy stores in
// tests). The receipt still carries its Scope so future chain migrations
// can reconstruct linkage in historical data.
func (s *Service) issueUnchained(ctx context.Context, req IssueRequest, scope string) error {
	receipt, err := s.buildReceipt(req, scope, "", 0)
	if err != nil {
		return err
	}
	return s.store.Create(ctx, receipt)
}

// buildReceipt assembles the Receipt struct, computes PayloadHash, and signs.
// Pure function — no I/O — so callers can retry it on chain-head contention.
func (s *Service) buildReceipt(req IssueRequest, scope, prevHash string, chainIndex int64) (*Receipt, error) {
	payload := receiptPayload{
		Amount:     req.Amount,
		ChainIndex: chainIndex,
		From:       strings.ToLower(req.From),
		Path:       string(req.Path),
		PrevHash:   prevHash,
		Reference:  req.Reference,
		Scope:      scope,
		ServiceID:  req.ServiceID,
		Status:     req.Status,
		To:         strings.ToLower(req.To),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("receipts: marshal payload: %w", err)
	}
	hash := sha256.Sum256(data)
	payloadHash := fmt.Sprintf("%x", hash)

	sig, issuedAtStr, expiresAtStr, err := s.signer.Sign(payload)
	if err != nil {
		return nil, fmt.Errorf("receipts: sign: %w", err)
	}
	issuedAt, _ := time.Parse(time.RFC3339, issuedAtStr)
	expiresAt, _ := time.Parse(time.RFC3339, expiresAtStr)

	return &Receipt{
		ID:          idgen.WithPrefix("rcpt_"),
		PaymentPath: req.Path,
		Reference:   req.Reference,
		From:        strings.ToLower(req.From),
		To:          strings.ToLower(req.To),
		Amount:      req.Amount,
		ServiceID:   req.ServiceID,
		Status:      req.Status,
		Scope:       scope,
		ChainIndex:  chainIndex,
		PrevHash:    prevHash,
		PayloadHash: payloadHash,
		Signature:   sig,
		IssuedAt:    issuedAt,
		ExpiresAt:   expiresAt,
		Metadata:    req.Metadata,
		CreatedAt:   time.Now(),
	}, nil
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

// GetChainHead returns the full ChainHead for the given scope when the
// underlying store supports chain operations. Returns (nil, false, nil)
// when chain operations are not supported.
func (s *Service) GetChainHead(ctx context.Context, scope string) (*ChainHead, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	chainStore, supported := s.store.(ChainStore)
	if !supported {
		return nil, false, nil
	}
	head, err := chainStore.GetChainHead(ctx, scope)
	if err != nil {
		return nil, false, err
	}
	return head, true, nil
}

// ChainHead returns the current HEAD of the given scope when the underlying
// store supports chain operations. Returns ("", -1, false) otherwise so
// callers can treat chain support as optional.
func (s *Service) ChainHead(ctx context.Context, scope string) (hash string, index int64, ok bool, err error) {
	if s == nil {
		return "", -1, false, nil
	}
	chainStore, supported := s.store.(ChainStore)
	if !supported {
		return "", -1, false, nil
	}
	head, err := chainStore.GetChainHead(ctx, scope)
	if err != nil {
		return "", -1, false, err
	}
	return head.HeadHash, head.HeadIndex, true, nil
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
		Amount:     receipt.Amount,
		ChainIndex: receipt.ChainIndex,
		From:       receipt.From,
		Path:       string(receipt.PaymentPath),
		PrevHash:   receipt.PrevHash,
		Reference:  receipt.Reference,
		Scope:      scopeOrDefault(receipt.Scope),
		ServiceID:  receipt.ServiceID,
		Status:     receipt.Status,
		To:         receipt.To,
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
