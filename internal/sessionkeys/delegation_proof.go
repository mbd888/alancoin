package sessionkeys

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// Caveat encodes a single permission restriction in the delegation chain.
// Each caveat can only narrow permissions — never widen. This mirrors the
// macaroon attenuation model: the holder of a delegated key can prove its
// full permission lineage without database lookups.
type Caveat struct {
	// Budget constraints (USDC strings for precision)
	MaxTotal          string `json:"maxTotal,omitempty"`
	MaxPerTransaction string `json:"maxPerTx,omitempty"`
	MaxPerDay         string `json:"maxPerDay,omitempty"`

	// Time bound
	ExpiresAt time.Time `json:"expiresAt"`

	// Recipient / scope restrictions
	AllowedRecipients   []string `json:"recipients,omitempty"`
	AllowedServiceTypes []string `json:"serviceTypes,omitempty"`
	Scopes              []string `json:"scopes,omitempty"`

	// Identity binding
	PublicKey string `json:"publicKey"` // Ethereum address of the key holder
	KeyID     string `json:"keyId"`     // Session key ID
	Depth     int    `json:"depth"`     // Position in delegation chain

	// Issuer binding
	IssuedAt time.Time `json:"issuedAt"`
	IssuerID string    `json:"issuerId"` // Parent key ID (empty for root)
}

// DelegationProof is a macaroon-inspired cryptographic proof of a delegation chain.
// It enables O(1) verification of the entire ancestor chain without database walks.
//
// The proof works as follows:
//  1. Root key creation: generate random rootSecret, compute
//     tag₀ = HMAC-SHA256(rootSecret, canonicalize(caveat₀))
//  2. Each delegation: compute
//     tagₙ = HMAC-SHA256(tagₙ₋₁, canonicalize(caveatₙ))
//  3. Verification: given rootSecret and the caveat chain, recompute the
//     chain and compare the final tag. If it matches, the entire delegation
//     chain is authentic and every caveat was authorized by its parent.
//
// Monotonic attenuation is enforced: each caveat must be a strict subset
// of its predecessor's permissions.
type DelegationProof struct {
	// Caveats is the ordered list of permission restrictions,
	// from root (index 0) to leaf (last element).
	Caveats []Caveat `json:"caveats"`

	// Tag is the final HMAC in the chain. Verifiers recompute the chain
	// from the root secret and compare.
	Tag string `json:"tag"` // hex-encoded HMAC-SHA256

	// RootKeyID identifies which root key's secret to look up for verification.
	RootKeyID string `json:"rootKeyId"`
}

// RootSecretSize is the byte length of randomly generated root secrets.
const RootSecretSize = 32

// GenerateRootSecret creates a cryptographically random root secret.
func GenerateRootSecret() ([]byte, error) {
	secret := make([]byte, RootSecretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate root secret: %w", err)
	}
	return secret, nil
}

// NewRootProof creates a DelegationProof for a root session key.
func NewRootProof(rootSecret []byte, key *SessionKey) *DelegationProof {
	caveat := caveatFromKey(key, "")

	tag := computeHMAC(rootSecret, caveat)

	return &DelegationProof{
		Caveats:   []Caveat{caveat},
		Tag:       hex.EncodeToString(tag),
		RootKeyID: key.ID,
	}
}

// ExtendProof creates a new DelegationProof for a child key by extending
// the parent's proof with a new caveat. The child caveat must be a strict
// subset of the parent's last caveat (monotonic attenuation).
func ExtendProof(parentProof *DelegationProof, childKey *SessionKey) (*DelegationProof, error) {
	if len(parentProof.Caveats) == 0 {
		return nil, fmt.Errorf("parent proof has no caveats")
	}

	parentCaveat := parentProof.Caveats[len(parentProof.Caveats)-1]
	childCaveat := caveatFromKey(childKey, parentCaveat.KeyID)

	// Enforce monotonic attenuation
	if err := validateAttenuation(parentCaveat, childCaveat); err != nil {
		return nil, fmt.Errorf("attenuation violation: %w", err)
	}

	// Chain: new tag = HMAC(parentTag, childCaveat)
	parentTag, err := hex.DecodeString(parentProof.Tag)
	if err != nil {
		return nil, fmt.Errorf("invalid parent tag: %w", err)
	}
	childTag := computeHMAC(parentTag, childCaveat)

	// Build new proof with extended caveat chain
	caveats := make([]Caveat, len(parentProof.Caveats)+1)
	copy(caveats, parentProof.Caveats)
	caveats[len(caveats)-1] = childCaveat

	return &DelegationProof{
		Caveats:   caveats,
		Tag:       hex.EncodeToString(childTag),
		RootKeyID: parentProof.RootKeyID,
	}, nil
}

