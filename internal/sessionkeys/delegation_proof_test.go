package sessionkeys

import (
	"encoding/hex"
	"testing"
	"time"
)

func makeRootKey() *SessionKey {
	return &SessionKey{
		ID:        "sk_root",
		OwnerAddr: "0xowner",
		PublicKey: "0xrootpubkey000000000000000000000000000000",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:            "100.00",
			MaxPerTransaction:   "10.00",
			MaxPerDay:           "50.00",
			ExpiresAt:           time.Now().Add(24 * time.Hour),
			AllowedServiceTypes: []string{"inference", "translation"},
			Scopes:              []string{"spend", "delegate"},
		},
		Depth: 0,
	}
}

func makeChildKey(parentID string) *SessionKey {
	return &SessionKey{
		ID:        "sk_child1",
		OwnerAddr: "0xowner",
		PublicKey: "0xchildpubkey00000000000000000000000000000",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:            "25.00",
			MaxPerTransaction:   "5.00",
			MaxPerDay:           "20.00",
			ExpiresAt:           time.Now().Add(12 * time.Hour),
			AllowedServiceTypes: []string{"inference"},
			Scopes:              []string{"spend"},
		},
		ParentKeyID: parentID,
		Depth:       1,
	}
}

func TestGenerateRootSecret(t *testing.T) {
	secret, err := GenerateRootSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != RootSecretSize {
		t.Errorf("secret length = %d, want %d", len(secret), RootSecretSize)
	}

	// Two secrets should be different
	secret2, _ := GenerateRootSecret()
	if hex.EncodeToString(secret) == hex.EncodeToString(secret2) {
		t.Error("two generated secrets are identical")
	}
}

func TestNewRootProof(t *testing.T) {
	secret, _ := GenerateRootSecret()
	key := makeRootKey()

	proof := NewRootProof(secret, key)

	if len(proof.Caveats) != 1 {
		t.Fatalf("caveats len = %d, want 1", len(proof.Caveats))
	}
	if proof.RootKeyID != "sk_root" {
		t.Errorf("rootKeyId = %q", proof.RootKeyID)
	}
	if proof.Tag == "" {
		t.Error("tag is empty")
	}
	if proof.Caveats[0].KeyID != "sk_root" {
		t.Errorf("caveat keyId = %q", proof.Caveats[0].KeyID)
	}
	if proof.Caveats[0].MaxTotal != "100.00" {
		t.Errorf("caveat maxTotal = %q", proof.Caveats[0].MaxTotal)
	}
}

func TestVerifyProof_RootOnly(t *testing.T) {
	secret, _ := GenerateRootSecret()
	key := makeRootKey()
	proof := NewRootProof(secret, key)

	if err := VerifyProof(secret, proof); err != nil {
		t.Fatalf("verify root proof: %v", err)
	}
}

func TestVerifyProof_WrongSecret(t *testing.T) {
	secret, _ := GenerateRootSecret()
	wrongSecret, _ := GenerateRootSecret()
	key := makeRootKey()
	proof := NewRootProof(secret, key)

	if err := VerifyProof(wrongSecret, proof); err == nil {
		t.Fatal("expected verification failure with wrong secret")
	}
}

func TestExtendProof_SingleDelegation(t *testing.T) {
	secret, _ := GenerateRootSecret()
	rootKey := makeRootKey()
	rootProof := NewRootProof(secret, rootKey)

	childKey := makeChildKey("sk_root")
	childProof, err := ExtendProof(rootProof, childKey)
	if err != nil {
		t.Fatalf("extend proof: %v", err)
	}

	if len(childProof.Caveats) != 2 {
		t.Fatalf("caveats len = %d, want 2", len(childProof.Caveats))
	}
	if childProof.Tag == rootProof.Tag {
		t.Error("child tag should differ from root tag")
	}
	if childProof.RootKeyID != "sk_root" {
		t.Errorf("rootKeyId = %q", childProof.RootKeyID)
	}

	// Verify the extended proof
	if err := VerifyProof(secret, childProof); err != nil {
		t.Fatalf("verify child proof: %v", err)
	}
}

