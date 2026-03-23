// Package config handles application configuration from environment variables
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

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

	// Redis (optional — enables distributed rate limiting, idempotency, circuit breaker)
	RedisURL string // Redis connection URL (e.g. "redis://localhost:6379"), empty = in-memory fallback

	// Blockchain settings
	RPCURL        string
	ChainID       int64
	PrivateKey    string `json:"-"` // Hex-encoded, no 0x prefix — excluded from serialization
	WalletAddress string
	USDCContract  string
	PaymasterURL  string // Circle Paymaster (future)

	// Deposit watcher
	DepositWatcherEnabled bool   // Enable on-chain deposit watcher (requires RPC_URL)
	DepositWatcherStart   uint64 // Start block for deposit scanning (0 = latest)

	// Payment settings
	DefaultPrice string // Default price in USDC (e.g., "0.001")
	MinPayment   string
	MaxPayment   string

	// Security
	APIKeyHash    string // For authenticating SDK clients
	WebhookSecret string
	RateLimitRPM  int

	// Reputation API
	ReputationHMACSecret string // HMAC secret for signing reputation responses (optional)
	AdminSecret          string // Admin API secret

	// Platform fee
	PlatformAddress string // Ledger address for collecting basis-point fees (from PLATFORM_ADDRESS env var)

	// Session key mode
	SessionKeyMode string // "demo" (ledger-only, default) or "production" (on-chain transfers)

	// Receipt signing
	ReceiptHMACSecret string // HMAC secret for signing payment receipts (optional)

	// Gateway proxy settings
	AllowLocalEndpoints bool   // Allow localhost/private endpoints (for demos and local development)
	CORSAllowedOrigins  string // Comma-separated allowed CORS origins (empty = allow all in dev, reject in production)

	// Database pool settings
	DBMaxOpenConns     int
	DBMaxIdleConns     int
	DBConnMaxLifetime  time.Duration
	DBConnMaxIdleTime  time.Duration
	DBConnectTimeout   int // seconds, appended to Postgres DSN
	DBStatementTimeout int // milliseconds, appended to Postgres DSN

	// HTTP server timeouts
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration
	RequestTimeout   time.Duration // global handler execution timeout

	// Observability
	OTLPEndpoint string // OpenTelemetry collector endpoint (e.g. "localhost:4317"), empty = disabled

	// Operational tuning
	CircuitBreakerThreshold int           // Failure count before circuit opens
	CircuitBreakerDuration  time.Duration // How long circuit stays open
	DrainDeadline           time.Duration // Max time to wait for in-flight requests during shutdown
	EventBusBufferSize      int           // Event bus channel buffer size
	EventBusBackend         string        // "memory" (default) or "kafka"
	RateLimitBurst          int           // Token bucket burst size for rate limiter

	// Kafka (only used when EventBusBackend=kafka)
	KafkaBrokers       []string // Broker addresses
	KafkaConsumerGroup string   // Consumer group prefix
	KafkaClientID      string   // Client ID for this node
	KafkaTopicPrefix   string   // Topic name prefix
	KafkaTLSEnabled    bool
	KafkaTLSCert       string
	KafkaTLSKey        string
	KafkaTLSCA         string
	KafkaSASLEnabled   bool
	KafkaSASLMechanism string // "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"
	KafkaSASLUsername  string
	KafkaSASLPassword  string

	// CDC (Change Data Capture)
	CDCEnabled bool // Enable ledger CDC watcher

	// Cleanup retention
	WALRetention    time.Duration // How long to keep processed WAL entries (default 24h)
	OutboxRetention time.Duration // How long to keep published outbox entries (default 24h)

	// Stripe billing
	StripeSecretKey         string // Stripe secret key (sk_test_... or sk_live_...)
	StripeWebhookSecret     string // Stripe webhook signing secret (whsec_...)
	StripePriceStarterID    string // Stripe Price ID for Starter plan
	StripePriceGrowthID     string // Stripe Price ID for Growth plan
	StripePriceEnterpriseID string // Stripe Price ID for Enterprise plan
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

	// Database pool defaults
	DefaultDBMaxOpenConns     = 50
	DefaultDBMaxIdleConns     = 10
	DefaultDBConnMaxLifetime  = 5 * time.Minute
	DefaultDBConnMaxIdleTime  = 3 * time.Minute
	DefaultDBConnectTimeout   = 5     // seconds
	DefaultDBStatementTimeout = 30000 // milliseconds (30s)

	// HTTP server timeout defaults
	DefaultHTTPReadTimeout  = 10 * time.Second
	DefaultHTTPWriteTimeout = 30 * time.Second
	DefaultHTTPIdleTimeout  = 60 * time.Second
	DefaultRequestTimeout   = 30 * time.Second

	// Operational defaults
	DefaultCircuitBreakerThreshold = 5
	DefaultCircuitBreakerDuration  = 30 * time.Second
	DefaultDrainDeadline           = 15 * time.Second
	DefaultEventBusBufferSize      = 10000
	DefaultRateLimitBurst          = 10
)

