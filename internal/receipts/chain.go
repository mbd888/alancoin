package receipts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ChainStatus classifies the outcome of a VerifyChain run.
type ChainStatus string

const (
	ChainIntact       ChainStatus = "intact"
	ChainSignatureBad ChainStatus = "signature_invalid"
	ChainLinkageBad   ChainStatus = "linkage_broken"
	ChainIndexGap     ChainStatus = "index_gap"
	ChainEmpty        ChainStatus = "empty"
	ChainHashMismatch ChainStatus = "payload_hash_mismatch"
)

// VerifyReport summarizes a chain verification walk.
// Count reflects receipts actually scanned (including the one that broke).
type VerifyReport struct {
	Scope        string      `json:"scope"`
	Status       ChainStatus `json:"status"`
	Count        int         `json:"count"`
	LastIndex    int64       `json:"lastIndex"` // highest ChainIndex verified intact
	LastHash     string      `json:"lastHash"`  // PayloadHash at LastIndex
	BreakAtIndex *int64      `json:"breakAtIndex,omitempty"`
	BreakReceipt string      `json:"breakReceipt,omitempty"`
	Message      string      `json:"message,omitempty"`
}

// VerifyChain walks the chain for the given scope between [lowerIndex, upperIndex]
// and reports the first integrity break. Passing -1 for upperIndex walks to HEAD.
//
// A chain is intact iff, for each receipt r_i:
//   - r_i.ChainIndex == i (no gaps, monotonic from lowerIndex)
//   - r_i.PrevHash == PayloadHash(r_{i-1})  (or "" when i == 0)
//   - r_i.PayloadHash == SHA256(canonical_payload(r_i))
//   - signer.Verify(canonical_payload(r_i), r_i.Signature) is true
func (s *Service) VerifyChain(ctx context.Context, scope string, lowerIndex, upperIndex int64) (*VerifyReport, error) {
	if s == nil || s.signer == nil {
		return &VerifyReport{Scope: scopeOrDefault(scope), Status: ChainEmpty, Message: "signing disabled"}, nil
	}
	chainStore, ok := s.store.(ChainStore)
	if !ok {
		return nil, fmt.Errorf("receipts: store does not support chain walks")
	}

	scope = scopeOrDefault(scope)
	receipts, err := chainStore.ListByChain(ctx, scope, lowerIndex, upperIndex)
	if err != nil {
		return nil, err
	}
	if len(receipts) == 0 {
		return &VerifyReport{Scope: scope, Status: ChainEmpty, LastIndex: -1}, nil
	}

	report := &VerifyReport{Scope: scope, Status: ChainIntact, LastIndex: -1}
	expectedIndex := lowerIndex
	var expectedPrev string

	if lowerIndex > 0 {
		// Starting mid-chain: seed expectedPrev from the receipt preceding lowerIndex.
		prev, err := chainStore.ListByChain(ctx, scope, lowerIndex-1, lowerIndex-1)
		if err != nil {
			return nil, err
		}
		if len(prev) == 1 {
			expectedPrev = prev[0].PayloadHash
		}
	}

	for _, r := range receipts {
		report.Count++

		if r.ChainIndex != expectedIndex {
			idx := r.ChainIndex
			report.Status = ChainIndexGap
			report.BreakAtIndex = &idx
			report.BreakReceipt = r.ID
			report.Message = fmt.Sprintf("expected chain index %d, got %d", expectedIndex, r.ChainIndex)
			return report, nil
		}

		if r.PrevHash != expectedPrev {
			idx := r.ChainIndex
			report.Status = ChainLinkageBad
			report.BreakAtIndex = &idx
			report.BreakReceipt = r.ID
			report.Message = fmt.Sprintf("receipt prevHash %q does not match previous payloadHash %q", r.PrevHash, expectedPrev)
			return report, nil
		}

		payload := payloadFromReceipt(r)
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		computed := sha256.Sum256(data)
		computedHex := hex.EncodeToString(computed[:])
		if computedHex != r.PayloadHash {
			idx := r.ChainIndex
			report.Status = ChainHashMismatch
			report.BreakAtIndex = &idx
			report.BreakReceipt = r.ID
			report.Message = fmt.Sprintf("stored payloadHash %q does not match computed %q", r.PayloadHash, computedHex)
			return report, nil
		}

		if !s.signer.Verify(payload, r.Signature) {
			idx := r.ChainIndex
			report.Status = ChainSignatureBad
			report.BreakAtIndex = &idx
			report.BreakReceipt = r.ID
			report.Message = "signature verification failed"
			return report, nil
		}

		report.LastIndex = r.ChainIndex
		report.LastHash = r.PayloadHash
		expectedPrev = r.PayloadHash
		expectedIndex++
	}

	return report, nil
}