func TestExtendProof_MultiLevel(t *testing.T) {
	secret, _ := GenerateRootSecret()
	rootKey := makeRootKey()
	proof := NewRootProof(secret, rootKey)

	// Level 1
	child1 := makeChildKey("sk_root")
	proof, err := ExtendProof(proof, child1)
	if err != nil {
		t.Fatal(err)
	}

	// Level 2
	grandchild := &SessionKey{
		ID:        "sk_grandchild",
		OwnerAddr: "0xowner",
		PublicKey: "0xgrandchildpk000000000000000000000000000",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:            "10.00",
			MaxPerTransaction:   "2.00",
			ExpiresAt:           time.Now().Add(6 * time.Hour),
			AllowedServiceTypes: []string{"inference"},
			Scopes:              []string{"spend"},
		},
		ParentKeyID: "sk_child1",
		Depth:       2,
	}
	proof, err = ExtendProof(proof, grandchild)
	if err != nil {
		t.Fatal(err)
	}

	if len(proof.Caveats) != 3 {
		t.Fatalf("caveats len = %d, want 3", len(proof.Caveats))
	}

	// Verify full chain
	if err := VerifyProof(secret, proof); err != nil {
		t.Fatalf("verify multi-level proof: %v", err)
	}
}

func TestExtendProof_AttenuationViolation_BudgetTooHigh(t *testing.T) {
	secret, _ := GenerateRootSecret()
	rootKey := makeRootKey() // maxTotal = 100.00
	rootProof := NewRootProof(secret, rootKey)

	badChild := &SessionKey{
		ID:        "sk_bad",
		OwnerAddr: "0xowner",
		PublicKey: "0xbadpubkey0000000000000000000000000000000",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:  "200.00", // exceeds parent's 100.00
			ExpiresAt: time.Now().Add(12 * time.Hour),
			Scopes:    []string{"spend"},
		},
		ParentKeyID: "sk_root",
		Depth:       1,
	}

	_, err := ExtendProof(rootProof, badChild)
	if err == nil {
		t.Fatal("expected attenuation violation for budget > parent")
	}
}

func TestExtendProof_AttenuationViolation_ExpiryTooLate(t *testing.T) {
	secret, _ := GenerateRootSecret()
	rootKey := makeRootKey() // expires in 24h
	rootProof := NewRootProof(secret, rootKey)

	badChild := &SessionKey{
		ID:        "sk_bad",
		OwnerAddr: "0xowner",
		PublicKey: "0xbadpubkey0000000000000000000000000000000",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:  "50.00",
			ExpiresAt: time.Now().Add(48 * time.Hour), // exceeds parent's 24h
			Scopes:    []string{"spend"},
		},
		ParentKeyID: "sk_root",
		Depth:       1,
	}

	_, err := ExtendProof(rootProof, badChild)
	if err == nil {
		t.Fatal("expected attenuation violation for expiry > parent")
	}
}

func TestExtendProof_AttenuationViolation_ScopeNotSubset(t *testing.T) {
	secret, _ := GenerateRootSecret()
	rootKey := makeRootKey() // scopes: ["spend", "delegate"]
	rootProof := NewRootProof(secret, rootKey)

	badChild := &SessionKey{
		ID:        "sk_bad",
		OwnerAddr: "0xowner",
		PublicKey: "0xbadpubkey0000000000000000000000000000000",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:  "50.00",
			ExpiresAt: time.Now().Add(12 * time.Hour),
			Scopes:    []string{"spend", "escrow"}, // "escrow" not in parent
		},
		ParentKeyID: "sk_root",
		Depth:       1,
	}

	_, err := ExtendProof(rootProof, badChild)
	if err == nil {
		t.Fatal("expected attenuation violation for scope not in parent")
	}
}

func TestVerifyProof_TamperedCaveat(t *testing.T) {
	secret, _ := GenerateRootSecret()
	rootKey := makeRootKey()
	rootProof := NewRootProof(secret, rootKey)

	childKey := makeChildKey("sk_root")
	childProof, _ := ExtendProof(rootProof, childKey)

	// Tamper with the child caveat's budget
	childProof.Caveats[1].MaxTotal = "999.00"

	if err := VerifyProof(secret, childProof); err == nil {
		t.Fatal("expected verification failure after tampering")
	}
}

