package x402

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/mbd888/alancoin/internal/wallet"
)

// Client wraps http.Client with automatic 402 payment handling
type Client struct {
	httpClient *http.Client
	wallet     *wallet.Wallet

	// Configuration
	MaxRetries     int           // Max payment retries (default: 1)
	ConfirmTimeout time.Duration // Time to wait for tx confirmation (default: 30s)
	AutoPay        bool          // Automatically pay 402s (default: true)
	MaxPayment     string        // Max payment amount (default: unlimited)

	// Hooks
	OnPayment func(req *PaymentRequirement, proof *PaymentProof) // Called before each payment
}

// NewClient creates a new x402-enabled HTTP client
func NewClient(w *wallet.Wallet) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		wallet:         w,
		MaxRetries:     1,
		ConfirmTimeout: 30 * time.Second,
		AutoPay:        true,
	}
}

// Do performs an HTTP request with automatic 402 payment handling
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.DoContext(req.Context(), req)
}

// DoContext performs an HTTP request with context and automatic 402 handling
func (c *Client) DoContext(ctx context.Context, req *http.Request) (*http.Response, error) {
	// Clone the request body if present (we might need to retry)
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		_ = req.Body.Close()
	}

	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		// Reset body for retry
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytesReader(bodyBytes))
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}

		// Not a 402 - return response as-is
		if resp.StatusCode != http.StatusPaymentRequired {
			return resp, nil
		}

		// Don't auto-pay if disabled
		if !c.AutoPay {
			return resp, nil
		}

		// Parse payment requirement
		payReq, err := ParsePaymentRequirement(resp)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to parse payment requirement: %w", err)
		}

		// Check max payment limit
		if c.MaxPayment != "" {
			if err := c.checkPaymentLimit(payReq.Price); err != nil {
				return nil, err
			}
		}

		// Make the payment
		proof, err := c.makePayment(ctx, payReq)
		if err != nil {
			return nil, fmt.Errorf("payment failed: %w", err)
		}

		// Call hook if set
		if c.OnPayment != nil {
			c.OnPayment(payReq, proof)
		}

		// Add proof to request and retry
		if err := AddProofToRequest(req, proof); err != nil {
			return nil, fmt.Errorf("failed to add proof: %w", err)
		}
	}

	return nil, fmt.Errorf("max retries exceeded")
}

// Get performs a GET request with automatic 402 handling
func (c *Client) Get(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// makePayment executes a USDC transfer and waits for confirmation
func (c *Client) makePayment(ctx context.Context, req *PaymentRequirement) (*PaymentProof, error) {
	// Convert recipient string to address
	recipient := common.HexToAddress(req.Recipient)

	// Parse price string to big.Int
	price, err := wallet.ParseUSDC(req.Price)
	if err != nil {
		return nil, fmt.Errorf("invalid price: %w", err)
	}

	// Send the payment
	result, err := c.wallet.Transfer(ctx, recipient, price)
	if err != nil {
		return nil, fmt.Errorf("transfer failed: %w", err)
	}

	// Wait for confirmation if timeout is set
	if c.ConfirmTimeout > 0 {
		_, err = c.wallet.WaitForConfirmation(ctx, result.TxHash, c.ConfirmTimeout)
		if err != nil {
			return nil, fmt.Errorf("confirmation failed: %w", err)
		}
	}

	return CreatePaymentProof(result.TxHash, c.wallet.Address(), req.Nonce), nil
}

// checkPaymentLimit verifies the payment doesn't exceed max
func (c *Client) checkPaymentLimit(price string) error {
	maxAmount, err := wallet.ParseUSDC(c.MaxPayment)
	if err != nil {
		return fmt.Errorf("invalid max payment: %w", err)
	}

	reqAmount, err := wallet.ParseUSDC(price)
	if err != nil {
		return fmt.Errorf("invalid price: %w", err)
	}

	if reqAmount.Cmp(maxAmount) > 0 {
		return fmt.Errorf("payment %s exceeds max %s", price, c.MaxPayment)
	}

	return nil
}

// Helper to create a bytes reader
type bytesReaderWrapper struct {
	data []byte
	pos  int
}

func bytesReader(data []byte) io.Reader {
	return &bytesReaderWrapper{data: data}
}

func (r *bytesReaderWrapper) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