// payloadFromReceipt reconstructs the canonical payload struct from a stored
// receipt. Must match the payload shape used by (*Service).buildReceipt —
// changes here require a chain-format version bump.
func payloadFromReceipt(r *Receipt) receiptPayload {
	return receiptPayload{
		Amount:     r.Amount,
		ChainIndex: r.ChainIndex,
		From:       r.From,
		Path:       string(r.PaymentPath),
		PrevHash:   r.PrevHash,
		Reference:  r.Reference,
		Scope:      scopeOrDefault(r.Scope),
		ServiceID:  r.ServiceID,
		Status:     r.Status,
		To:         r.To,
	}
}

// --- Merkle root ---

// MerkleRoot returns the Merkle root of the given receipts' PayloadHashes.
// Leaves are the raw 32-byte SHA-256 hashes; internal nodes are
// SHA-256(left || right). Odd layers duplicate the last leaf, matching the
// Bitcoin-style Merkle construction commonly accepted by auditors.
// Input order matters: callers must sort by ChainIndex ASC before calling.
// Returns "" for an empty slice.
func MerkleRoot(receipts []*Receipt) (string, error) {
	if len(receipts) == 0 {
		return "", nil
	}
	layer := make([][]byte, len(receipts))
	for i, r := range receipts {
		b, err := hex.DecodeString(r.PayloadHash)
		if err != nil {
			return "", fmt.Errorf("receipt %s: decode payload hash: %w", r.ID, err)
		}
		if len(b) != sha256.Size {
			return "", fmt.Errorf("receipt %s: payload hash wrong length %d", r.ID, len(b))
		}
		layer[i] = b
	}
	for len(layer) > 1 {
		if len(layer)%2 == 1 {
			dup := make([]byte, len(layer[len(layer)-1]))
			copy(dup, layer[len(layer)-1])
			layer = append(layer, dup)
		}
		next := make([][]byte, len(layer)/2)
		for i := 0; i < len(next); i++ {
			h := sha256.New()
			h.Write(layer[2*i])
			h.Write(layer[2*i+1])
			next[i] = h.Sum(nil)
		}
		layer = next
	}
	return hex.EncodeToString(layer[0]), nil
}

// --- Audit bundle ---

// BundleFormat is the canonical format tag a verifier checks before parsing.
const BundleFormat = "alancoin.receipts.bundle.v1"

// AuditBundle is a self-contained, signed export of a chain slice.
// Clients verify it by:
//  1. Re-computing MerkleRoot(Receipts) and comparing to Manifest.MerkleRoot.
//  2. Running VerifyChain semantics across the receipts (PrevHash linkage,
//     PayloadHash match, signature validity).
//  3. Verifying Manifest.Signature against the same HMAC secret.
type AuditBundle struct {
	Format   string         `json:"format"`
	Manifest BundleManifest `json:"manifest"`
	Receipts []*Receipt     `json:"receipts"`
}

// BundleManifest summarizes a bundle's scope, range, Merkle root, and
// signature. The signature covers the manifest's canonical JSON form
// (excluding the Signature field itself), committing to everything a
// regulator needs to reproduce the Merkle root independently.
type BundleManifest struct {
	Scope        string    `json:"scope"`
	Format       string    `json:"format"`
	Since        time.Time `json:"since"`
	Until        time.Time `json:"until"`
	LowerIndex   int64     `json:"lowerIndex"`
	UpperIndex   int64     `json:"upperIndex"`
	ReceiptCount int       `json:"receiptCount"`
	MerkleRoot   string    `json:"merkleRoot"` // hex, 64 chars
	FirstHash    string    `json:"firstHash"`  // PayloadHash of first receipt in range
	LastHash     string    `json:"lastHash"`   // PayloadHash of last receipt in range
	GeneratedAt  time.Time `json:"generatedAt"`
	Signature    string    `json:"signature"` // HMAC-SHA256 over manifest with Signature="" and Format=BundleFormat
}

