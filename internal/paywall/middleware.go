// Package paywall implements HTTP 402 Payment Required middleware
// This is the core of the x402 protocol implementation
package paywall

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// -----------------------------------------------------------------------------
// Types
// -----------------------------------------------------------------------------

// PaymentRequirement describes what payment is needed
// This is returned in the 402 response body
type PaymentRequirement struct {
	Price       string `json:"price"`
	Currency    string `json:"currency"`
	Chain       string `json:"chain"`
	ChainID     int64  `json:"chainId"`
	Recipient   string `json:"recipient"`
	Contract    string `json:"contract"`
	Description string `json:"description,omitempty"`
	ValidFor    int64  `json:"validFor,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
}

// PaymentProof is sent by the client to prove payment was made
type PaymentProof struct {
	TxHash    string `json:"txHash"`
	From      string `json:"from"`
	Nonce     string `json:"nonce,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// -----------------------------------------------------------------------------
// Wallet Interface (dependency inversion)
// -----------------------------------------------------------------------------

// PaymentWallet is the interface required by the paywall
type PaymentWallet interface {
	Address() string
	VerifyPayment(ctx context.Context, from string, minAmount string, txHash string) (bool, error)
	WaitForConfirmationAny(ctx context.Context, txHash string, timeout time.Duration) (interface{}, error)
}

// -----------------------------------------------------------------------------
// Config
// -----------------------------------------------------------------------------

// Config for the paywall middleware
type Config struct {
	// Wallet for receiving payments (interface, not concrete type)
	Wallet PaymentWallet

	// Payment settings
	DefaultPrice string
	Chain        string
	ChainID      int64
	Contract     string

	// Verification settings
	RequireConfirmation bool
	ConfirmationTimeout time.Duration
	ValidFor            time.Duration

	// Hooks
	OnPaymentReceived func(proof *PaymentProof, route string)
	OnPaymentFailed   func(proof *PaymentProof, err error)
}

// -----------------------------------------------------------------------------
// Middleware
// -----------------------------------------------------------------------------

// Middleware creates a gin middleware that requires payment
func Middleware(cfg Config) gin.HandlerFunc {
	return MiddlewareWithPrice(cfg, cfg.DefaultPrice, "API access")
}

// MiddlewareWithPrice creates a middleware with a specific price and description
func MiddlewareWithPrice(cfg Config, price string, description string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check for payment proof header
		proofHeader := c.GetHeader("X-Payment-Proof")

		// Also check for x402 standard header
		if proofHeader == "" {
			proofHeader = c.GetHeader("X-402-Payment")
		}

		if proofHeader == "" {
			returnPaymentRequired(c, cfg, price, description)
			return
		}

		// Parse the payment proof
		var proof PaymentProof
		if err := json.Unmarshal([]byte(proofHeader), &proof); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_payment_proof",
				"message": "Could not parse payment proof JSON",
			})
			c.Abort()
			return
		}

		// Verify the payment
		ctx := c.Request.Context()
		verified, err := verifyPayment(ctx, cfg, &proof, price)
		if err != nil {
			if cfg.OnPaymentFailed != nil {
				cfg.OnPaymentFailed(&proof, err)
			}
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "payment_verification_failed",
				"message": "Payment verification failed",
			})
			c.Abort()
			return
		}

		if !verified {
			if cfg.OnPaymentFailed != nil {
				cfg.OnPaymentFailed(&proof, fmt.Errorf("payment amount insufficient"))
			}
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "payment_insufficient",
				"message": "Payment amount was less than required",
			})
			c.Abort()
			return
		}

		// Payment verified
		if cfg.OnPaymentReceived != nil {
			cfg.OnPaymentReceived(&proof, c.FullPath())
		}

		// Store proof in context
		c.Set("payment_proof", &proof)
		c.Set("payment_amount", price)

		c.Next()
	}
}

func returnPaymentRequired(c *gin.Context, cfg Config, price string, description string) {
	nonce, err := generateSecureNonce()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to generate secure nonce",
		})
		return
	}

	req := PaymentRequirement{
		Price:       price,
		Currency:    "USDC",
		Chain:       cfg.Chain,
		ChainID:     cfg.ChainID,
		Recipient:   cfg.Wallet.Address(),
		Contract:    cfg.Contract,
		Description: description,
		ValidFor:    int64(cfg.ValidFor.Seconds()),
		Nonce:       nonce,
	}

	// Set standard headers
	c.Header("X-Payment-Required", "true")
	c.Header("X-Payment-Currency", "USDC")
	c.Header("X-Payment-Amount", price)
	c.Header("X-Payment-Recipient", cfg.Wallet.Address())
	c.Header("X-Payment-Chain", cfg.Chain)

	c.JSON(http.StatusPaymentRequired, req)
	c.Abort()
}

func verifyPayment(ctx context.Context, cfg Config, proof *PaymentProof, requiredAmount string) (bool, error) {
	if proof.TxHash == "" {
		return false, fmt.Errorf("missing transaction hash")
	}
	if proof.From == "" {
		return false, fmt.Errorf("missing sender address")
	}

	// Normalize tx hash
	txHash := proof.TxHash
	if !strings.HasPrefix(txHash, "0x") {
		txHash = "0x" + txHash
	}

	// Wait for confirmation if required
	if cfg.RequireConfirmation {
		timeout := cfg.ConfirmationTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}

		_, err := cfg.Wallet.WaitForConfirmationAny(ctx, txHash, timeout)
		if err != nil {
			return false, fmt.Errorf("transaction not confirmed: %w", err)
		}
	}

	// Verify the payment on-chain
	verified, err := cfg.Wallet.VerifyPayment(ctx, proof.From, requiredAmount, txHash)
	if err != nil {
		return false, fmt.Errorf("verification failed: %w", err)
	}

	return verified, nil
}

// generateSecureNonce creates a cryptographically secure nonce
func generateSecureNonce() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// GetPaymentProof retrieves the payment proof from the gin context
func GetPaymentProof(c *gin.Context) *PaymentProof {
	if proof, exists := c.Get("payment_proof"); exists {
		return proof.(*PaymentProof)
	}
	return nil
}
