package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test helper to set env vars and clean up after
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
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
				PrivateKey:         "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				RPCURL:             "https://sepolia.base.org",
				Port:               "8080",
				RateLimitRPM:       100,
				DBStatementTimeout: 30000,
				HTTPWriteTimeout:   DefaultHTTPWriteTimeout,
				RequestTimeout:     DefaultRequestTimeout,
			},
			wantErr: "",
		},
		{
			name: "missing private key",
			config: Config{
				PrivateKey:         "",
				RPCURL:             "https://sepolia.base.org",
				Port:               "8080",
				RateLimitRPM:       100,
				DBStatementTimeout: 30000,
			},
			wantErr: "PRIVATE_KEY is required",
		},
		{
			name: "invalid private key length",
			config: Config{
				PrivateKey:         "abc123",
				RPCURL:             "https://sepolia.base.org",
				Port:               "8080",
				RateLimitRPM:       100,
				DBStatementTimeout: 30000,
			},
			wantErr: "64 hex characters",
		},
		{
			name: "missing RPC URL",
			config: Config{
				PrivateKey:         "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				RPCURL:             "",
				Port:               "8080",
				RateLimitRPM:       100,
				DBStatementTimeout: 30000,
			},
			wantErr: "RPC_URL is required",
		},
		{
			name: "invalid port",
			config: Config{
				PrivateKey:         "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				RPCURL:             "https://sepolia.base.org",
				Port:               "99999",
				RateLimitRPM:       100,
				DBStatementTimeout: 30000,
			},
			wantErr: "PORT must be a number between 1 and 65535",
		},
		{
			name: "rate limit too low",
			config: Config{
				PrivateKey:         "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				RPCURL:             "https://sepolia.base.org",
				Port:               "8080",
				RateLimitRPM:       0,
				DBStatementTimeout: 30000,
			},
			wantErr: "RATE_LIMIT_RPM must be at least 1",
		},
		{
			name: "statement timeout too low",
			config: Config{
				PrivateKey:         "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				RPCURL:             "https://sepolia.base.org",
				Port:               "8080",
				RateLimitRPM:       100,
				DBStatementTimeout: 500,
			},
			wantErr: "POSTGRES_STATEMENT_TIMEOUT must be at least 1000ms",
		},
		{
			name: "write timeout less than request timeout",
			config: Config{
				PrivateKey:         "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				RPCURL:             "https://sepolia.base.org",
				Port:               "8080",
				RateLimitRPM:       100,
				DBStatementTimeout: 30000,
				HTTPWriteTimeout:   10 * time.Second,
				RequestTimeout:     30 * time.Second,
			},
			wantErr: "HTTP_WRITE_TIMEOUT",
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

func TestConfig_Validate_ProductionPlatformAddress(t *testing.T) {
	base := Config{
		PrivateKey:         "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		RPCURL:             "https://sepolia.base.org",
		Port:               "8080",
		RateLimitRPM:       100,
		DBStatementTimeout: 30000,
		Env:                "production",
		AdminSecret:        "secret",
	}

	// Sentinel address rejected in production
	cfg := base
	cfg.PlatformAddress = "0x0000000000000000000000000000000000000001"
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "PLATFORM_ADDRESS must be set to a real address")

	// Real address accepted in production
	cfg.PlatformAddress = "0x1234567890abcdef1234567890abcdef12345678"
	err = cfg.Validate()
	assert.NoError(t, err)

	// Sentinel address accepted in development (default env)
	cfg.Env = "development"
	cfg.PlatformAddress = "0x0000000000000000000000000000000000000001"
	err = cfg.Validate()
	assert.NoError(t, err)
}

func TestConfig_OperationalDefaults(t *testing.T) {
	setEnv(t, "PRIVATE_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, DefaultCircuitBreakerThreshold, cfg.CircuitBreakerThreshold)
	assert.Equal(t, DefaultCircuitBreakerDuration, cfg.CircuitBreakerDuration)
	assert.Equal(t, DefaultDrainDeadline, cfg.DrainDeadline)
	assert.Equal(t, DefaultEventBusBufferSize, cfg.EventBusBufferSize)
	assert.Equal(t, DefaultRateLimitBurst, cfg.RateLimitBurst)
}

func TestConfig_OperationalOverrides(t *testing.T) {
	setEnv(t, "PRIVATE_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	setEnv(t, "CB_THRESHOLD", "10")
	setEnv(t, "CB_DURATION", "1m")
	setEnv(t, "DRAIN_DEADLINE", "30s")
	setEnv(t, "EVENT_BUS_BUFFER_SIZE", "50000")
	setEnv(t, "RATE_LIMIT_BURST", "20")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 10, cfg.CircuitBreakerThreshold)
	assert.Equal(t, time.Minute, cfg.CircuitBreakerDuration)
	assert.Equal(t, 30*time.Second, cfg.DrainDeadline)
	assert.Equal(t, 50000, cfg.EventBusBufferSize)
	assert.Equal(t, 20, cfg.RateLimitBurst)
}