// ExportBundle builds a signed audit bundle for the scope/time-range.
// It does NOT run VerifyChain — callers who need integrity guarantees
// before exporting should call VerifyChain first.
func (s *Service) ExportBundle(ctx context.Context, scope string, since, until time.Time) (*AuditBundle, error) {
	if s == nil {
		return nil, fmt.Errorf("receipts: nil service")
	}
	if s.signer == nil {
		return nil, ErrSigningDisabled
	}
	chainStore, ok := s.store.(ChainStore)
	if !ok {
		return nil, fmt.Errorf("receipts: store does not support chain export")
	}

	scope = scopeOrDefault(scope)
	receipts, err := chainStore.ListByChainTime(ctx, scope, since, until)
	if err != nil {
		return nil, fmt.Errorf("list receipts: %w", err)
	}

	// Deterministic ordering for reproducible Merkle roots.
	sort.Slice(receipts, func(i, j int) bool {
		return receipts[i].ChainIndex < receipts[j].ChainIndex
	})

	root, err := MerkleRoot(receipts)
	if err != nil {
		return nil, fmt.Errorf("merkle root: %w", err)
	}

	manifest := BundleManifest{
		Scope:        scope,
		Format:       BundleFormat,
		Since:        since.UTC(),
		Until:        until.UTC(),
		ReceiptCount: len(receipts),
		MerkleRoot:   root,
		GeneratedAt:  time.Now().UTC(),
	}
	if len(receipts) > 0 {
		manifest.LowerIndex = receipts[0].ChainIndex
		manifest.UpperIndex = receipts[len(receipts)-1].ChainIndex
		manifest.FirstHash = receipts[0].PayloadHash
		manifest.LastHash = receipts[len(receipts)-1].PayloadHash
	} else {
		manifest.LowerIndex = -1
		manifest.UpperIndex = -1
	}

	sig, err := s.signManifest(manifest)
	if err != nil {
		return nil, fmt.Errorf("sign manifest: %w", err)
	}
	manifest.Signature = sig

	return &AuditBundle{
		Format:   BundleFormat,
		Manifest: manifest,
		Receipts: receipts,
	}, nil
}

// VerifyBundle checks a bundle produced by ExportBundle.
// It re-computes the Merkle root, verifies the manifest signature, and
// replays VerifyChain semantics over the embedded receipts.
// Returns a VerifyReport so callers can surface the first break location.
func (s *Service) VerifyBundle(bundle *AuditBundle) (*VerifyReport, error) {
	if s == nil || s.signer == nil {
		return nil, ErrSigningDisabled
	}
	if bundle == nil {
		return nil, fmt.Errorf("receipts: nil bundle")
	}
	if bundle.Format != BundleFormat {
		return nil, fmt.Errorf("receipts: unsupported bundle format %q", bundle.Format)
	}

	// Manifest signature first — cheap and scopes the verification key.
	expectedSig, err := s.signManifest(bundle.Manifest)
	if err != nil {
		return nil, err
	}
	if expectedSig != bundle.Manifest.Signature {
		return nil, fmt.Errorf("receipts: manifest signature invalid")
	}

	root, err := MerkleRoot(bundle.Receipts)
	if err != nil {
		return nil, fmt.Errorf("recompute merkle root: %w", err)
	}
	if root != bundle.Manifest.MerkleRoot {
		return nil, fmt.Errorf("receipts: merkle root mismatch: manifest=%s computed=%s",
			bundle.Manifest.MerkleRoot, root)
	}

	// Replay chain semantics over the embedded slice without touching the store.
	report := &VerifyReport{Scope: bundle.Manifest.Scope, Status: ChainIntact, LastIndex: -1}
	if len(bundle.Receipts) == 0 {
		report.Status = ChainEmpty
		return report, nil
	}

	expectedIndex := bundle.Manifest.LowerIndex
	var expectedPrev string
	if expectedIndex > 0 {
		// The bundle is a mid-chain slice; we cannot verify the first receipt's
		// PrevHash against an ancestor we don't have. Trust the manifest but
		// verify linkage within the slice.
		expectedPrev = bundle.Receipts[0].PrevHash
	}

loop:
	for _, r := range bundle.Receipts {
		report.Count++

		if r.ChainIndex != expectedIndex {
			idx := r.ChainIndex
			report.Status = ChainIndexGap
			report.BreakAtIndex = &idx
			report.BreakReceipt = r.ID
			break loop
		}
		if r.PrevHash != expectedPrev {
			idx := r.ChainIndex
			report.Status = ChainLinkageBad
			report.BreakAtIndex = &idx
			report.BreakReceipt = r.ID
			break loop
		}
		payload := payloadFromReceipt(r)
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		computed := sha256.Sum256(data)
		if hex.EncodeToString(computed[:]) != r.PayloadHash {
			idx := r.ChainIndex
			report.Status = ChainHashMismatch
			report.BreakAtIndex = &idx
			report.BreakReceipt = r.ID
			break loop
		}
		if !s.signer.Verify(payload, r.Signature) {
			idx := r.ChainIndex
			report.Status = ChainSignatureBad
			report.BreakAtIndex = &idx
			report.BreakReceipt = r.ID
			break loop
		}

		report.LastIndex = r.ChainIndex
		report.LastHash = r.PayloadHash
		expectedPrev = r.PayloadHash
		expectedIndex++
	}

	if report.Status != ChainIntact && report.Status != ChainEmpty {
		return report, fmt.Errorf("receipts: bundle integrity broken (%s)", report.Status)
	}
	return report, nil
}

// signManifest returns the HMAC signature of the manifest with its Signature
// field cleared, so verification re-derives the same input bytes.
func (s *Service) signManifest(m BundleManifest) (string, error) {
	m.Signature = ""
	sig, _, _, err := s.signer.Sign(m)
	if err != nil {
		return "", err
	}
	return sig, nil
}
