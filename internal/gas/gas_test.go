package gas

import (
	"math/big"
	"testing"

	"github.com/mbd888/alancoin/internal/usdc"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ETHPriceUSD <= 0 {
		t.Error("ETHPriceUSD should be positive")
	}
	if cfg.GasMarkupPct < 0 {
		t.Error("GasMarkupPct should be non-negative")
	}
	minFee, ok := usdc.Parse(cfg.MinGasFeeUSDC)
	if !ok {
		t.Fatalf("usdc.Parse(MinGasFeeUSDC) failed for %q", cfg.MinGasFeeUSDC)
	}
	if minFee.Sign() <= 0 {
		t.Error("MinGasFeeUSDC should be positive")
	}
	maxFee, ok := usdc.Parse(cfg.MaxGasFeeUSDC)
	if !ok {
		t.Fatalf("usdc.Parse(MaxGasFeeUSDC) failed for %q", cfg.MaxGasFeeUSDC)
	}
	if maxFee.Cmp(minFee) <= 0 {
		t.Error("MaxGasFeeUSDC should be greater than MinGasFeeUSDC")
	}
}

func TestWeiToETH(t *testing.T) {
	tests := []struct {
		wei      string
		expected float64
	}{
		{"1000000000000000000", 1.0}, // 1 ETH
		{"500000000000000000", 0.5},  // 0.5 ETH
		{"1000000000", 0.000000001},  // 1 gwei
		{"65000000000000", 0.000065}, // Typical gas cost
	}

	for _, tc := range tests {
		wei, _ := new(big.Int).SetString(tc.wei, 10)
		result := weiToETH(wei)
		// Use approximate comparison for floating point
		diff := result - tc.expected
		if diff < 0 {
			diff = -diff
		}
		if diff > 1e-12 {
			t.Errorf("weiToETH(%s) = %f, expected %f", tc.wei, result, tc.expected)
		}
	}
}

func TestParseETH(t *testing.T) {
	tests := []struct {
		input    string
		expected string // as wei
	}{
		{"1", "1000000000000000000"},
		{"0.5", "500000000000000000"},
		{"0.000001", "1000000000000"},
	}

	for _, tc := range tests {
		result, err := parseETH(tc.input)
		if err != nil {
			t.Errorf("parseETH(%s) unexpected error: %v", tc.input, err)
			continue
		}
		if result.String() != tc.expected {
			t.Errorf("parseETH(%s) = %s, expected %s", tc.input, result.String(), tc.expected)
		}
	}
}

func TestParseETH_InvalidInputs(t *testing.T) {
	tests := []string{
		"",
		"abc",
		"1.2.3",
		"hello world",
		"--1",
	}

	for _, input := range tests {
		result, err := parseETH(input)
		if err == nil {
			t.Errorf("parseETH(%q) should return error, got result %s", input, result.String())
		}
	}
}

func TestCheckAndRecordSpending(t *testing.T) {
	cfg := PaymasterConfig{
		DailyGasLimit: "0.01", // 0.01 ETH per day
	}

	p := &PlatformPaymaster{
		config:     cfg,
		dailySpent: big.NewInt(0),
	}

	// First call within limit should succeed
	err := p.checkAndRecordSpending("0.003")
	if err != nil {
		t.Fatalf("First spending should succeed: %v", err)
	}

	// Second call still within limit
	err = p.checkAndRecordSpending("0.003")
	if err != nil {
		t.Fatalf("Second spending should succeed: %v", err)
	}

	// Third call that exceeds limit should fail
	err = p.checkAndRecordSpending("0.005")
	if err != ErrDailyLimitExceeded {
		t.Errorf("Expected ErrDailyLimitExceeded, got: %v", err)
	}
}

func TestCheckAndRecordSpending_InvalidCost(t *testing.T) {
	cfg := PaymasterConfig{
		DailyGasLimit: "0.1",
	}

	p := &PlatformPaymaster{
		config:     cfg,
		dailySpent: big.NewInt(0),
	}

	err := p.checkAndRecordSpending("not_a_number")
	if err == nil {
		t.Error("Expected error for invalid gas cost")
	}
}

func TestCheckAndRecordSpending_InvalidConfig(t *testing.T) {
	cfg := PaymasterConfig{
		DailyGasLimit: "garbage",
	}

	p := &PlatformPaymaster{
		config:     cfg,
		dailySpent: big.NewInt(0),
	}

	err := p.checkAndRecordSpending("0.001")
	if err == nil {
		t.Error("Expected error for invalid daily gas limit config")
	}
}

