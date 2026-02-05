package sessionkeys

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

// CreateTransactionMessage creates the message that must be signed for a transaction
// Format: "Alancoin|{to}|{amount}|{nonce}|{timestamp}"
func CreateTransactionMessage(to string, amount string, nonce uint64, timestamp int64) string {
	return fmt.Sprintf("Alancoin|%s|%s|%d|%d",
		strings.ToLower(to),
		amount,
		nonce,
		timestamp,
	)
}

// HashMessage creates an Ethereum signed message hash
// This prefixes the message with "\x19Ethereum Signed Message:\n{len}" as per EIP-191
func HashMessage(message string) []byte {
	// Ethereum signed message prefix
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	return crypto.Keccak256([]byte(prefix + message))
}

// RecoverAddress recovers the signer's address from a message and signature
// signature should be hex-encoded, 65 bytes (r[32] + s[32] + v[1])
func RecoverAddress(message string, signatureHex string) (string, error) {
	// Remove 0x prefix if present
	sigHex := strings.TrimPrefix(signatureHex, "0x")

	// Decode signature
	signature, err := hex.DecodeString(sigHex)
	if err != nil {
		return "", fmt.Errorf("invalid signature hex: %w", err)
	}

	if len(signature) != 65 {
		return "", fmt.Errorf("signature must be 65 bytes, got %d", len(signature))
	}

	// Ethereum signatures have v = 27 or 28, but Ecrecover expects 0 or 1
	if signature[64] >= 27 {
		signature[64] -= 27
	}

	// Hash the message with Ethereum prefix
	messageHash := HashMessage(message)

	// Recover public key
	pubKeyBytes, err := crypto.Ecrecover(messageHash, signature)
	if err != nil {
		return "", fmt.Errorf("failed to recover public key: %w", err)
	}

	// Convert to address
	pubKey, err := crypto.UnmarshalPubkey(pubKeyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal public key: %w", err)
	}

	address := crypto.PubkeyToAddress(*pubKey)
	return strings.ToLower(address.Hex()), nil
}

// VerifySignature verifies that a signature was created by the expected address
func VerifySignature(message string, signatureHex string, expectedAddress string) error {
	recoveredAddr, err := RecoverAddress(message, signatureHex)
	if err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}

	if !strings.EqualFold(recoveredAddr, expectedAddress) {
		return fmt.Errorf("signature mismatch: expected %s, got %s", expectedAddress, recoveredAddr)
	}

	return nil
}
