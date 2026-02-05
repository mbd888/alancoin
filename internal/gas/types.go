// Package gas provides gas abstraction for agent transactions.
//
// The goal: Agents only deal with USDC. They never need ETH.
//
// How it works:
// 1. Agent wants to send $1 USDC to another agent
// 2. We estimate gas cost (~$0.001 on Base)
// 3. Agent is charged $1.001 USDC total (transfer + gas fee)
// 4. Platform sponsors the ETH gas, gets reimbursed in USDC
//
// Future: Full ERC-4337 with smart accounts and on-chain paymasters
package gas

import (
	"context"
	"math/big"
	"time"
)

// Paymaster sponsors gas for transactions
type Paymaster interface {
	// SponsorTransaction pays gas for a transaction and returns the sponsored tx
	SponsorTransaction(ctx context.Context, req *SponsorRequest) (*SponsorResult, error)

	// EstimateGasFee estimates the gas cost in USDC for a transaction
	EstimateGasFee(ctx context.Context, req *EstimateRequest) (*GasEstimate, error)

	// GetBalance returns the paymaster's ETH balance (for monitoring)
	GetBalance(ctx context.Context) (*big.Int, error)
}

// SponsorRequest is a request to sponsor a transaction
type SponsorRequest struct {
	// The original transaction details
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"` // USDC amount

	// Optional: specific gas settings
	GasLimit    uint64 `json:"gasLimit,omitempty"`
	GasPriceWei string `json:"gasPriceWei,omitempty"`

	// For tracking
	AgentAddress string `json:"agentAddress"`
	ServiceID    string `json:"serviceId,omitempty"`
}

// SponsorResult is the result of a sponsored transaction
type SponsorResult struct {
	// Transaction details
	TxHash       string `json:"txHash"`
	From         string `json:"from"`
	To           string `json:"to"`
	Amount       string `json:"amount"`       // Original USDC amount
	GasFeeUSDC   string `json:"gasFeeUsdc"`   // Gas fee charged in USDC
	TotalCharged string `json:"totalCharged"` // Amount + GasFee

	// Gas details
	GasUsed     uint64 `json:"gasUsed"`
	GasPriceWei string `json:"gasPriceWei"`
	GasCostETH  string `json:"gasCostEth"`

	// Status
	Status    string    `json:"status"` // pending, confirmed, failed
	Timestamp time.Time `json:"timestamp"`
}

// EstimateRequest is a request to estimate gas fees
type EstimateRequest struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Amount   string `json:"amount"` // USDC amount
	GasLimit uint64 `json:"gasLimit,omitempty"`
}

// GasEstimate is the estimated gas cost
type GasEstimate struct {
	GasLimit    uint64 `json:"gasLimit"`
	GasPriceWei string `json:"gasPriceWei"`
	GasCostETH  string `json:"gasCostEth"`
	GasCostUSDC string `json:"gasCostUsdc"` // What we'll charge the agent
	ETHPriceUSD string `json:"ethPriceUsd"` // ETH/USD rate used

	// For display
	TotalWithGas string    `json:"totalWithGas"` // Original amount + gas fee
	ValidUntil   time.Time `json:"validUntil"`   // Estimate expires
}

// PaymasterConfig configures the paymaster
type PaymasterConfig struct {
	// Wallet that pays for gas
	PrivateKey string
	RPCURL     string
	ChainID    int64

	// USDC contract for receiving reimbursement
	USDCContract string

	// Pricing
	ETHPriceUSD   float64 // Fixed ETH price (or fetch from oracle)
	GasMarkupPct  float64 // Markup on gas (e.g., 0.1 = 10%)
	MinGasFeeUSDC string  // Minimum gas fee to charge
	MaxGasFeeUSDC string  // Maximum gas fee to charge

	// Safety
	MaxGasPrice   uint64 // Max gas price in gwei
	DailyGasLimit string // Max ETH to spend per day
}

// DefaultConfig returns sensible defaults for Base
func DefaultConfig() PaymasterConfig {
	return PaymasterConfig{
		ETHPriceUSD:   2500.0,   // Approximate - should use oracle
		GasMarkupPct:  0.2,      // 20% markup to cover volatility
		MinGasFeeUSDC: "0.0001", // $0.0001 minimum
		MaxGasFeeUSDC: "1.0",    // $1 maximum
		MaxGasPrice:   100,      // 100 gwei max
		DailyGasLimit: "0.1",    // 0.1 ETH per day max
	}
}

// GasSponsorshipError represents a sponsorship failure
type GasSponsorshipError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

func (e *GasSponsorshipError) Error() string {
	return e.Message
}

// Common errors
var (
	ErrInsufficientPaymasterBalance = &GasSponsorshipError{
		Code:    "insufficient_balance",
		Message: "Paymaster has insufficient ETH balance",
	}
	ErrGasPriceTooHigh = &GasSponsorshipError{
		Code:    "gas_price_too_high",
		Message: "Current gas price exceeds maximum",
	}
	ErrDailyLimitExceeded = &GasSponsorshipError{
		Code:    "daily_limit_exceeded",
		Message: "Daily gas sponsorship limit exceeded",
	}
	ErrEstimateFailed = &GasSponsorshipError{
		Code:    "estimate_failed",
		Message: "Failed to estimate gas",
	}
)