func TestVerifyProof_TamperedTag(t *testing.T) {
	secret, _ := GenerateRootSecret()
	rootKey := makeRootKey()
	proof := NewRootProof(secret, rootKey)

	// Tamper with the tag
	proof.Tag = "deadbeef" + proof.Tag[8:]

	if err := VerifyProof(secret, proof); err == nil {
		t.Fatal("expected verification failure after tag tampering")
	}
}

func TestVerifyProof_Expired(t *testing.T) {
	secret, _ := GenerateRootSecret()
	key := &SessionKey{
		ID:        "sk_expired",
		OwnerAddr: "0xowner",
		PublicKey: "0xexpiredpubkey00000000000000000000000000",
		CreatedAt: time.Now().Add(-2 * time.Hour),
		Permission: Permission{
			MaxTotal:  "100.00",
			ExpiresAt: time.Now().Add(-1 * time.Hour), // already expired
			Scopes:    []string{"spend"},
		},
		Depth: 0,
	}

	proof := NewRootProof(secret, key)

	if err := VerifyProof(secret, proof); err == nil {
		t.Fatal("expected verification failure for expired proof")
	}
}

func TestVerifyBudget(t *testing.T) {
	secret, _ := GenerateRootSecret()
	rootKey := makeRootKey()
	proof := NewRootProof(secret, rootKey)

	childKey := makeChildKey("sk_root") // maxTotal=25.00, maxPerTx=5.00
	childProof, _ := ExtendProof(proof, childKey)

	// Within budget
	if err := VerifyBudget(childProof, "3.00", "10.00"); err != nil {
		t.Fatalf("expected budget check to pass: %v", err)
	}

	// Exceeds per-tx
	if err := VerifyBudget(childProof, "6.00", "0.00"); err != ErrExceedsPerTx {
		t.Errorf("expected ErrExceedsPerTx, got %v", err)
	}

	// Exceeds total
	if err := VerifyBudget(childProof, "3.00", "23.00"); err != ErrExceedsTotal {
		t.Errorf("expected ErrExceedsTotal, got %v", err)
	}
}

func TestCanonicalize_Deterministic(t *testing.T) {
	c := Caveat{
		MaxTotal:            "10.00",
		ExpiresAt:           time.Unix(1700000000, 0),
		AllowedServiceTypes: []string{"translation", "inference"},
		Scopes:              []string{"delegate", "spend"},
		PublicKey:           "0xabc",
		KeyID:               "sk_1",
		Depth:               1,
		IssuedAt:            time.Unix(1699999000, 0),
		IssuerID:            "sk_0",
	}

	b1 := canonicalize(c)
	b2 := canonicalize(c)

	if string(b1) != string(b2) {
		t.Error("canonicalize is not deterministic")
	}

	// Reverse the order — should still produce same output
	c.AllowedServiceTypes = []string{"inference", "translation"}
	c.Scopes = []string{"spend", "delegate"}
	b3 := canonicalize(c)

	if string(b1) != string(b3) {
		t.Error("canonicalize is not order-independent")
	}
}

func TestExtendProof_ServiceTypeNotSubset(t *testing.T) {
	secret, _ := GenerateRootSecret()
	rootKey := makeRootKey() // service types: ["inference", "translation"]
	rootProof := NewRootProof(secret, rootKey)

	badChild := &SessionKey{
		ID:        "sk_bad",
		OwnerAddr: "0xowner",
		PublicKey: "0xbadpubkey0000000000000000000000000000000",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:            "50.00",
			ExpiresAt:           time.Now().Add(12 * time.Hour),
			AllowedServiceTypes: []string{"inference", "summarization"}, // "summarization" not in parent
			Scopes:              []string{"spend"},
		},
		ParentKeyID: "sk_root",
		Depth:       1,
	}

	_, err := ExtendProof(rootProof, badChild)
	if err == nil {
		t.Fatal("expected attenuation violation for service type not in parent")
	}
}
