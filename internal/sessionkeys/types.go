// Package sessionkeys implements bounded autonomy for AI agents.
//
// Session keys are ECDSA keypairs with bounded permissions:
// - Agent generates keypair, registers public key with permissions
// - To transact, agent signs request with session private key
// - Server verifies signature + validates permissions
// - Cryptographic proof of session key possession
//
// This enables: "My agent can spend up to $10/day on translation services,
// proves it controls the session key by signing, and I can revoke instantly."
package sessionkeys

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Permission defines what a session key is allowed to do
type Permission struct {
	// Spending limits (in USDC, as string for precision)
	MaxPerTransaction string `json:"maxPerTransaction,omitempty"` // e.g., "1.00"
	MaxPerDay         string `json:"maxPerDay,omitempty"`         // e.g., "10.00"
	MaxTotal          string `json:"maxTotal,omitempty"`          // e.g., "100.00"

	// Time bounds
	ValidAfter time.Time `json:"validAfter,omitempty"` // Not valid before this time
	ExpiresAt  time.Time `json:"expiresAt"`            // Required: when the key expires

	// Recipient restrictions (at least one must be set)
	AllowedRecipients    []string `json:"allowedRecipients,omitempty"`    // Specific addresses
	AllowedServiceTypes  []string `json:"allowedServiceTypes,omitempty"`  // e.g., ["translation", "inference"]
	AllowedServiceAgents []string `json:"allowedServiceAgents,omitempty"` // Agents offering services
	AllowAny             bool     `json:"allowAny,omitempty"`             // If true, no recipient restrictions

	// Metadata
	Label string `json:"label,omitempty"` // Human-readable label, e.g., "Translation budget Q1"
}

// SessionKey represents an active session key with its permissions
type SessionKey struct {
	ID        string     `json:"id"`        // Unique identifier
	OwnerAddr string     `json:"ownerAddr"` // The agent/wallet that owns this key
	PublicKey string     `json:"publicKey"` // The session key's Ethereum address (derived from ECDSA pubkey)
	CreatedAt time.Time  `json:"createdAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"` // If set, key is revoked

	// The permissions granted to this key
	Permission Permission `json:"permission"`

	// Usage tracking
	Usage SessionKeyUsage `json:"usage"`
}

// SessionKeyUsage tracks how much the key has been used
type SessionKeyUsage struct {
	TransactionCount int       `json:"transactionCount"`
	TotalSpent       string    `json:"totalSpent"` // Total USDC spent
	SpentToday       string    `json:"spentToday"` // USDC spent today
	LastUsed         time.Time `json:"lastUsed,omitempty"`
	LastResetDay     string    `json:"lastResetDay,omitempty"` // Date of last daily reset (YYYY-MM-DD)
	LastNonce        uint64    `json:"lastNonce"`              // Last used nonce (replay protection)
}

// SessionKeyRequest is the payload for creating a new session key
type SessionKeyRequest struct {
	// The session key's public key (Ethereum address)
	// Client generates ECDSA keypair, sends the address here
	PublicKey string `json:"publicKey" binding:"required"`

	// Permission configuration
	MaxPerTransaction   string   `json:"maxPerTransaction,omitempty"`
	MaxPerDay           string   `json:"maxPerDay,omitempty"`
	MaxTotal            string   `json:"maxTotal,omitempty"`
	ExpiresIn           string   `json:"expiresIn,omitempty"` // Duration string, e.g., "24h", "7d"
	ExpiresAt           string   `json:"expiresAt,omitempty"` // Or exact timestamp
	AllowedRecipients   []string `json:"allowedRecipients,omitempty"`
	AllowedServiceTypes []string `json:"allowedServiceTypes,omitempty"`
	AllowAny            bool     `json:"allowAny,omitempty"`
	Label               string   `json:"label,omitempty"`
}

// SignedTransactRequest is a cryptographically signed transaction request
type SignedTransactRequest struct {
	// Transaction details
	To        string `json:"to" binding:"required"`     // Recipient address
	Amount    string `json:"amount" binding:"required"` // USDC amount
	ServiceID string `json:"serviceId,omitempty"`       // Optional: service being paid for

	// Cryptographic proof
	Nonce     uint64 `json:"nonce" binding:"required"`     // Unique per transaction (replay protection)
	Timestamp int64  `json:"timestamp" binding:"required"` // Unix timestamp (freshness)
	Signature string `json:"signature" binding:"required"` // Hex-encoded ECDSA signature

	// Note: SessionKeyID comes from URL parameter
}

// TransactRequest is a request to make a transaction using a session key
// DEPRECATED: Use SignedTransactRequest for cryptographic verification
type TransactRequest struct {
	SessionKeyID string `json:"sessionKeyId" binding:"required"`
	To           string `json:"to" binding:"required"`     // Recipient address
	Amount       string `json:"amount" binding:"required"` // USDC amount
	ServiceID    string `json:"serviceId,omitempty"`       // Optional: service being paid for
	Memo         string `json:"memo,omitempty"`            // Optional: transaction memo
}

// TransactResponse is the response from a session key transaction
type TransactResponse struct {
	TxHash       string    `json:"txHash"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	Amount       string    `json:"amount"`
	SessionKeyID string    `json:"sessionKeyId"`
	Timestamp    time.Time `json:"timestamp"`
}

// ValidationError represents a specific validation failure
type ValidationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return e.Message
}

// Common validation errors
var (
	ErrKeyNotFound           = &ValidationError{Code: "key_not_found", Message: "Session key not found"}
	ErrKeyRevoked            = &ValidationError{Code: "key_revoked", Message: "Session key has been revoked"}
	ErrKeyExpired            = &ValidationError{Code: "key_expired", Message: "Session key has expired"}
	ErrKeyNotYetValid        = &ValidationError{Code: "key_not_yet_valid", Message: "Session key is not yet valid"}
	ErrExceedsPerTx          = &ValidationError{Code: "exceeds_per_tx", Message: "Amount exceeds per-transaction limit"}
	ErrExceedsDaily          = &ValidationError{Code: "exceeds_daily", Message: "Amount exceeds daily spending limit"}
	ErrExceedsTotal          = &ValidationError{Code: "exceeds_total", Message: "Amount exceeds total spending limit"}
	ErrRecipientNotAllowed   = &ValidationError{Code: "recipient_not_allowed", Message: "Recipient is not in allowed list"}
	ErrServiceTypeNotAllowed = &ValidationError{Code: "service_type_not_allowed", Message: "Service type is not allowed"}
	ErrInvalidSignature      = &ValidationError{Code: "invalid_signature", Message: "Invalid or malformed signature"}
	ErrSignatureMismatch     = &ValidationError{Code: "signature_mismatch", Message: "Signature does not match session key"}
	ErrNonceReused           = &ValidationError{Code: "nonce_reused", Message: "Nonce has already been used"}
	ErrSignatureExpired      = &ValidationError{Code: "signature_expired", Message: "Signature timestamp is too old"}
	ErrInvalidPublicKey      = &ValidationError{Code: "invalid_public_key", Message: "Invalid public key format"}
)

// GenerateID creates a random session key ID
func GenerateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "sk_" + hex.EncodeToString(b)
}

// IsActive returns true if the session key is currently valid
func (sk *SessionKey) IsActive() bool {
	now := time.Now()

	// Check if revoked
	if sk.RevokedAt != nil {
		return false
	}

	// Check expiration
	if now.After(sk.Permission.ExpiresAt) {
		return false
	}

	// Check valid after
	if !sk.Permission.ValidAfter.IsZero() && now.Before(sk.Permission.ValidAfter) {
		return false
	}

	return true
}
