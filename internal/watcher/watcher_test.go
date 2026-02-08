package watcher

import (
	"math/big"
	"testing"

	"github.com/mbd888/alancoin/internal/usdc"
)

// ---------------------------------------------------------------------------
// formatUSDC tests - the main unit-testable function
// ---------------------------------------------------------------------------

func TestFormatUSDC(t *testing.T) {
	tests := []struct {
		name     string
		amount   *big.Int
		expected string
	}{
		{"nil", nil, "0.000000"},
		{"zero", big.NewInt(0), "0.000000"},
		{"one micro USDC", big.NewInt(1), "0.000001"},
		{"one cent", big.NewInt(10000), "0.010000"},
		{"one dollar", big.NewInt(1000000), "1.000000"},
		{"ten dollars", big.NewInt(10000000), "10.000000"},
		{"hundred dollars", big.NewInt(100000000), "100.000000"},
		{"1234.567890", big.NewInt(1234567890), "1234.567890"},
		{"small amount 0.000123", big.NewInt(123), "0.000123"},
		{"0.100000", big.NewInt(100000), "0.100000"},
		{"max practical", new(big.Int).SetUint64(999999999999), "999999.999999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := usdc.Format(tt.amount)
			if result != tt.expected {
				t.Errorf("usdc.Format(%v) = %q, want %q", tt.amount, result, tt.expected)
			}
		})
	}
}

func TestFormatUSDC_LargeAmounts(t *testing.T) {
	// 1 million USDC
	amount := new(big.Int).SetUint64(1000000000000)
	result := usdc.Format(amount)
	if result != "1000000.000000" {
		t.Errorf("usdc.Format(1M USDC) = %q, want 1000000.000000", result)
	}
}

func TestFormatUSDC_VerySmall(t *testing.T) {
	// Exactly 1 (smallest unit)
	result := usdc.Format(big.NewInt(1))
	if result != "0.000001" {
		t.Errorf("usdc.Format(1) = %q, want 0.000001", result)
	}

	// 999999 (just under $1)
	result = usdc.Format(big.NewInt(999999))
	if result != "0.999999" {
		t.Errorf("usdc.Format(999999) = %q, want 0.999999", result)
	}
}

// ---------------------------------------------------------------------------
// Config tests
// ---------------------------------------------------------------------------

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.PollInterval == 0 {
		t.Error("Expected non-zero poll interval")
	}
	if cfg.StartBlock != 0 {
		t.Errorf("Expected start block 0, got %d", cfg.StartBlock)
	}
}