// Load reads configuration from environment variables
// It loads .env file if present (for local development)
func Load() (*Config, error) {
	// Load .env file if it exists (ignore error if not present)
	_ = godotenv.Load()

	cfg := &Config{
		Port:          getEnv("PORT", DefaultPort),
		Env:           getEnv("ENV", DefaultEnv),
		LogLevel:      getEnv("LOG_LEVEL", DefaultLogLevel),
		DatabaseURL:   os.Getenv("DATABASE_URL"), // Optional, uses in-memory if not set
		RedisURL:      os.Getenv("REDIS_URL"),    // Optional, enables distributed state
		RPCURL:        getEnv("RPC_URL", DefaultRPCURL),
		ChainID:       getEnvInt64("CHAIN_ID", DefaultChainID),
		PrivateKey:    os.Getenv("PRIVATE_KEY"), // Required, no default
		WalletAddress: os.Getenv("WALLET_ADDRESS"),
		USDCContract:  getEnv("USDC_CONTRACT", DefaultUSDCContract),
		PaymasterURL:  os.Getenv("PAYMASTER_URL"),
		DefaultPrice:  getEnv("DEFAULT_PRICE", DefaultPrice),
		MinPayment:    getEnv("MIN_PAYMENT", "0.0001"),
		MaxPayment:    getEnv("MAX_PAYMENT", "1000"),
		APIKeyHash:    os.Getenv("API_KEY_HASH"),
		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		RateLimitRPM: func() int {
			rpm := getEnvInt64("RATE_LIMIT_RPM", 0)
			if rpm == 0 {
				rpm = getEnvInt64("RATE_LIMIT_RPS", int64(DefaultRateLimit))
			}
			return int(rpm)
		}(),
		PlatformAddress:       getEnv("PLATFORM_ADDRESS", "0x0000000000000000000000000000000000000001"),
		ReputationHMACSecret:  os.Getenv("REPUTATION_HMAC_SECRET"),
		AdminSecret:           os.Getenv("ADMIN_SECRET"),
		DepositWatcherEnabled: os.Getenv("DEPOSIT_WATCHER_ENABLED") == "true",
		DepositWatcherStart:   getEnvUint64("DEPOSIT_WATCHER_START_BLOCK", 0),
		SessionKeyMode:        getEnv("SESSION_KEY_MODE", "demo"),
		ReceiptHMACSecret:     os.Getenv("RECEIPT_HMAC_SECRET"),

		DBMaxOpenConns:     int(getEnvInt64("POSTGRES_MAX_OPEN_CONNS", int64(DefaultDBMaxOpenConns))),
		DBMaxIdleConns:     int(getEnvInt64("POSTGRES_MAX_IDLE_CONNS", int64(DefaultDBMaxIdleConns))),
		DBConnMaxLifetime:  getEnvDuration("POSTGRES_CONN_MAX_LIFETIME", DefaultDBConnMaxLifetime),
		DBConnMaxIdleTime:  getEnvDuration("POSTGRES_CONN_MAX_IDLE_TIME", DefaultDBConnMaxIdleTime),
		DBConnectTimeout:   int(getEnvInt64("POSTGRES_CONNECT_TIMEOUT", int64(DefaultDBConnectTimeout))),
		DBStatementTimeout: int(getEnvInt64("POSTGRES_STATEMENT_TIMEOUT", int64(DefaultDBStatementTimeout))),

		HTTPReadTimeout:  getEnvDuration("HTTP_READ_TIMEOUT", DefaultHTTPReadTimeout),
		HTTPWriteTimeout: getEnvDuration("HTTP_WRITE_TIMEOUT", DefaultHTTPWriteTimeout),
		HTTPIdleTimeout:  getEnvDuration("HTTP_IDLE_TIMEOUT", DefaultHTTPIdleTimeout),
		RequestTimeout:   getEnvDuration("REQUEST_TIMEOUT", DefaultRequestTimeout),

		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),

		AllowLocalEndpoints: os.Getenv("ALLOW_LOCAL_ENDPOINTS") == "true",
		CORSAllowedOrigins:  os.Getenv("CORS_ALLOWED_ORIGINS"),

		CircuitBreakerThreshold: int(getEnvInt64("CB_THRESHOLD", int64(DefaultCircuitBreakerThreshold))),
		CircuitBreakerDuration:  getEnvDuration("CB_DURATION", DefaultCircuitBreakerDuration),
		DrainDeadline:           getEnvDuration("DRAIN_DEADLINE", DefaultDrainDeadline),
		EventBusBufferSize:      int(getEnvInt64("EVENT_BUS_BUFFER_SIZE", int64(DefaultEventBusBufferSize))),
		EventBusBackend:         getEnv("EVENTBUS_BACKEND", "memory"),
		RateLimitBurst:          int(getEnvInt64("RATE_LIMIT_BURST", int64(DefaultRateLimitBurst))),

		KafkaBrokers:       parseCSV(os.Getenv("KAFKA_BROKERS")),
		KafkaConsumerGroup: getEnv("KAFKA_CONSUMER_GROUP", "alancoin"),
		KafkaClientID:      getEnv("KAFKA_CLIENT_ID", "alancoin-1"),
		KafkaTopicPrefix:   getEnv("KAFKA_TOPIC_PREFIX", "alancoin."),
		KafkaTLSEnabled:    os.Getenv("KAFKA_TLS_ENABLED") == "true",
		KafkaTLSCert:       os.Getenv("KAFKA_TLS_CERT"),
		KafkaTLSKey:        os.Getenv("KAFKA_TLS_KEY"),
		KafkaTLSCA:         os.Getenv("KAFKA_TLS_CA"),
		KafkaSASLEnabled:   os.Getenv("KAFKA_SASL_ENABLED") == "true",
		KafkaSASLMechanism: getEnv("KAFKA_SASL_MECHANISM", "PLAIN"),
		KafkaSASLUsername:  os.Getenv("KAFKA_SASL_USERNAME"),
		KafkaSASLPassword:  os.Getenv("KAFKA_SASL_PASSWORD"),

		CDCEnabled: os.Getenv("CDC_ENABLED") == "true",

		WALRetention:    getEnvDuration("WAL_RETENTION", 24*time.Hour),
		OutboxRetention: getEnvDuration("OUTBOX_RETENTION", 24*time.Hour),

		StripeSecretKey:         os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret:     os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripePriceStarterID:    os.Getenv("STRIPE_PRICE_STARTER"),
		StripePriceGrowthID:     os.Getenv("STRIPE_PRICE_GROWTH"),
		StripePriceEnterpriseID: os.Getenv("STRIPE_PRICE_ENTERPRISE"),
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

	// Port range
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("PORT must be a number between 1 and 65535, got %q", c.Port)
	}

	// Rate limit sanity
	if c.RateLimitRPM < 1 {
		return fmt.Errorf("RATE_LIMIT_RPM must be at least 1, got %d", c.RateLimitRPM)
	}

	// DB statement timeout sanity
	if c.DBStatementTimeout < 1000 {
		return fmt.Errorf("POSTGRES_STATEMENT_TIMEOUT must be at least 1000ms, got %d", c.DBStatementTimeout)
	}

	// Write timeout must exceed request timeout to avoid truncated responses
	if c.HTTPWriteTimeout > 0 && c.RequestTimeout > 0 && c.HTTPWriteTimeout < c.RequestTimeout {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT (%v) must be >= REQUEST_TIMEOUT (%v)", c.HTTPWriteTimeout, c.RequestTimeout)
	}

	// Warnings (non-fatal)
	if c.IsProduction() && c.AdminSecret == "" {
		slog.Warn("ADMIN_SECRET not set — admin endpoints accept any authenticated request")
	}

	// Reject DEMO_MODE in production — prevents accidental admin access bypass.
	if c.IsProduction() && os.Getenv("DEMO_MODE") == "true" {
		return fmt.Errorf("DEMO_MODE=true is not allowed when ENV=production")
	}

	// Reject ALLOW_LOCAL_ENDPOINTS in production — prevents SSRF to internal services.
	if c.IsProduction() && c.AllowLocalEndpoints {
		return fmt.Errorf("ALLOW_LOCAL_ENDPOINTS=true is not allowed when ENV=production")
	}

	// Reject sentinel platform address in production — must be a real address.
	if c.IsProduction() && c.PlatformAddress == "0x0000000000000000000000000000000000000001" {
		return fmt.Errorf("PLATFORM_ADDRESS must be set to a real address in production")
	}

	// Event bus backend validation
	if c.EventBusBackend != "" && c.EventBusBackend != "memory" && c.EventBusBackend != "kafka" {
		return fmt.Errorf("EVENTBUS_BACKEND must be \"memory\" or \"kafka\", got %q", c.EventBusBackend)
	}
	if c.EventBusBackend == "kafka" && len(c.KafkaBrokers) == 0 {
		return fmt.Errorf("KAFKA_BROKERS is required when EVENTBUS_BACKEND=kafka")
	}

	// Warn if production database connection doesn't use SSL
	if c.IsProduction() && c.DatabaseURL != "" {
		if !strings.Contains(c.DatabaseURL, "sslmode=require") &&
			!strings.Contains(c.DatabaseURL, "sslmode=verify-full") &&
			!strings.Contains(c.DatabaseURL, "sslmode=verify-ca") {
			slog.Warn("DATABASE_URL does not enforce SSL (sslmode=require recommended in production)")
		}
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

func getEnvUint64(key string, defaultValue uint64) uint64 {
	if value := os.Getenv(key); value != "" {
		if u, err := strconv.ParseUint(value, 10, 64); err == nil {
			return u
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}

func parseCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