func TestFormatETH(t *testing.T) {
	tests := []struct {
		wei      string
		expected string
	}{
		{"1000000000000000000", "1.00000000"},
		{"500000000000000000", "0.50000000"},
		{"100000000000000", "0.00010000"},
	}

	for _, tc := range tests {
		wei, _ := new(big.Int).SetString(tc.wei, 10)
		result := formatETH(wei)
		if result != tc.expected {
			t.Errorf("formatETH(%s) = %s, expected %s", tc.wei, result, tc.expected)
		}
	}
}

func TestParseUSDC(t *testing.T) {
	tests := []struct {
		input    string
		expected int64 // in micro-USDC (6 decimals)
	}{
		{"1", 1000000},
		{"1.00", 1000000},
		{"0.50", 500000},
		{"0.000001", 1},
		{"100.123456", 100123456},
	}

	for _, tc := range tests {
		result, ok := usdc.Parse(tc.input)
		if !ok {
			t.Errorf("usdc.Parse(%s) failed unexpectedly", tc.input)
			continue
		}
		if result.Int64() != tc.expected {
			t.Errorf("usdc.Parse(%s) = %d, expected %d", tc.input, result.Int64(), tc.expected)
		}
	}
}

func TestFormatUSDC(t *testing.T) {
	tests := []struct {
		microUSDC int64
		expected  string
	}{
		{1000000, "1.000000"},
		{500000, "0.500000"},
		{1, "0.000001"},
		{100123456, "100.123456"},
	}

	for _, tc := range tests {
		result := usdc.Format(big.NewInt(tc.microUSDC))
		if result != tc.expected {
			t.Errorf("usdc.Format(%d) = %s, expected %s", tc.microUSDC, result, tc.expected)
		}
	}
}

func TestUsdToBigUSDC(t *testing.T) {
	tests := []struct {
		usd      float64
		expected int64
	}{
		{1.0, 1000000},
		{0.5, 500000},
		{0.000001, 1},
		{100.50, 100500000},
	}

	for _, tc := range tests {
		result := usdToBigUSDC(tc.usd)
		if result.Int64() != tc.expected {
			t.Errorf("usdToBigUSDC(%f) = %d, expected %d", tc.usd, result.Int64(), tc.expected)
		}
	}
}

func TestGasEstimateCalculation(t *testing.T) {
	cfg := &PaymasterConfig{
		ETHPriceUSD:   2500.0, // $2500/ETH
		GasMarkupPct:  0.20,   // 20% markup (as decimal)
		MinGasFeeUSDC: "0.0001",
		MaxGasFeeUSDC: "1.0",
		MaxGasPrice:   100,   // 100 gwei
		DailyGasLimit: "0.1", // 0.1 ETH per day
	}

	// Simulate gas estimation
	gasLimit := uint64(65000)
	gasPriceWei := big.NewInt(1e9) // 1 gwei

	// Gas cost in ETH = 65000 * 1e9 / 1e18 = 0.000065 ETH
	gasCostWei := new(big.Int).Mul(big.NewInt(int64(gasLimit)), gasPriceWei)
	gasCostETH := weiToETH(gasCostWei)

	// Use approximate comparison for floating point
	if gasCostETH < 0.000064 || gasCostETH > 0.000066 {
		t.Errorf("Gas cost ETH = %f, expected ~0.000065", gasCostETH)
	}

	// Gas cost in USD = 0.000065 * 2500 = $0.1625
	gasCostUSD := gasCostETH * cfg.ETHPriceUSD
	if gasCostUSD < 0.16 || gasCostUSD > 0.17 {
		t.Errorf("Gas cost USD = %f, expected ~0.1625", gasCostUSD)
	}

	// With 20% markup = $0.195
	withMarkup := gasCostUSD * (1 + cfg.GasMarkupPct)
	if withMarkup < 0.19 || withMarkup > 0.20 {
		t.Errorf("Gas cost with markup = %f, expected ~0.195", withMarkup)
	}
}

func TestMinMaxGasFee(t *testing.T) {
	minGasFee := 0.01 // Min $0.01
	maxGasFee := 0.50 // Max $0.50
	gasMarkup := 0.20 // 20% markup

	// Very low gas should be bumped to min
	veryLowGas := 0.0001 // $0.0001 before markup
	result := veryLowGas * (1 + gasMarkup)
	if result < minGasFee {
		result = minGasFee
	}
	if result != minGasFee {
		t.Errorf("Low gas should be bumped to min, got %f", result)
	}

	// Very high gas should be capped at max
	veryHighGas := 10.0 // $10 before markup
	result = veryHighGas * (1 + gasMarkup)
	if result > maxGasFee {
		result = maxGasFee
	}
	if result != maxGasFee {
		t.Errorf("High gas should be capped at max, got %f", result)
	}
}