// VerifyProof recomputes the HMAC chain from the root secret and verifies
// that the final tag matches. Returns nil if the proof is valid.
func VerifyProof(rootSecret []byte, proof *DelegationProof) error {
	if len(proof.Caveats) == 0 {
		return fmt.Errorf("empty caveat chain")
	}

	// Recompute chain: tag₀ = HMAC(rootSecret, caveat₀), tagₙ = HMAC(tagₙ₋₁, caveatₙ)
	currentTag := computeHMAC(rootSecret, proof.Caveats[0])
	for i := 1; i < len(proof.Caveats); i++ {
		// Verify monotonic attenuation at each step
		if err := validateAttenuation(proof.Caveats[i-1], proof.Caveats[i]); err != nil {
			return fmt.Errorf("caveat %d attenuation violation: %w", i, err)
		}
		currentTag = computeHMAC(currentTag, proof.Caveats[i])
	}

	// Compare computed tag with provided tag
	providedTag, err := hex.DecodeString(proof.Tag)
	if err != nil {
		return fmt.Errorf("invalid proof tag: %w", err)
	}

	if !hmac.Equal(currentTag, providedTag) {
		return fmt.Errorf("proof verification failed: tag mismatch")
	}

	// Verify time bounds
	now := time.Now()
	leafCaveat := proof.Caveats[len(proof.Caveats)-1]
	if now.After(leafCaveat.ExpiresAt) {
		return fmt.Errorf("delegation proof expired")
	}

	return nil
}

// VerifyBudget checks that the leaf caveat's budget permits the given amount.
func VerifyBudget(proof *DelegationProof, amount string, totalSpent string) error {
	if len(proof.Caveats) == 0 {
		return fmt.Errorf("empty caveat chain")
	}

	leaf := proof.Caveats[len(proof.Caveats)-1]

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		return fmt.Errorf("invalid amount")
	}

	// Check per-transaction limit
	if leaf.MaxPerTransaction != "" {
		maxPerTx, ok := usdc.Parse(leaf.MaxPerTransaction)
		if ok && amountBig.Cmp(maxPerTx) > 0 {
			return ErrExceedsPerTx
		}
	}

	// Check total limit
	if leaf.MaxTotal != "" {
		maxTotal, ok := usdc.Parse(leaf.MaxTotal)
		if ok {
			spent, _ := usdc.Parse(totalSpent)
			newTotal := new(big.Int).Add(spent, amountBig)
			if newTotal.Cmp(maxTotal) > 0 {
				return ErrExceedsTotal
			}
		}
	}

	return nil
}

