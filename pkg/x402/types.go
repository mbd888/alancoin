// Package x402 implements the x402 protocol types and client
// This is the foundation for the Alancoin SDK
package x402

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PaymentRequirement is returned by servers in 402 responses
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

// PaymentProof is sent to servers to prove payment
type PaymentProof struct {
	TxHash    string `json:"txHash"`
	From      string `json:"from"`
	Nonce     string `json:"nonce,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// Error represents an x402 error response
type Error struct {
	Code    string `json:"error"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Is402Response checks if an HTTP response is a 402 Payment Required
func Is402Response(resp *http.Response) bool {
	return resp.StatusCode == http.StatusPaymentRequired
}

// ParsePaymentRequirement extracts payment requirements from a 402 response
func ParsePaymentRequirement(resp *http.Response) (*PaymentRequirement, error) {
	if resp.StatusCode != http.StatusPaymentRequired {
		return nil, fmt.Errorf("not a 402 response: got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var req PaymentRequirement
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to parse payment requirement: %w", err)
	}

	return &req, nil
}

// CreatePaymentProof creates a proof object for a completed payment
func CreatePaymentProof(txHash, fromAddress, nonce string) *PaymentProof {
	return &PaymentProof{
		TxHash:    txHash,
		From:      fromAddress,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
	}
}

// ToHeader serializes the payment proof for HTTP header
func (p *PaymentProof) ToHeader() (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("failed to marshal proof: %w", err)
	}
	return string(data), nil
}

// AddProofToRequest adds the payment proof header to an HTTP request
func AddProofToRequest(req *http.Request, proof *PaymentProof) error {
	header, err := proof.ToHeader()
	if err != nil {
		return err
	}
	req.Header.Set("X-Payment-Proof", header)
	return nil
}
