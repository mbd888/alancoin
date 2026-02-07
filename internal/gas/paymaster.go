package gas

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// PlatformPaymaster sponsors gas using the platform's ETH balance.
// Agents are charged USDC equivalent of gas costs.
//
// Flow:
// 1. Agent initiates USDC transfer
// 2. We estimate gas cost in ETH
// 3. Convert ETH cost to USDC (with markup) using live price oracle
// 4. Execute transfer, we pay ETH gas
// 5. Agent is charged: transfer amount + gas fee in USDC
type PlatformPaymaster struct {
	client        *ethclient.Client
	config        PaymasterConfig
	walletAddress common.Address
	oracle        *PriceOracle

	// Rate limiting
	mu           sync.Mutex
	dailySpent   *big.Int // ETH spent today
	lastResetDay string   // YYYY-MM-DD
}

// NewPlatformPaymaster creates a new platform paymaster
// walletAddress is the platform wallet that sponsors gas
func NewPlatformPaymaster(rpcURL string, walletAddress string, cfg PaymasterConfig) (*PlatformPaymaster, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %w", err)
	}

	var addr common.Address
	if walletAddress != "" {
		addr = common.HexToAddress(walletAddress)
	}

	// Initialize price oracle: 60s cache, falls back to config value
	oracle := NewPriceOracle(cfg.ETHPriceUSD, 60*time.Second)

	return &PlatformPaymaster{
		client:        client,
		config:        cfg,
		walletAddress: addr,
		oracle:        oracle,
		dailySpent:    big.NewInt(0),
	}, nil
}

// EstimateGasFee estimates the gas cost in USDC for a transaction
func (p *PlatformPaymaster) EstimateGasFee(ctx context.Context, req *EstimateRequest) (*GasEstimate, error) {
	// Get current gas price
	gasPrice, err := p.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	// Check against max
	maxGasPrice := new(big.Int).Mul(big.NewInt(int64(p.config.MaxGasPrice)), big.NewInt(1e9)) // gwei to wei
	if gasPrice.Cmp(maxGasPrice) > 0 {
		return nil, ErrGasPriceTooHigh
	}

	// Estimate gas limit
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		// Default for ERC20 transfer
		gasLimit = 65000

		// Try to estimate if we have addresses
		if req.From != "" && req.To != "" {
			estimated, err := p.client.EstimateGas(ctx, ethereum.CallMsg{
				From: common.HexToAddress(req.From),
				To:   ptrAddr(common.HexToAddress(req.To)),
			})
			if err == nil && estimated > 0 {
				gasLimit = estimated
			}
		}
	}

	// Calculate ETH cost
	gasCostWei := new(big.Int).Mul(gasPrice, big.NewInt(int64(gasLimit)))
	gasCostETH := weiToETH(gasCostWei)

	// Convert to USDC with markup using live oracle price
	ethPrice := p.oracle.GetETHPrice(ctx)
	gasCostUSD := gasCostETH * ethPrice
	gasCostUSD *= (1 + p.config.GasMarkupPct) // Add markup

	// Apply min/max
	minFee := parseUSDC(p.config.MinGasFeeUSDC)
	maxFee := parseUSDC(p.config.MaxGasFeeUSDC)
	gasCostUSDCBig := usdToBigUSDC(gasCostUSD)

	if gasCostUSDCBig.Cmp(minFee) < 0 {
		gasCostUSDCBig = minFee
	}
	if gasCostUSDCBig.Cmp(maxFee) > 0 {
		gasCostUSDCBig = maxFee
	}

	// Calculate total
	originalAmount := parseUSDC(req.Amount)
	totalWithGas := new(big.Int).Add(originalAmount, gasCostUSDCBig)

	return &GasEstimate{
		GasLimit:     gasLimit,
		GasPriceWei:  gasPrice.String(),
		GasCostETH:   fmt.Sprintf("%.8f", gasCostETH),
		GasCostUSDC:  formatUSDC(gasCostUSDCBig),
		ETHPriceUSD:  fmt.Sprintf("%.2f", ethPrice),
		TotalWithGas: formatUSDC(totalWithGas),
		ValidUntil:   time.Now().Add(30 * time.Second), // Estimate valid for 30s
	}, nil
}