// computeHMAC computes HMAC-SHA256(key, canonicalize(caveat)).
func computeHMAC(key []byte, caveat Caveat) []byte {
	data := canonicalize(caveat)
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// canonicalize produces a deterministic JSON encoding of a caveat.
// Fields are sorted and formatted consistently to ensure the same
// caveat always produces the same bytes.
func canonicalize(c Caveat) []byte {
	// Sort slice fields for deterministic output
	recipients := make([]string, len(c.AllowedRecipients))
	copy(recipients, c.AllowedRecipients)
	sort.Strings(recipients)

	serviceTypes := make([]string, len(c.AllowedServiceTypes))
	copy(serviceTypes, c.AllowedServiceTypes)
	sort.Strings(serviceTypes)

	scopes := make([]string, len(c.Scopes))
	copy(scopes, c.Scopes)
	sort.Strings(scopes)

	canonical := struct {
		MaxTotal   string   `json:"b"`
		MaxPerTx   string   `json:"bt"`
		MaxPerDay  string   `json:"bd"`
		ExpiresAt  int64    `json:"e"`
		Recipients []string `json:"r"`
		Services   []string `json:"s"`
		Scopes     []string `json:"sc"`
		PublicKey  string   `json:"pk"`
		KeyID      string   `json:"k"`
		Depth      int      `json:"d"`
		IssuedAt   int64    `json:"ia"`
		IssuerID   string   `json:"i"`
	}{
		MaxTotal:   c.MaxTotal,
		MaxPerTx:   c.MaxPerTransaction,
		MaxPerDay:  c.MaxPerDay,
		ExpiresAt:  c.ExpiresAt.Unix(),
		Recipients: recipients,
		Services:   serviceTypes,
		Scopes:     scopes,
		PublicKey:  c.PublicKey,
		KeyID:      c.KeyID,
		Depth:      c.Depth,
		IssuedAt:   c.IssuedAt.Unix(),
		IssuerID:   c.IssuerID,
	}

	data, _ := json.Marshal(canonical)
	return data
}

// caveatFromKey constructs a Caveat from a SessionKey.
func caveatFromKey(key *SessionKey, issuerID string) Caveat {
	return Caveat{
		MaxTotal:            key.Permission.MaxTotal,
		MaxPerTransaction:   key.Permission.MaxPerTransaction,
		MaxPerDay:           key.Permission.MaxPerDay,
		ExpiresAt:           key.Permission.ExpiresAt,
		AllowedRecipients:   key.Permission.AllowedRecipients,
		AllowedServiceTypes: key.Permission.AllowedServiceTypes,
		Scopes:              key.Permission.Scopes,
		PublicKey:           key.PublicKey,
		KeyID:               key.ID,
		Depth:               key.Depth,
		IssuedAt:            key.CreatedAt,
		IssuerID:            issuerID,
	}
}

// validateAttenuation verifies that the child caveat is a strict subset of
// the parent caveat (monotonic restriction). This is the core security
// invariant of the delegation chain.
func validateAttenuation(parent, child Caveat) error {
	// Budget: child must be <= parent (if parent has a limit)
	if parent.MaxTotal != "" {
		if child.MaxTotal == "" {
			return fmt.Errorf("child must set maxTotal when parent has one")
		}
		parentBig, _ := usdc.Parse(parent.MaxTotal)
		childBig, _ := usdc.Parse(child.MaxTotal)
		if childBig.Cmp(parentBig) > 0 {
			return fmt.Errorf("child maxTotal %s exceeds parent %s", child.MaxTotal, parent.MaxTotal)
		}
	}

	if parent.MaxPerTransaction != "" {
		if child.MaxPerTransaction == "" {
			return fmt.Errorf("child must set maxPerTransaction when parent has one")
		}
		parentBig, _ := usdc.Parse(parent.MaxPerTransaction)
		childBig, _ := usdc.Parse(child.MaxPerTransaction)
		if childBig.Cmp(parentBig) > 0 {
			return fmt.Errorf("child maxPerTx %s exceeds parent %s", child.MaxPerTransaction, parent.MaxPerTransaction)
		}
	}

	// Expiry: child must expire no later than parent
	if child.ExpiresAt.After(parent.ExpiresAt) {
		return fmt.Errorf("child expiry %v exceeds parent %v", child.ExpiresAt, parent.ExpiresAt)
	}

	// Scopes: child must be subset (and must inherit if parent restricts)
	if len(parent.Scopes) > 0 && len(child.Scopes) == 0 {
		return fmt.Errorf("child must set scopes when parent restricts them")
	}
	if len(parent.Scopes) > 0 && len(child.Scopes) > 0 {
		parentSet := make(map[string]bool, len(parent.Scopes))
		for _, s := range parent.Scopes {
			parentSet[s] = true
		}
		for _, s := range child.Scopes {
			if !parentSet[s] {
				return fmt.Errorf("child scope %q not in parent scopes", s)
			}
		}
	}

	// Recipients: child must be subset (and must inherit if parent restricts)
	if len(parent.AllowedRecipients) > 0 && len(child.AllowedRecipients) == 0 {
		return fmt.Errorf("child must set allowedRecipients when parent restricts them")
	}
	if len(parent.AllowedRecipients) > 0 && len(child.AllowedRecipients) > 0 {
		parentSet := make(map[string]bool, len(parent.AllowedRecipients))
		for _, r := range parent.AllowedRecipients {
			parentSet[strings.ToLower(r)] = true
		}
		for _, r := range child.AllowedRecipients {
			if !parentSet[strings.ToLower(r)] {
				return fmt.Errorf("child recipient %q not in parent recipients", r)
			}
		}
	}

	// Service types: child must be subset (and must inherit if parent restricts)
	if len(parent.AllowedServiceTypes) > 0 && len(child.AllowedServiceTypes) == 0 {
		return fmt.Errorf("child must set allowedServiceTypes when parent restricts them")
	}
	if len(parent.AllowedServiceTypes) > 0 && len(child.AllowedServiceTypes) > 0 {
		parentSet := make(map[string]bool, len(parent.AllowedServiceTypes))
		for _, s := range parent.AllowedServiceTypes {
			parentSet[strings.ToLower(s)] = true
		}
		for _, s := range child.AllowedServiceTypes {
			if !parentSet[strings.ToLower(s)] {
				return fmt.Errorf("child service type %q not in parent service types", s)
			}
		}
	}

	// Depth: child must be deeper
	if child.Depth <= parent.Depth {
		return fmt.Errorf("child depth %d must exceed parent depth %d", child.Depth, parent.Depth)
	}

	return nil
}
