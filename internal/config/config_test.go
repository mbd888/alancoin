package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test helper to set env vars and clean up after
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	old := os.Getenv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if old == "" {
			os.Unsetenv(key)
		} else {
			os.Setenv(key, old)
		}
	})
}

func TestLoad_WithValidConfig(t *testing.T) {
	// Set required env vars
	setEnv(t, "PRIVATE_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	setEnv(t, "WALLET_ADDRESS", "0x1234567890123456789012345678901234567890")
	setEnv(t, "PORT", "9090")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "9090", cfg.Port)
	assert.Equal(t, DefaultRPCURL, cfg.RPCURL)
	assert.Equal(t, int64(DefaultChainID), cfg.ChainID)
	assert.Equal(t, DefaultUSDCContract, cfg.USDCContract)
}

func TestLoad_MissingPrivateKey(t *testing.T) {
	// Clear private key
	setEnv(t, "PRIVATE_KEY", "")

	_, err := Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "PRIVATE_KEY is required")
}

func TestLoad_InvalidPrivateKeyLength(t *testing.T) {
	setEnv(t, "PRIVATE_KEY", "tooshort")

	_, err := Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "64 hex characters")
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name: "valid config",
			config: Config{
				PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				RPCURL:     "https://sepolia.base.org",
			},
			wantErr: "",
		},
		{
			name: "missing private key",
			config: Config{
				PrivateKey: "",
				RPCURL:     "https://sepolia.base.org",
			},
			wantErr: "PRIVATE_KEY is required",
		},
		{
			name: "invalid private key length",
			config: Config{
				PrivateKey: "abc123",
				RPCURL:     "https://sepolia.base.org",
			},
			wantErr: "64 hex characters",
		},
		{
			name: "missing RPC URL",
			config: Config{
				PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				RPCURL:     "",
			},
			wantErr: "RPC_URL is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestConfig_IsDevelopment(t *testing.T) {
	cfg := &Config{Env: "development"}
	assert.True(t, cfg.IsDevelopment())
	assert.False(t, cfg.IsProduction())

	cfg.Env = "production"
	assert.False(t, cfg.IsDevelopment())
	assert.True(t, cfg.IsProduction())
}

func TestGetEnv(t *testing.T) {
	setEnv(t, "TEST_VAR", "custom_value")

	assert.Equal(t, "custom_value", getEnv("TEST_VAR", "default"))
	assert.Equal(t, "default", getEnv("NONEXISTENT_VAR", "default"))
}

func TestGetEnvInt64(t *testing.T) {
	setEnv(t, "TEST_INT", "42")
	setEnv(t, "TEST_INVALID", "not_a_number")

	assert.Equal(t, int64(42), getEnvInt64("TEST_INT", 0))
	assert.Equal(t, int64(99), getEnvInt64("NONEXISTENT_VAR", 99))
	assert.Equal(t, int64(99), getEnvInt64("TEST_INVALID", 99)) // Falls back on parse error
}
