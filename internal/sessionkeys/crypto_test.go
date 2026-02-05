package sessionkeys

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestCreateTransactionMessage(t *testing.T) {
	msg := CreateTransactionMessage("0xRecipient", "1.50", 1, 1707234567)
	expected := "Alancoin|0xrecipient|1.50|1|1707234567"
	if msg != expected {
		t.Errorf("Expected %s, got %s", expected, msg)
	}

	// Should lowercase address
	msg = CreateTransactionMessage("0xABCDEF", "0.01", 99, 1234567890)
	if msg != "Alancoin|0xabcdef|0.01|99|1234567890" {
		t.Errorf("Expected lowercase address in message")
	}
}

func TestRecoverAddress(t *testing.T) {
	// Generate a test keypair
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	address := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()

	// Create and sign a message
	message := "Alancoin|0xrecipient|1.00|1|1707234567"
	messageHash := HashMessage(message)

	sig, err := crypto.Sign(messageHash, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign: %v", err)
	}

	// Ethereum signatures need v = 27 or 28
	sig[64] += 27

	// Recover address
	recovered, err := RecoverAddress(message, "0x"+bytesToHex(sig))
	if err != nil {
		t.Fatalf("RecoverAddress failed: %v", err)
	}

	if recovered != address[:42] && recovered != address[2:] {
		// Compare case-insensitively
		if !equalIgnoreCase(recovered, address) {
			t.Errorf("Expected %s, got %s", address, recovered)
		}
	}
}

func TestRecoverAddressInvalidSignature(t *testing.T) {
	// Invalid hex
	_, err := RecoverAddress("test", "not-hex")
	if err == nil {
		t.Error("Expected error for invalid hex")
	}

	// Wrong length
	_, err = RecoverAddress("test", "0xabcd")
	if err == nil {
		t.Error("Expected error for wrong length")
	}
}

func TestValidateSigned(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Generate a session keypair
	sessionKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Failed to generate session key: %v", err)
	}
	sessionAddr := crypto.PubkeyToAddress(sessionKey.PublicKey).Hex()

	// Create session key in manager
	req := &SessionKeyRequest{
		PublicKey:         sessionAddr,
		MaxPerTransaction: "10.00",
		MaxPerDay:         "100.00",
		ExpiresIn:         "1h",
		AllowAny:          true,
	}

	key, err := mgr.Create(ctx, "0xOwner123", req)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create a valid signed transaction
	now := time.Now().Unix()
	message := CreateTransactionMessage("0xRecipient", "1.00", 1, now)
	messageHash := HashMessage(message)

	sig, _ := crypto.Sign(messageHash, sessionKey)
	sig[64] += 27

	signedReq := &SignedTransactRequest{
		To:        "0xRecipient",
		Amount:    "1.00",
		Nonce:     1,
		Timestamp: now,
		Signature: "0x" + bytesToHex(sig),
	}

	// Should validate successfully
	err = mgr.ValidateSigned(ctx, key.ID, signedReq)
	if err != nil {
		t.Errorf("ValidateSigned failed for valid request: %v", err)
	}
}

func TestValidateSignedReplayProtection(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	sessionKey, _ := crypto.GenerateKey()
	sessionAddr := crypto.PubkeyToAddress(sessionKey.PublicKey).Hex()

	req := &SessionKeyRequest{
		PublicKey: sessionAddr,
		ExpiresIn: "1h",
		AllowAny:  true,
	}
	key, _ := mgr.Create(ctx, "0xOwner", req)

	now := time.Now().Unix()

	// First transaction with nonce 1
	msg1 := CreateTransactionMessage("0xRecipient", "1.00", 1, now)
	sig1, _ := crypto.Sign(HashMessage(msg1), sessionKey)
	sig1[64] += 27

	signedReq1 := &SignedTransactRequest{
		To:        "0xRecipient",
		Amount:    "1.00",
		Nonce:     1,
		Timestamp: now,
		Signature: "0x" + bytesToHex(sig1),
	}

	// First should succeed
	err := mgr.ValidateSigned(ctx, key.ID, signedReq1)
	if err != nil {
		t.Fatalf("First transaction should succeed: %v", err)
	}

	// Record usage to update nonce
	mgr.RecordUsage(ctx, key.ID, "1.00", 1)

	// Replay with same nonce should fail
	err = mgr.ValidateSigned(ctx, key.ID, signedReq1)
	if err == nil {
		t.Error("Replay should be rejected")
	}

	// New transaction with nonce 2 should succeed
	msg2 := CreateTransactionMessage("0xRecipient", "1.00", 2, now)
	sig2, _ := crypto.Sign(HashMessage(msg2), sessionKey)
	sig2[64] += 27

	signedReq2 := &SignedTransactRequest{
		To:        "0xRecipient",
		Amount:    "1.00",
		Nonce:     2,
		Timestamp: now,
		Signature: "0x" + bytesToHex(sig2),
	}

	err = mgr.ValidateSigned(ctx, key.ID, signedReq2)
	if err != nil {
		t.Errorf("Nonce 2 should succeed: %v", err)
	}
}

