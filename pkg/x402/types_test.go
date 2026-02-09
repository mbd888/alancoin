package x402

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIs402Response(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{"402 response", http.StatusPaymentRequired, true},
		{"200 response", http.StatusOK, false},
		{"401 response", http.StatusUnauthorized, false},
		{"403 response", http.StatusForbidden, false},
		{"500 response", http.StatusInternalServerError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.statusCode}
			assert.Equal(t, tt.want, Is402Response(resp))
		})
	}
}

func TestParsePaymentRequirement(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
		wantPrice  string
	}{
		{
			name:       "valid 402 response",
			statusCode: http.StatusPaymentRequired,
			body:       `{"price":"0.001","currency":"USDC","chain":"base-sepolia","chainId":84532,"recipient":"0x1234"}`,
			wantErr:    false,
			wantPrice:  "0.001",
		},
		{
			name:       "not 402 response",
			statusCode: http.StatusOK,
			body:       `{"price":"0.001"}`,
			wantErr:    true,
		},
		{
			name:       "invalid JSON",
			statusCode: http.StatusPaymentRequired,
			body:       `not-json`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(bytes.NewBufferString(tt.body)),
			}

			req, err := ParsePaymentRequirement(resp)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPrice, req.Price)
		})
	}
}

func TestCreatePaymentProof(t *testing.T) {
	proof := CreatePaymentProof(
		"0xabcdef123456",
		"0x1234567890",
		"nonce-123",
	)

	assert.Equal(t, "0xabcdef123456", proof.TxHash)
	assert.Equal(t, "0x1234567890", proof.From)
	assert.Equal(t, "nonce-123", proof.Nonce)
	assert.Greater(t, proof.Timestamp, int64(0))
}

func TestPaymentProof_ToHeader(t *testing.T) {
	proof := &PaymentProof{
		TxHash:    "0xabcdef",
		From:      "0x123456",
		Nonce:     "test-nonce",
		Timestamp: 1234567890,
	}

	header, err := proof.ToHeader()
	require.NoError(t, err)
	assert.Contains(t, header, "0xabcdef")
	assert.Contains(t, header, "0x123456")
	assert.Contains(t, header, "test-nonce")
}

func TestAddProofToRequest(t *testing.T) {
	proof := &PaymentProof{
		TxHash:    "0xabcdef",
		From:      "0x123456",
		Timestamp: 1234567890,
	}

	req := httptest.NewRequest("GET", "/test", nil)
	err := AddProofToRequest(req, proof)
	require.NoError(t, err)

	header := req.Header.Get("X-Payment-Proof")
	assert.NotEmpty(t, header)
	assert.Contains(t, header, "0xabcdef")
}

func TestError(t *testing.T) {
	err := &Error{
		Code:    "payment_failed",
		Message: "Insufficient funds",
	}

	assert.Equal(t, "payment_failed: Insufficient funds", err.Error())
}

// Integration-style test with mock server

func TestClient_Get_NoPay(t *testing.T) {
	// Create a server that returns 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message":"success"}`))
	}))
	defer server.Close()

	// Create client without wallet (can't actually pay)
	client := &Client{
		httpClient: http.DefaultClient,
		AutoPay:    false, // Disable auto-pay for this test
	}

	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestClient_Get_402_NoPay(t *testing.T) {
	// Create a server that returns 402
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Payment-Required", "true")
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"price":"0.001","currency":"USDC","chain":"base-sepolia","chainId":84532,"recipient":"0x123"}`))
	}))
	defer server.Close()

	// Create client with auto-pay disabled
	client := &Client{
		httpClient: http.DefaultClient,
		AutoPay:    false,
	}

	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusPaymentRequired, resp.StatusCode)
}

// Benchmark

func BenchmarkParsePaymentRequirement(b *testing.B) {
	body := `{"price":"0.001","currency":"USDC","chain":"base-sepolia","chainId":84532,"recipient":"0x1234567890123456789012345678901234567890"}`

	for i := 0; i < b.N; i++ {
		resp := &http.Response{
			StatusCode: http.StatusPaymentRequired,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}
		ParsePaymentRequirement(resp)
	}
}
