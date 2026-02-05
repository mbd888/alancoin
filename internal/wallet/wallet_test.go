package wallet

import (
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatUSDC(t *testing.T) {
	tests := []struct {
		name   string
		amount *big.Int
		want   string
	}{
		{
			name:   "nil amount",
			amount: nil,
			want:   "0",
		},
		{
			name:   "zero",
			amount: big.NewInt(0),
			want:   "0",
		},
		{
			name:   "one dollar",
			amount: big.NewInt(1_000_000),
			want:   "1",
		},
		{
			name:   "one cent",
			amount: big.NewInt(10_000),
			want:   "0.010000",
		},
		{
			name:   "one tenth of a cent",
			amount: big.NewInt(1_000),
			want:   "0.001000",
		},
		{
			name:   "smallest unit",
			amount: big.NewInt(1),
			want:   "0.000001",
		},
		{
			name:   "dollar fifty",
			amount: big.NewInt(1_500_000),
			want:   "1.500000",
		},
		{
			name:   "large amount",
			amount: big.NewInt(1_234_567_890),
			want:   "1234.567890",
		},
		{
			name:   "typical micropayment",
			amount: big.NewInt(1_000), // $0.001
			want:   "0.001000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatUSDC(tt.amount)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseUSDC(t *testing.T) {
	tests := []struct {
		name    string
		amount  string
		want    *big.Int
		wantErr bool
	}{
		{
			name:   "one dollar",
			amount: "1",
			want:   big.NewInt(1_000_000),
		},
		{
			name:   "one dollar with decimal",
			amount: "1.0",
			want:   big.NewInt(1_000_000),
		},
		{
			name:   "one dollar fifty",
			amount: "1.50",
			want:   big.NewInt(1_500_000),
		},
		{
			name:   "one cent",
			amount: "0.01",
			want:   big.NewInt(10_000),
		},
		{
			name:   "micropayment",
			amount: "0.001",
			want:   big.NewInt(1_000),
		},
		{
			name:   "smallest unit",
			amount: "0.000001",
			want:   big.NewInt(1),
		},
		{
			name:   "large amount",
			amount: "1234.567890",
			want:   big.NewInt(1_234_567_890),
		},
		{
			name:   "truncates extra decimals",
			amount: "1.1234567890",
			want:   big.NewInt(1_123_456), // Truncated to 6 decimals
		},
		{
			name:    "empty string",
			amount:  "",
			wantErr: true,
		},
		{
			name:    "invalid number",
			amount:  "abc",
			wantErr: true,
		},
		{
			name:    "multiple decimal points",
			amount:  "1.2.3",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUSDC(tt.amount)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 0, tt.want.Cmp(got), "expected %s, got %s", tt.want.String(), got.String())
		})
	}
}

func TestParseAndFormat_Roundtrip(t *testing.T) {
	amounts := []string{
		"0",
		"1",
		"1.500000",
		"0.001000",
		"1234.567890",
	}

	for _, amount := range amounts {
		t.Run(amount, func(t *testing.T) {
			parsed, err := ParseUSDC(amount)
			require.NoError(t, err)

			formatted := FormatUSDC(parsed)
			if amount == "0" {
				assert.Equal(t, "0", formatted)
			} else {
				assert.Equal(t, amount, formatted)
			}
		})
	}
}

func TestTransferError(t *testing.T) {
	tests := []struct {
		name     string
		err      *TransferError
		contains string
	}{
		{
			name: "with tx hash",
			err: &TransferError{
				Op:     "send",
				TxHash: "0xabc123",
				Err:    errors.New("network error"),
			},
			contains: "0xabc123",
		},
		{
			name: "without tx hash",
			err: &TransferError{
				Op:  "nonce",
				Err: errors.New("failed to get nonce"),
			},
			contains: "nonce failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, tt.err.Error(), tt.contains)
			assert.True(t, errors.Is(tt.err, tt.err.Err))
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				RPCURL:       "https://sepolia.base.org",
				PrivateKey:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				ChainID:      84532,
				USDCContract: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			wantErr: false,
		},
		{
			name: "valid config with 0x prefix",
			cfg: Config{
				RPCURL:       "https://sepolia.base.org",
				PrivateKey:   "0x0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				ChainID:      84532,
				USDCContract: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			wantErr: false,
		},
		{
			name: "missing RPC URL",
			cfg: Config{
				PrivateKey:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				ChainID:      84532,
				USDCContract: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			wantErr: true,
		},
		{
			name: "missing private key",
			cfg: Config{
				RPCURL:       "https://sepolia.base.org",
				ChainID:      84532,
				USDCContract: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			wantErr: true,
		},
		{
			name: "invalid private key length",
			cfg: Config{
				RPCURL:       "https://sepolia.base.org",
				PrivateKey:   "tooshort",
				ChainID:      84532,
				USDCContract: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			wantErr: true,
		},
		{
			name: "missing chain ID",
			cfg: Config{
				RPCURL:       "https://sepolia.base.org",
				PrivateKey:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				USDCContract: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Integration tests - only run with -short=false

func TestWallet_Integration_Balance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	// Requires real testnet credentials
}

func TestWallet_Integration_Transfer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	// Requires real testnet credentials and USDC
}
