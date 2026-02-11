package receipts

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const signatureValidity = 30 * 24 * time.Hour // 30 days — receipts are proof documents

// Signer signs receipt payloads with HMAC-SHA256.
type Signer struct {
	secret []byte
}

// NewSigner creates a new HMAC signer. If secret is empty, signing is disabled.
func NewSigner(secret string) *Signer {
	if secret == "" {
		return nil
	}
	return &Signer{secret: []byte(secret)}
}

// Sign computes HMAC-SHA256 of the canonical JSON of payload.
func (s *Signer) Sign(payload interface{}) (signature, issuedAt, expiresAt string, err error) {
	if s == nil {
		return "", "", "", nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", "", "", err
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(data)
	now := time.Now().UTC()
	return hex.EncodeToString(mac.Sum(nil)),
		now.Format(time.RFC3339),
		now.Add(signatureValidity).Format(time.RFC3339),
		nil
}

// Verify checks the HMAC-SHA256 signature of the canonical JSON payload.
func (s *Signer) Verify(payload interface{}, signature string) bool {
	if s == nil {
		return false
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(data)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
