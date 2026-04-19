// Package receipts provides cryptographic receipt signing for all payment paths.
//
// Every payment through the platform (gateway, streams, escrow, session keys)
// produces a signed receipt that buyers and sellers can independently verify.
//
// Receipts are additionally linked into append-only hash chains scoped by an
// opaque Scope string. Each receipt commits to its predecessor's PayloadHash,
// so any modification, reordering, or deletion of a historical receipt breaks
// every signature from that point forward. Chains can be verified standalone
// or exported as a Merkle-rooted audit bundle.
package receipts

import (
	"context"
	"errors"
	"time"
)

// DefaultScope is used when callers do not specify a chain scope.
// A deployment with a single chain keeps everything here.
const DefaultScope = "global"

var (
	ErrReceiptNotFound = errors.New("receipts: not found")
	ErrSigningDisabled = errors.New("receipts: signing disabled (no HMAC secret configured)")
	ErrChainBroken     = errors.New("receipts: chain integrity broken")
	ErrChainHeadStale  = errors.New("receipts: chain head was advanced concurrently")
)

// PaymentPath identifies which subsystem issued the receipt.
type PaymentPath string

const (
	PathGateway    PaymentPath = "gateway"
	PathStream     PaymentPath = "stream"
	PathSessionKey PaymentPath = "session_key"
	PathEscrow     PaymentPath = "escrow"
)

// Receipt is a cryptographically signed proof that the platform processed a payment.
type Receipt struct {
	ID          string      `json:"id"`
	PaymentPath PaymentPath `json:"paymentPath"`
	Reference   string      `json:"reference"`           // e.g. session ID, stream ID, escrow ID
	From        string      `json:"from"`                // buyer address
	To          string      `json:"to"`                  // seller address
	Amount      string      `json:"amount"`              // USDC amount
	ServiceID   string      `json:"serviceId,omitempty"` // optional service identifier
	Status      string      `json:"status"`              // "confirmed" or "failed"
	Scope       string      `json:"scope"`               // chain scope (e.g. tenant ID, DefaultScope)
	ChainIndex  int64       `json:"chainIndex"`          // position in Scope chain, 0-based
	PrevHash    string      `json:"prevHash"`            // PayloadHash of preceding receipt ("" if first)
	PayloadHash string      `json:"payloadHash"`         // SHA-256 of canonical payload (includes Scope/ChainIndex/PrevHash)
	Signature   string      `json:"signature"`           // HMAC-SHA256 signature over the same payload
	IssuedAt    time.Time   `json:"issuedAt"`            // when the receipt was signed
	ExpiresAt   time.Time   `json:"expiresAt"`           // when the signature expires
	Metadata    string      `json:"metadata,omitempty"`  // optional extra context
	CreatedAt   time.Time   `json:"createdAt"`
}

// IssueRequest is the input for creating a receipt.
type IssueRequest struct {
	Path      PaymentPath
	Reference string
	From      string
	To        string
	Amount    string
	ServiceID string
	Status    string
	Metadata  string
	Scope     string // optional; falls back to DefaultScope
}

// ChainHead is the HEAD pointer of a chain scope.
// Stores the most recent receipt's PayloadHash and ChainIndex so the next
// Append can link without scanning the full history.
type ChainHead struct {
	Scope     string    `json:"scope"`
	HeadHash  string    `json:"headHash"`  // PayloadHash of the latest receipt ("" for empty chain)
	HeadIndex int64     `json:"headIndex"` // ChainIndex of the latest receipt (-1 for empty chain)
	UpdatedAt time.Time `json:"updatedAt"`
	ReceiptID string    `json:"receiptId,omitempty"` // ID of the latest receipt
}

// VerifyRequest is the input for verifying a receipt signature.
type VerifyRequest struct {
	ReceiptID string `json:"receiptId" binding:"required"`
}

// VerifyResponse is the result of receipt verification.
type VerifyResponse struct {
	Valid     bool   `json:"valid"`
	ReceiptID string `json:"receiptId"`
	Expired   bool   `json:"expired,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Store persists receipt data.
type Store interface {
	Create(ctx context.Context, receipt *Receipt) error
	Get(ctx context.Context, id string) (*Receipt, error)
	ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Receipt, error)
	ListByReference(ctx context.Context, reference string) ([]*Receipt, error)
}

// ChainStore extends Store with chain-aware operations.
// Implementations must provide atomic read-head + write-receipt + advance-head
// to prevent lost updates under concurrent IssueReceipt calls.
type ChainStore interface {
	Store

	// GetChainHead returns the current HEAD of the given scope.
	// An empty chain returns HeadHash="", HeadIndex=-1, not an error.
	GetChainHead(ctx context.Context, scope string) (*ChainHead, error)

	// AppendReceipt atomically:
	//   1. Reads the chain head for the receipt's Scope.
	//   2. Asserts the receipt's PrevHash matches the current HeadHash
	//      and its ChainIndex == HeadIndex+1. Returns ErrChainHeadStale otherwise.
	//   3. Inserts the receipt.
	//   4. Advances the head.
	AppendReceipt(ctx context.Context, r *Receipt) error

	// ListByChain returns receipts in a scope between two indices inclusive,
	// ordered by ChainIndex ascending. Pass -1 for upperIndex to mean "up to HEAD".
	ListByChain(ctx context.Context, scope string, lowerIndex, upperIndex int64) ([]*Receipt, error)

	// ListByChainTime returns receipts in a scope whose IssuedAt falls in [since, until],
	// ordered by ChainIndex ascending.
	ListByChainTime(ctx context.Context, scope string, since, until time.Time) ([]*Receipt, error)
}

// scopeOrDefault returns DefaultScope when s is empty.
// Used so legacy receipts (no Scope set) verify deterministically.
func scopeOrDefault(s string) string {
	if s == "" {
		return DefaultScope
	}
	return s
}

// receiptPayload is the canonical struct signed by HMAC.
// Field order must be deterministic (JSON marshalling of struct is by field order).
//
// The payload includes Scope, ChainIndex, and PrevHash so the signature
// commits to the receipt's position in the chain. Modifying any earlier
// receipt changes its PayloadHash, which means every subsequent receipt's
// PrevHash no longer matches and chain verification fails.
type receiptPayload struct {
	Amount     string `json:"amount"`
	ChainIndex int64  `json:"chainIndex"`
	From       string `json:"from"`
	Path       string `json:"path"`
	PrevHash   string `json:"prevHash"`
	Reference  string `json:"reference"`
	Scope      string `json:"scope"`
	ServiceID  string `json:"serviceId"`
	Status     string `json:"status"`
	To         string `json:"to"`
}