func TestValidateSignedTimestampExpiry(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	sessionKey, _ := crypto.GenerateKey()
	sessionAddr := crypto.PubkeyToAddress(sessionKey.PublicKey).Hex()

	req := &SessionKeyRequest{
		PublicKey: sessionAddr,
		ExpiresIn: "1h",
		AllowAny:  true,
	}
	key, _ := mgr.Create(ctx, "0xOwner", req)

	// Old timestamp (10 minutes ago)
	oldTimestamp := time.Now().Unix() - 600

	msg := CreateTransactionMessage("0xRecipient", "1.00", 1, oldTimestamp)
	sig, _ := crypto.Sign(HashMessage(msg), sessionKey)
	sig[64] += 27

	signedReq := &SignedTransactRequest{
		To:        "0xRecipient",
		Amount:    "1.00",
		Nonce:     1,
		Timestamp: oldTimestamp,
		Signature: "0x" + bytesToHex(sig),
	}

	err := mgr.ValidateSigned(ctx, key.ID, signedReq)
	if err == nil {
		t.Error("Old timestamp should be rejected")
	}
}

func TestValidateSignedWrongSigner(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	// Session key registered
	sessionKey, _ := crypto.GenerateKey()
	sessionAddr := crypto.PubkeyToAddress(sessionKey.PublicKey).Hex()

	// Different key used to sign
	wrongKey, _ := crypto.GenerateKey()

	req := &SessionKeyRequest{
		PublicKey: sessionAddr,
		ExpiresIn: "1h",
		AllowAny:  true,
	}
	key, _ := mgr.Create(ctx, "0xOwner", req)

	now := time.Now().Unix()
	msg := CreateTransactionMessage("0xRecipient", "1.00", 1, now)

	// Sign with WRONG key
	sig, _ := crypto.Sign(HashMessage(msg), wrongKey)
	sig[64] += 27

	signedReq := &SignedTransactRequest{
		To:        "0xRecipient",
		Amount:    "1.00",
		Nonce:     1,
		Timestamp: now,
		Signature: "0x" + bytesToHex(sig),
	}

	err := mgr.ValidateSigned(ctx, key.ID, signedReq)
	if err == nil {
		t.Error("Wrong signer should be rejected")
	}
}

func TestValidateSignedPermissions(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	sessionKey, _ := crypto.GenerateKey()
	sessionAddr := crypto.PubkeyToAddress(sessionKey.PublicKey).Hex()

	// Limited permissions
	req := &SessionKeyRequest{
		PublicKey:         sessionAddr,
		MaxPerTransaction: "1.00", // Max $1
		ExpiresIn:         "1h",
		AllowAny:          true,
	}
	key, _ := mgr.Create(ctx, "0xOwner", req)

	now := time.Now().Unix()

	// Try to spend more than limit
	msg := CreateTransactionMessage("0xRecipient", "5.00", 1, now)
	sig, _ := crypto.Sign(HashMessage(msg), sessionKey)
	sig[64] += 27

	signedReq := &SignedTransactRequest{
		To:        "0xRecipient",
		Amount:    "5.00", // Over limit
		Nonce:     1,
		Timestamp: now,
		Signature: "0x" + bytesToHex(sig),
	}

	err := mgr.ValidateSigned(ctx, key.ID, signedReq)
	if err != ErrExceedsPerTx {
		t.Errorf("Expected ErrExceedsPerTx, got: %v", err)
	}
}

// Helper functions

func bytesToHex(b []byte) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, len(b)*2)
	for i, v := range b {
		result[i*2] = hexChars[v>>4]
		result[i*2+1] = hexChars[v&0x0f]
	}
	return string(result)
}

func equalIgnoreCase(a, b string) bool {
	if len(a) != len(b) {
		// Try with/without 0x prefix
		if len(a) == len(b)+2 {
			a = a[2:]
		} else if len(b) == len(a)+2 {
			b = b[2:]
		}
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return len(a) == len(b)
}
