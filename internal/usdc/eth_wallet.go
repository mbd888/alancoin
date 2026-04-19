package usdc

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/crypto"
)

// EthWallet is a Wallet backed by a real secp256k1 private key.
// Replace with a KMS-backed signer before production: this struct holds
// the raw key in memory which is fine for dev but not for custody.
type EthWallet struct {
	mu      sync.Mutex
	priv    *ecdsa.PrivateKey
	address string
}

// NewEthWallet wraps the given key. Address is derived from the key's
// public half and lowercased to match the rest of the codebase's address
// normalization.
func NewEthWallet(priv *ecdsa.PrivateKey) (*EthWallet, error) {
	if priv == nil {
		return nil, errors.New("usdc: EthWallet requires a private key")
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	return &EthWallet{
		priv:    priv,
		address: strings.ToLower(addr.Hex()),
	}, nil
}

// NewEthWalletFromHex parses a 0x-prefixed or unprefixed hex-encoded
// private key. Use NewEthWallet directly when you already have a
// *ecdsa.PrivateKey (e.g. from a keystore).
func NewEthWalletFromHex(hexKey string) (*EthWallet, error) {
	s := strings.TrimPrefix(hexKey, "0x")
	priv, err := crypto.HexToECDSA(s)
	if err != nil {
		return nil, fmt.Errorf("usdc: parse private key: %w", err)
	}
	return NewEthWallet(priv)
}

// GenerateEthWallet returns a fresh EthWallet with a random key.
// Convenient for tests; do not use for anything that holds real funds.
func GenerateEthWallet() (*EthWallet, error) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("usdc: generate key: %w", err)
	}
	return NewEthWallet(priv)
}

// Address returns the 0x-prefixed lowercase address.
func (w *EthWallet) Address() string {
	return w.address
}

// Sign produces a 65-byte (r || s || v) signature over the 32-byte digest.
// v is 0 or 1 here — caller (EthClient) is responsible for normalizing
// to the chain's V convention via types.SignerHash / types.Transaction.
func (w *EthWallet) Sign(_ context.Context, digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("usdc: EthWallet.Sign requires a 32-byte digest, got %d", len(digest))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	sig, err := crypto.Sign(digest, w.priv)
	if err != nil {
		return nil, fmt.Errorf("usdc: sign: %w", err)
	}
	return sig, nil
}
