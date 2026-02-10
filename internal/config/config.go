// Package config handles application configuration from environment variables
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	// Server settings
	Port     string
	Env      string // "development", "staging", "production"
	LogLevel string

	// Database
	DatabaseURL string // PostgreSQL connection string (optional, uses in-memory if not set)

	// Blockchain settings
	RPCURL        string
	ChainID       int64
	PrivateKey    string // Hex-encoded, no 0x prefix
	WalletAddress string
	USDCContract  string
	PaymasterURL  string // Circle Paymaster (future)

	// Payment settings
	DefaultPrice string // Default price in USDC (e.g., "0.001")
	MinPayment   string
	MaxPayment   string

	// Security
	APIKeyHash    string // For authenticating SDK clients
	WebhookSecret string
	RateLimitRPS  int

	// Reputation API
	ReputationHMACSecret string // HMAC secret for signing reputation responses (optional)
	AdminSecret          string // Admin API secret
}

// Base Sepolia defaults
const (
	DefaultRPCURL       = "https://sepolia.base.org"
	DefaultChainID      = 84532                                        // Base Sepolia
	DefaultUSDCContract = "0x036CbD53842c5426634e7929541eC2318f3dCF7e" // Base Sepolia USDC
	DefaultPort         = "8080"
	DefaultEnv          = "development"
	DefaultLogLevel     = "info"
	DefaultPrice        = "0.001"
	DefaultRateLimit    = 100
)

// Load reads configuration from environment variables
// It loads .env file if present (for local development)
func Load() (*Config, error) {
	// Load .env file if it exists (ignore error if not present)
	_ = godotenv.Load()

	cfg := &Config{
		Port:                 getEnv("PORT", DefaultPort),
		Env:                  getEnv("ENV", DefaultEnv),
		LogLevel:             getEnv("LOG_LEVEL", DefaultLogLevel),
		DatabaseURL:          os.Getenv("DATABASE_URL"), // Optional, uses in-memory if not set
		RPCURL:               getEnv("RPC_URL", DefaultRPCURL),
		ChainID:              getEnvInt64("CHAIN_ID", DefaultChainID),
		PrivateKey:           os.Getenv("PRIVATE_KEY"), // Required, no default
		WalletAddress:        os.Getenv("WALLET_ADDRESS"),
		USDCContract:         getEnv("USDC_CONTRACT", DefaultUSDCContract),
		PaymasterURL:         os.Getenv("PAYMASTER_URL"),
		DefaultPrice:         getEnv("DEFAULT_PRICE", DefaultPrice),
		MinPayment:           getEnv("MIN_PAYMENT", "0.0001"),
		MaxPayment:           getEnv("MAX_PAYMENT", "1000"),
		APIKeyHash:           os.Getenv("API_KEY_HASH"),
		WebhookSecret:        os.Getenv("WEBHOOK_SECRET"),
		RateLimitRPS:         int(getEnvInt64("RATE_LIMIT_RPS", int64(DefaultRateLimit))),
		ReputationHMACSecret: os.Getenv("REPUTATION_HMAC_SECRET"),
		AdminSecret:          os.Getenv("ADMIN_SECRET"),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that all required configuration is present
func (c *Config) Validate() error {
	if c.PrivateKey == "" {
		return fmt.Errorf("PRIVATE_KEY is required")
	}

	// Allow both with and without 0x prefix
	key := c.PrivateKey
	if len(key) == 66 && key[:2] == "0x" {
		key = key[2:]
	}
	if len(key) != 64 {
		return fmt.Errorf("PRIVATE_KEY must be 64 hex characters (with or without 0x prefix)")
	}

	if c.RPCURL == "" {
		return fmt.Errorf("RPC_URL is required")
	}

	return nil
}

// IsDevelopment returns true if running in development mode
func (c *Config) IsDevelopment() bool {
	return c.Env == "development"
}

// IsProduction returns true if running in production mode
func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

// Helper functions

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
	}
	return defaultValue
}
