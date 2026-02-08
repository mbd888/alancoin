// Package usdc provides shared USDC parsing and formatting utilities.
//
// USDC uses 6 decimal places. All amounts are stored as big.Int in
// the smallest unit (1 USDC = 1,000,000 units).
package usdc

import (
	"math/big"
	"strings"
)

const Decimals = 6

// Parse converts a decimal string (e.g. "1.50") to its smallest-unit
// big.Int representation (1500000). Returns (nil, false) on invalid input.
//
// Rules:
//   - Empty string returns (0, true)
//   - Negative amounts are rejected
//   - Multiple decimal points are rejected
//   - Fractional parts are padded/truncated to 6 decimal places
func Parse(s string) (*big.Int, bool) {
	if s == "" {
		return big.NewInt(0), true
	}

	if strings.HasPrefix(s, "-") {
		return nil, false
	}

	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return nil, false
	}
	whole := parts[0]
	frac := ""
	if len(parts) > 1 {
		frac = parts[1]
	}

	// Pad or trim to 6 decimals
	for len(frac) < Decimals {
		frac += "0"
	}
	frac = frac[:Decimals]

	combined := whole + frac
	result, ok := new(big.Int).SetString(combined, 10)
	return result, ok
}

// Format converts a smallest-unit big.Int to a human-readable decimal
// string with exactly 6 decimal places (e.g. "1.500000").
func Format(amount *big.Int) string {
	if amount == nil {
		return "0.000000"
	}
	neg := amount.Sign() < 0
	abs := new(big.Int).Abs(amount)
	s := abs.String()
	for len(s) < Decimals+1 {
		s = "0" + s
	}
	decimal := len(s) - Decimals
	result := s[:decimal] + "." + s[decimal:]
	if neg {
		result = "-" + result
	}
	return result
}
