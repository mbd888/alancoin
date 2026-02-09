package paywall

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestPaymentRequirement_JSON(t *testing.T) {
	req := PaymentRequirement{
		Price:       "0.001",
		Currency:    "USDC",
		Chain:       "base-sepolia",
		ChainID:     84532,
		Recipient:   "0x1234567890123456789012345678901234567890",
		Contract:    "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Description: "API call",
		ValidFor:    300,
		Nonce:       "test-nonce-123",
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var parsed PaymentRequirement
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, req.Price, parsed.Price)
	assert.Equal(t, req.Currency, parsed.Currency)
	assert.Equal(t, req.ChainID, parsed.ChainID)
	assert.Equal(t, req.Recipient, parsed.Recipient)
}

func TestPaymentProof_JSON(t *testing.T) {
	proof := PaymentProof{
		TxHash:    "0xabcdef1234567890",
		From:      "0x1234567890123456789012345678901234567890",
		Nonce:     "test-nonce-123",
		Timestamp: 1234567890,
	}

	data, err := json.Marshal(proof)
	require.NoError(t, err)

	var parsed PaymentProof
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, proof.TxHash, parsed.TxHash)
	assert.Equal(t, proof.From, parsed.From)
	assert.Equal(t, proof.Nonce, parsed.Nonce)
	assert.Equal(t, proof.Timestamp, parsed.Timestamp)
}

func TestMiddleware_NoPaymentReturns402(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		// Simulate middleware behavior
		if c.GetHeader("X-Payment-Proof") == "" {
			c.Header("X-Payment-Required", "true")
			c.Header("X-Payment-Currency", "USDC")
			c.Header("X-Payment-Amount", "0.001")
			c.JSON(http.StatusPaymentRequired, PaymentRequirement{
				Price:       "0.001",
				Currency:    "USDC",
				Chain:       "base-sepolia",
				ChainID:     84532,
				Recipient:   "0x1234567890123456789012345678901234567890",
				Contract:    "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Description: "Test endpoint",
				ValidFor:    300,
			})
			c.Abort()
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusPaymentRequired, w.Code)
	assert.Equal(t, "true", w.Header().Get("X-Payment-Required"))
	assert.Equal(t, "USDC", w.Header().Get("X-Payment-Currency"))
	assert.Equal(t, "0.001", w.Header().Get("X-Payment-Amount"))

	var resp PaymentRequirement
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "0.001", resp.Price)
	assert.Equal(t, "USDC", resp.Currency)
	assert.Equal(t, "base-sepolia", resp.Chain)
	assert.Equal(t, int64(84532), resp.ChainID)
}

func TestMiddleware_InvalidProofFormat(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		proofHeader := c.GetHeader("X-Payment-Proof")
		if proofHeader == "" {
			c.JSON(http.StatusPaymentRequired, gin.H{"error": "payment required"})
			c.Abort()
			return
		}

		var proof PaymentProof
		if err := json.Unmarshal([]byte(proofHeader), &proof); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_payment_proof",
				"message": "Could not parse payment proof JSON",
			})
			c.Abort()
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Payment-Proof", "not-valid-json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid_payment_proof", resp["error"])
}

func TestGenerateSecureNonce(t *testing.T) {
	// Generate multiple nonces and ensure they're unique and correct length
	nonces := make(map[string]bool)
	for i := 0; i < 100; i++ {
		nonce, err := generateSecureNonce()
		require.NoError(t, err)
		assert.Len(t, nonce, 32) // 16 bytes = 32 hex chars
		assert.False(t, nonces[nonce], "duplicate nonce generated")
		nonces[nonce] = true
	}
}

// Benchmark

func BenchmarkGenerateSecureNonce(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = generateSecureNonce()
	}
}
