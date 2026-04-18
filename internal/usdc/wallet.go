package usdc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
)

// Wallet abstracts transaction signing.
// The production implementation holds an ECDSA key (in-memory for dev,
// KMS/HSM for prod). The mock implementation in mock_client.go returns
// deterministic fake signatures so tests can assert bytes without keys.
//
// Implementations must be safe for concurrent use.
type Wallet interface {
	// Address is the 0x-prefixed 20-byte address derived from the wallet's key.
	Address() string

	// Sign produces a signature over the provided digest.
	// The digest is the keccak256 of the RLP-encoded tx minus the signature
	// fields (caller's responsibility to construct correctly).
	// Implementations MUST return a fixed-length deterministic result so
	// that replaying the same digest with the same wallet produces the
	// same signature bytes.
	Sign(ctx context.Context, digest []byte) ([]byte, error)
}

// StubWallet is a deterministic in-memory Wallet for tests and local dev.
// It does NOT produce valid ECDSA signatures; it returns SHA-256 over the
// digest concatenated with the wallet's secret, padded to 65 bytes.
// Real deployments must replace it with a KMS-backed signer before
// touching a real RPC endpoint.
type StubWallet struct {
	address string
	secret  []byte
	mu      sync.Mutex // guards multi-step signing flows
}

// NewStubWallet returns a StubWallet bound to the given 0x-prefixed address.
// The secret is an opaque byte string mixed into signatures so tests can
// distinguish different stub wallets without caring about valid ECDSA.
func NewStubWallet(address, secret string) (*StubWallet, error) {
	if !isHexAddress(address) {
		return nil, errors.New("usdc: StubWallet requires 0x-prefixed 20-byte address")
	}
	if secret == "" {
		return nil, errors.New("usdc: StubWallet requires a non-empty secret")
	}
	return &StubWallet{
		address: strings.ToLower(address),
		secret:  []byte(secret),
	}, nil
}

func (w *StubWallet) Address() string {
	return w.address
}

func (w *StubWallet) Sign(_ context.Context, digest []byte) ([]byte, error) {
	if len(digest) == 0 {
		return nil, errors.New("usdc: empty digest")
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	// SHA-256(digest || secret). Pad/truncate to 65 bytes (r,s,v shape).
	h := sha256.Sum256(append(append([]byte{}, digest...), w.secret...))
	sig := make([]byte, 65)
	copy(sig, h[:])
	return sig, nil
}

// HexDigest is a tiny helper for tests that want to log digests.
func HexDigest(b []byte) string {
	return "0x" + hex.EncodeToString(b)
}

// NonceManager serializes nonce issuance per wallet address. Each SendTransfer
// call must hold the wallet's nonce through submission so two goroutines
// cannot collide on the same number.
//
// Implementations must be safe for concurrent use.
type NonceManager interface {
	// Next returns the next nonce to use for address. The caller is
	// expected to call Release(address, nonce) after the tx has been
	// submitted or permanently failed.
	Next(ctx context.Context, address string, onchain uint64) (uint64, error)

	// Release informs the manager that the given nonce is no longer in flight.
	// Callers pass success=false on non-retryable send failures so the
	// manager can reuse the nonce without waiting for the chain to advance.
	Release(address string, nonce uint64, success bool)
}

// InMemoryNonceManager is a per-address monotonic nonce issuer.
// It trusts the on-chain pending nonce as a floor; each call returns
// max(inflight_high_water+1, onchain). Release only matters when a send
// fails before broadcast and the caller wants to reuse the slot.
type InMemoryNonceManager struct {
	mu    sync.Mutex
	state map[string]*nonceState
}

type nonceState struct {
	highWater uint64 // last issued nonce + 1 (i.e. next to issue if no onchain advance)
	inFlight  map[uint64]struct{}
}

// NewInMemoryNonceManager returns an empty manager.
func NewInMemoryNonceManager() *InMemoryNonceManager {
	return &InMemoryNonceManager{state: make(map[string]*nonceState)}
}

func (m *InMemoryNonceManager) Next(_ context.Context, address string, onchain uint64) (uint64, error) {
	addr := strings.ToLower(address)
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.state[addr]
	if !ok {
		st = &nonceState{highWater: onchain, inFlight: make(map[uint64]struct{})}
		m.state[addr] = st
	}
	if onchain > st.highWater {
		st.highWater = onchain
	}
	n := st.highWater
	st.highWater = n + 1
	st.inFlight[n] = struct{}{}
	return n, nil
}

func (m *InMemoryNonceManager) Release(address string, nonce uint64, success bool) {
	addr := strings.ToLower(address)
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.state[addr]
	if !ok {
		return
	}
	delete(st.inFlight, nonce)
	// On failure with no successor in flight, roll highWater back so the
	// nonce gets reused on the next Next(). If there ARE later inflights,
	// we cannot safely rewind — the sender has to wait for the onchain
	// nonce to catch up.
	if !success && st.highWater == nonce+1 && len(st.inFlight) == 0 {
		st.highWater = nonce
	}
}

// InFlightCount returns the number of outstanding nonces for an address.
// Exposed for tests and ops dashboards.
func (m *InMemoryNonceManager) InFlightCount(address string) int {
	addr := strings.ToLower(address)
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.state[addr]
	if !ok {
		return 0
	}
	return len(st.inFlight)
}
