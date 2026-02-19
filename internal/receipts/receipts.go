// Package receipts provides cryptographic receipt signing for all payment paths.
//
// Every payment through the platform (gateway, streams, escrow, session keys)
// produces a signed receipt that buyers and sellers can independently verify.
package receipts

import (
	"context"
	"errors"
	"time"
)

var (
	ErrReceiptNotFound = errors.New("receipts: not found")
	ErrSigningDisabled = errors.New("receipts: signing disabled (no HMAC secret configured)")
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
	PayloadHash string      `json:"payloadHash"`         // SHA-256 of canonical payload
	Signature   string      `json:"signature"`           // HMAC-SHA256 signature
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

// receiptPayload is the canonical struct signed by HMAC.
// Field order must be deterministic (JSON marshalling of struct is by field order).
type receiptPayload struct {
	Amount    string `json:"amount"`
	From      string `json:"from"`
	Path      string `json:"path"`
	Reference string `json:"reference"`
	ServiceID string `json:"serviceId"`
	Status    string `json:"status"`
	To        string `json:"to"`
}