// SponsorTransaction sponsors gas for a transaction
// In this implementation, we don't actually execute - we prepare the sponsorship
// The actual execution happens in the wallet layer
func (p *PlatformPaymaster) SponsorTransaction(ctx context.Context, req *SponsorRequest) (*SponsorResult, error) {
	// First, estimate the gas
	estimate, err := p.EstimateGasFee(ctx, &EstimateRequest{
		From:     req.From,
		To:       req.To,
		Amount:   req.Amount,
		GasLimit: req.GasLimit,
	})
	if err != nil {
		return nil, err
	}

	// Check daily limit
	if err := p.checkDailyLimit(estimate.GasCostETH); err != nil {
		return nil, err
	}

	// Calculate total to charge
	originalAmount := parseUSDC(req.Amount)
	gasFee := parseUSDC(estimate.GasCostUSDC)
	totalCharged := new(big.Int).Add(originalAmount, gasFee)

	// Record spending (optimistic - actual spend tracked separately)
	p.recordSpending(estimate.GasCostETH)

	return &SponsorResult{
		From:         req.From,
		To:           req.To,
		Amount:       req.Amount,
		GasFeeUSDC:   estimate.GasCostUSDC,
		TotalCharged: formatUSDC(totalCharged),
		GasPriceWei:  estimate.GasPriceWei,
		GasCostETH:   estimate.GasCostETH,
		Status:       "ready", // Ready for execution
		Timestamp:    time.Now(),
	}, nil
}

// GetBalance returns the paymaster's ETH balance
func (p *PlatformPaymaster) GetBalance(ctx context.Context) (*big.Int, error) {
	if p.walletAddress == (common.Address{}) {
		return big.NewInt(0), fmt.Errorf("wallet address not configured")
	}
	return p.client.BalanceAt(ctx, p.walletAddress, nil)
}

// checkDailyLimit checks if we've exceeded daily gas spending
func (p *PlatformPaymaster) checkDailyLimit(gasCostETH string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Reset if new day
	today := time.Now().Format("2006-01-02")
	if p.lastResetDay != today {
		p.dailySpent = big.NewInt(0)
		p.lastResetDay = today
	}

	// Parse limit and cost
	limit := parseETH(p.config.DailyGasLimit)
	cost := parseETH(gasCostETH)

	// Check
	newTotal := new(big.Int).Add(p.dailySpent, cost)
	if newTotal.Cmp(limit) > 0 {
		return ErrDailyLimitExceeded
	}

	return nil
}

// recordSpending records gas spending
func (p *PlatformPaymaster) recordSpending(gasCostETH string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	cost := parseETH(gasCostETH)
	p.dailySpent = new(big.Int).Add(p.dailySpent, cost)
}

// GetDailySpending returns today's gas spending
func (p *PlatformPaymaster) GetDailySpending() (spent string, limit string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return formatETH(p.dailySpent), p.config.DailyGasLimit
}

// Helper functions

func ptrAddr(addr common.Address) *common.Address {
	return &addr
}

func weiToETH(wei *big.Int) float64 {
	eth := new(big.Float).SetInt(wei)
	eth.Quo(eth, big.NewFloat(1e18))
	f, _ := eth.Float64()
	return f
}

func parseETH(s string) *big.Int {
	f, _ := new(big.Float).SetString(s)
	if f == nil {
		return big.NewInt(0)
	}
	f.Mul(f, big.NewFloat(1e18))
	result, _ := f.Int(nil)
	return result
}

func formatETH(wei *big.Int) string {
	return fmt.Sprintf("%.8f", weiToETH(wei))
}

func parseUSDC(s string) *big.Int {
	if s == "" {
		return big.NewInt(0)
	}
	f, _ := new(big.Float).SetString(s)
	if f == nil {
		return big.NewInt(0)
	}
	f.Mul(f, big.NewFloat(1e6)) // USDC has 6 decimals
	result, _ := f.Int(nil)
	return result
}

func formatUSDC(amount *big.Int) string {
	f := new(big.Float).SetInt(amount)
	f.Quo(f, big.NewFloat(1e6))
	result, _ := f.Float64()
	return fmt.Sprintf("%.6f", result)
}

func usdToBigUSDC(usd float64) *big.Int {
	f := big.NewFloat(usd)
	f.Mul(f, big.NewFloat(1e6))
	result, _ := f.Int(nil)
	return result
}
