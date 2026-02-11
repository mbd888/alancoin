// Package server sets up the HTTP server with all routes
package server

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/commentary"
	"github.com/mbd888/alancoin/internal/config"
	"github.com/mbd888/alancoin/internal/contracts"
	"github.com/mbd888/alancoin/internal/credit"
	"github.com/mbd888/alancoin/internal/discovery"
	"github.com/mbd888/alancoin/internal/escrow"
	"github.com/mbd888/alancoin/internal/gas"
	"github.com/mbd888/alancoin/internal/gateway"
	"github.com/mbd888/alancoin/internal/ledger"
	"github.com/mbd888/alancoin/internal/logging"
	"github.com/mbd888/alancoin/internal/metrics"
	"github.com/mbd888/alancoin/internal/negotiation"
	"github.com/mbd888/alancoin/internal/paywall"
	"github.com/mbd888/alancoin/internal/predictions"
	"github.com/mbd888/alancoin/internal/ratelimit"
	"github.com/mbd888/alancoin/internal/realtime"
	"github.com/mbd888/alancoin/internal/registry"
	"github.com/mbd888/alancoin/internal/reputation"
	"github.com/mbd888/alancoin/internal/risk"
	"github.com/mbd888/alancoin/internal/security"
	"github.com/mbd888/alancoin/internal/sessionkeys"
	"github.com/mbd888/alancoin/internal/stakes"
	"github.com/mbd888/alancoin/internal/streams"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/validation"
	"github.com/mbd888/alancoin/internal/verified"
	"github.com/mbd888/alancoin/internal/wallet"
	"github.com/mbd888/alancoin/internal/watcher"
	"github.com/mbd888/alancoin/internal/webhooks"
)

// -----------------------------------------------------------------------------
// Server
// -----------------------------------------------------------------------------

// Server wraps the HTTP server and dependencies
type Server struct {
	cfg                *config.Config
	wallet             wallet.WalletService
	registry           registry.Store
	sessionMgr         *sessionkeys.Manager
	authMgr            *auth.Manager
	ledger             *ledger.Ledger
	commentary         *commentary.Service
	predictions        *predictions.Service
	depositWatcher     *watcher.Watcher
	webhooks           *webhooks.Dispatcher
	realtimeHub        *realtime.Hub
	paymaster          gas.Paymaster
	escrowService      *escrow.Service
	escrowTimer        *escrow.Timer
	creditService      *credit.Service
	creditTimer        *credit.Timer
	contractService    *contracts.Service
	contractTimer      *contracts.Timer
	streamService      *streams.Service
	streamTimer        *streams.Timer
	gatewayService     *gateway.Service
	gatewayTimer       *gateway.Timer
	negotiationService *negotiation.Service
	negotiationTimer   *negotiation.Timer
	stakesService      *stakes.Service
	stakesDistributor  *stakes.Distributor
	reputationStore    reputation.SnapshotStore
	reputationWorker   *reputation.Worker
	reputationSigner   *reputation.Signer
	matviewRefresher   *registry.MatviewRefresher
	partitionMaint     *registry.PartitionMaintainer
	riskEngine         *risk.Engine
	verifiedService    *verified.Service
	verifiedEnforcer   *verified.Enforcer
	rateLimiter        *ratelimit.Limiter
	db                 *sql.DB // nil if using in-memory
	router             *gin.Engine
	httpSrv            *http.Server
	logger             *slog.Logger
	cancelRunCtx       context.CancelFunc // cancels background goroutines started in Run
	tracerShutdown     func(context.Context) error

	// Health state
	ready   atomic.Bool
	healthy atomic.Bool
}

// Option configures the server
type Option func(*Server)

// WithLogger sets a custom logger
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		s.logger = logger
	}
}

// WithWallet sets a custom wallet (for testing)
func WithWallet(w wallet.WalletService) Option {
	return func(s *Server) {
		s.wallet = w
	}
}

// New creates a new server instance
func New(cfg *config.Config, opts ...Option) (*Server, error) {
	s := &Server{
		cfg:    cfg,
		logger: logging.New(cfg.LogLevel, "json"),
	}

	// Apply options first (may set wallet/logger)
	for _, opt := range opts {
		opt(s)
	}

	// Context for initialization
	ctx := context.Background()

	// Initialize distributed tracing (no-op if endpoint not configured)
	tracerShutdown, err := traces.Init(ctx, cfg.OTLPEndpoint, s.logger)
	if err != nil {
		s.logger.Warn("failed to initialize tracing", "error", err)
		tracerShutdown = func(context.Context) error { return nil }
	}
	s.tracerShutdown = tracerShutdown

	// Initialize storage (Postgres if DATABASE_URL set, otherwise in-memory)
	if cfg.DatabaseURL != "" {
		dbDSN := appendDSNParams(cfg.DatabaseURL, cfg.DBConnectTimeout, cfg.DBStatementTimeout)
		db, err := sql.Open("postgres", dbDSN)
		if err != nil {
			return nil, fmt.Errorf("failed to open database: %w", err)
		}

		// Configure connection pool
		db.SetMaxOpenConns(cfg.DBMaxOpenConns)
		db.SetMaxIdleConns(cfg.DBMaxIdleConns)
		db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
		db.SetConnMaxIdleTime(cfg.DBConnMaxIdleTime)

		// Test connection
		if err := db.Ping(); err != nil {
			return nil, fmt.Errorf("failed to connect to database: %w", err)
		}

		s.db = db
		s.registry = registry.NewPostgresStore(db)
		s.logger.Info("using PostgreSQL storage", "url", maskDSN(cfg.DatabaseURL))

		// Session keys with Postgres
		sessionStore := sessionkeys.NewPostgresStore(db)
		policyStore := sessionkeys.NewPolicyPostgresStore(db)
		if err := policyStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate policy store", "error", err)
		}
		s.sessionMgr = sessionkeys.NewManager(sessionStore, nil, policyStore)

		// API keys with Postgres
		authStore := auth.NewPostgresStore(db)
		if err := authStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate auth store", "error", err)
		}
		s.authMgr = auth.NewManager(authStore)

		// Ledger with Postgres
		ledgerStore := ledger.NewPostgresStore(db)
		if err := ledgerStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate ledger store", "error", err)
		}
		eventStore := ledger.NewPostgresEventStore(db)
		auditLogger := ledger.NewPostgresAuditLogger(db)
		alertStore := ledger.NewPostgresAlertStore(db)
		alertChecker := ledger.NewAlertChecker(alertStore)
		s.ledger = ledger.NewWithEvents(ledgerStore, eventStore).
			WithAuditLogger(auditLogger).
			WithAlertChecker(alertChecker)
		s.logger.Info("agent balance tracking enabled")

		// Webhooks with Postgres
		webhookStore := webhooks.NewPostgresStore(db)
		if err := webhookStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate webhook store", "error", err)
		}
		s.webhooks = webhooks.NewDispatcher(webhookStore)
		s.logger.Info("webhooks enabled")

		// Commentary (verbal agents layer)
		commentaryStore := commentary.NewPostgresStore(db)
		if err := commentaryStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate commentary store", "error", err)
		}
		s.commentary = commentary.NewService(commentaryStore)
		s.logger.Info("verbal agents enabled")

		// Predictions (verifiable predictions with reputation stakes)
		predictionsStore := predictions.NewPostgresStore(db)
		if err := predictionsStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate predictions store", "error", err)
		}
		s.predictions = predictions.NewService(predictionsStore, &registryMetricProvider{s.registry})
		s.logger.Info("predictions enabled")

		// Escrow with PostgreSQL store
		escrowStore := escrow.NewPostgresStore(db)
		s.escrowService = escrow.NewService(escrowStore, &escrowLedgerAdapter{s.ledger}).WithLogger(s.logger)
		s.escrowTimer = escrow.NewTimer(s.escrowService, escrowStore, s.logger)
		s.logger.Info("escrow enabled (postgres)")

		// Contracts with PostgreSQL store
		contractStore := contracts.NewPostgresStore(db)
		s.contractService = contracts.NewService(contractStore, &escrowLedgerAdapter{s.ledger})
		s.contractTimer = contracts.NewTimer(s.contractService, contractStore, s.logger)
		s.logger.Info("contracts enabled (postgres)")

		// Streams with PostgreSQL store (streaming micropayments)
		streamStore := streams.NewPostgresStore(db)
		s.streamService = streams.NewService(streamStore, &streamLedgerAdapter{s.ledger})
		s.streamTimer = streams.NewTimer(s.streamService, streamStore, s.logger)
		s.logger.Info("streams enabled (postgres)")

		// Gateway with in-memory store (transparent payment proxy)
		gwStore := gateway.NewMemoryStore()
		gwResolver := gateway.NewResolver(&gatewayRegistryAdapter{s.registry})
		gwForwarder := gateway.NewForwarder(0)
		s.gatewayService = gateway.NewService(gwStore, gwResolver, gwForwarder, &gatewayLedgerAdapter{s.ledger}, s.logger)
		s.gatewayTimer = gateway.NewTimer(s.gatewayService, gwStore, s.logger)
		s.logger.Info("gateway enabled")

		// Negotiation with PostgreSQL store (autonomous deal-making)
		negotiationStore := negotiation.NewPostgresStore(db)
		reputationProvForNeg := reputation.NewRegistryProvider(s.registry)
		s.negotiationService = negotiation.NewService(negotiationStore, reputationProvForNeg).
			WithContractFormer(&contractFormerAdapter{s.contractService}).
			WithLedger(&negotiationLedgerAdapter{s.ledger})
		s.negotiationTimer = negotiation.NewTimer(s.negotiationService, s.logger)
		s.logger.Info("negotiation enabled (postgres)")

		// Stakes with PostgreSQL store (agent revenue staking)
		stakesStore := stakes.NewPostgresStore(db)
		s.stakesService = stakes.NewService(stakesStore, &stakesLedgerAdapter{s.ledger})
		s.stakesDistributor = stakes.NewDistributor(s.stakesService, s.logger)
		s.logger.Info("stakes enabled (postgres)")

		// Wire revenue interception into payment flows
		revenueAdapter := &revenueAccumulatorAdapter{s.stakesService}
		s.escrowService.WithRevenueAccumulator(revenueAdapter)
		s.streamService.WithRevenueAccumulator(revenueAdapter)
		s.gatewayService.WithRecorder(&gatewayRecorderAdapter{s.registry}).
			WithRevenueAccumulator(revenueAdapter)

		// Reputation snapshots (PostgreSQL)
		s.reputationStore = reputation.NewPostgresSnapshotStore(db)
		s.logger.Info("reputation snapshots enabled (postgres)")

		// Credit system (spend on credit, repay from earnings)
		creditStore := credit.NewPostgresStore(db)
		// Note: gateway verification/contracts wired after verifiedService is created (below)
		if err := creditStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate credit store", "error", err)
		}
		reputationProv := reputation.NewRegistryProvider(s.registry)
		creditScorer := credit.NewScorer()
		s.creditService = credit.NewService(
			creditStore,
			creditScorer,
			reputationProv,
			&creditMetricsAdapter{reputationProv},
			&creditLedgerAdapter{s.ledger},
		)
		s.creditTimer = credit.NewTimer(s.creditService, s.logger)
		s.logger.Info("credit system enabled")

		// Risk scoring engine (real-time anomaly detection for session key transactions)
		riskStore := risk.NewPostgresStore(db)
		if err := riskStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate risk store", "error", err)
		}
		s.riskEngine = risk.NewEngine(riskStore)
		s.logger.Info("risk scoring enabled (postgres)")

		// Verified agents (performance-guaranteed agents with bonds)
		verifiedStore := verified.NewPostgresStore(db)
		if err := verifiedStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate verified store", "error", err)
		}
		verifiedReputationProv := reputation.NewRegistryProvider(s.registry)
		verifiedScorer := verified.NewScorer()
		s.verifiedService = verified.NewService(
			verifiedStore,
			verifiedScorer,
			verifiedReputationProv,
			&creditMetricsAdapter{verifiedReputationProv},
			&verifiedLedgerAdapter{s.ledger},
		)
		s.verifiedService.WithBuyerPayments(&buyerPaymentAdapter{s.contractService})
		s.verifiedEnforcer = verified.NewEnforcer(
			s.verifiedService,
			&contractCallAdapter{s.contractService},
			s.wallet.Address(), // platform address receives forfeited bonds
			s.logger,
		)
		s.logger.Info("verified agents enabled (postgres)")

		// Wire verification and contracts into gateway for premium handling
		s.gatewayService.
			WithVerification(&gatewayVerificationAdapter{s.verifiedService}).
			WithContracts(&gatewayContractAdapter{s.contractService}).
			WithGuaranteeFundAddr(s.wallet.Address())
	} else {
		s.registry = registry.NewMemoryStore()
		s.logger.Info("using in-memory storage (data will not persist)")

		// Session keys with in-memory store
		sessionStore := sessionkeys.NewMemoryStore()
		policyStore := sessionkeys.NewPolicyMemoryStore()
		s.sessionMgr = sessionkeys.NewManager(sessionStore, nil, policyStore)

		// API keys with in-memory store
		s.authMgr = auth.NewManager(auth.NewMemoryStore())

		// In-memory ledger for demo mode
		memStore := ledger.NewMemoryStore()
		memEventStore := ledger.NewMemoryEventStore()
		memAuditLogger := ledger.NewMemoryAuditLogger()
		memAlertStore := ledger.NewMemoryAlertStore()
		memAlertChecker := ledger.NewAlertChecker(memAlertStore)
		s.ledger = ledger.NewWithEvents(memStore, memEventStore).
			WithAuditLogger(memAuditLogger).
			WithAlertChecker(memAlertChecker)
		s.logger.Info("agent balance tracking enabled (in-memory)")

		// Webhooks with in-memory store
		s.webhooks = webhooks.NewDispatcher(webhooks.NewMemoryStore())

		// Commentary and predictions with in-memory stores (for demo)
		s.commentary = commentary.NewService(commentary.NewMemoryStore())
		s.predictions = predictions.NewService(predictions.NewMemoryStore(), &registryMetricProvider{s.registry})

		// Escrow with in-memory store
		escrowStore := escrow.NewMemoryStore()
		s.escrowService = escrow.NewService(escrowStore, &escrowLedgerAdapter{s.ledger}).WithLogger(s.logger)
		s.escrowTimer = escrow.NewTimer(s.escrowService, escrowStore, s.logger)
		s.logger.Info("escrow enabled (in-memory)")

		// Contracts with in-memory store
		contractStore := contracts.NewMemoryStore()
		s.contractService = contracts.NewService(contractStore, &escrowLedgerAdapter{s.ledger})
		s.contractTimer = contracts.NewTimer(s.contractService, contractStore, s.logger)
		s.logger.Info("contracts enabled (in-memory)")

		// Streams with in-memory store (streaming micropayments)
		streamStore := streams.NewMemoryStore()
		s.streamService = streams.NewService(streamStore, &streamLedgerAdapter{s.ledger})
		s.streamTimer = streams.NewTimer(s.streamService, streamStore, s.logger)
		s.logger.Info("streams enabled (in-memory)")

		// Gateway with in-memory store (transparent payment proxy)
		gwStore2 := gateway.NewMemoryStore()
		gwResolver2 := gateway.NewResolver(&gatewayRegistryAdapter{s.registry})
		gwForwarder2 := gateway.NewForwarder(0)
		s.gatewayService = gateway.NewService(gwStore2, gwResolver2, gwForwarder2, &gatewayLedgerAdapter{s.ledger}, s.logger)
		s.gatewayTimer = gateway.NewTimer(s.gatewayService, gwStore2, s.logger)
		s.logger.Info("gateway enabled (in-memory)")

		// Negotiation with in-memory store (autonomous deal-making)
		negotiationStore := negotiation.NewMemoryStore()
		reputationProvForNeg := reputation.NewRegistryProvider(s.registry)
		s.negotiationService = negotiation.NewService(negotiationStore, reputationProvForNeg).
			WithContractFormer(&contractFormerAdapter{s.contractService}).
			WithLedger(&negotiationLedgerAdapter{s.ledger})
		s.negotiationTimer = negotiation.NewTimer(s.negotiationService, s.logger)
		s.logger.Info("negotiation enabled (in-memory)")

		// Stakes with in-memory store (agent revenue staking)
		stakesStore := stakes.NewMemoryStore()
		s.stakesService = stakes.NewService(stakesStore, &stakesLedgerAdapter{s.ledger})
		s.stakesDistributor = stakes.NewDistributor(s.stakesService, s.logger)
		s.logger.Info("stakes enabled (in-memory)")

		// Wire revenue interception into payment flows
		revenueAdapter := &revenueAccumulatorAdapter{s.stakesService}
		s.escrowService.WithRevenueAccumulator(revenueAdapter)
		s.streamService.WithRevenueAccumulator(revenueAdapter)
		s.gatewayService.WithRecorder(&gatewayRecorderAdapter{s.registry}).
			WithRevenueAccumulator(revenueAdapter)

		// Reputation snapshots (in-memory)
		s.reputationStore = reputation.NewMemorySnapshotStore()
		s.logger.Info("reputation snapshots enabled (in-memory)")

		// Credit system (in-memory) — use demo scorer with relaxed policies
		creditStore := credit.NewMemoryStore()
		reputationProv := reputation.NewRegistryProvider(s.registry)
		creditScorer := credit.NewDemoScorer()
		s.creditService = credit.NewService(
			creditStore,
			creditScorer,
			reputationProv,
			&creditMetricsAdapter{reputationProv},
			&creditLedgerAdapter{s.ledger},
		)
		s.creditTimer = credit.NewTimer(s.creditService, s.logger)
		s.logger.Info("credit system enabled (in-memory)")

		// Risk scoring engine (in-memory)
		s.riskEngine = risk.NewEngine(risk.NewMemoryStore())
		s.logger.Info("risk scoring enabled (in-memory)")

		// Verified agents (in-memory) — use demo scorer with relaxed policies
		verifiedStore := verified.NewMemoryStore()
		verifiedReputationProv := reputation.NewRegistryProvider(s.registry)
		verifiedScorer := verified.NewDemoScorer()
		s.verifiedService = verified.NewService(
			verifiedStore,
			verifiedScorer,
			verifiedReputationProv,
			&creditMetricsAdapter{verifiedReputationProv},
			&verifiedLedgerAdapter{s.ledger},
		)
		s.verifiedService.WithBuyerPayments(&buyerPaymentAdapter{s.contractService})
		s.logger.Info("verified agents enabled (in-memory)")

		// Wire verification and contracts into gateway for premium handling
		s.gatewayService.
			WithVerification(&gatewayVerificationAdapter{s.verifiedService}).
			WithContracts(&gatewayContractAdapter{s.contractService})
		// guaranteeFundAddr set after wallet is created (below)
	}

	// Create wallet if not injected
	if s.wallet == nil {
		w, err := wallet.New(wallet.Config{
			RPCURL:       cfg.RPCURL,
			PrivateKey:   cfg.PrivateKey,
			ChainID:      cfg.ChainID,
			USDCContract: cfg.USDCContract,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create wallet: %w", err)
		}
		s.wallet = w
	}

	// Wire gateway guarantee fund address now that wallet is available
	if s.gatewayService != nil && s.wallet != nil && s.db == nil {
		s.gatewayService.WithGuaranteeFundAddr(s.wallet.Address())
	}

	// Create paymaster for gas abstraction
	paymasterCfg := gas.DefaultConfig()
	walletAddr := ""
	if s.wallet != nil {
		walletAddr = s.wallet.Address()
	}
	paymaster, err := gas.NewPlatformPaymaster(cfg.RPCURL, walletAddr, paymasterCfg)
	if err != nil {
		s.logger.Warn("failed to initialize paymaster, gas estimation disabled", "error", err)
	} else {
		s.paymaster = paymaster
		s.logger.Info("gas abstraction enabled", "ethPrice", paymasterCfg.ETHPriceUSD, "wallet", walletAddr)
	}

	// Create deposit watcher (auto-credits agent balances when they deposit)
	if s.ledger != nil && s.wallet != nil && cfg.RPCURL != "" {
		watcherCfg := watcher.DefaultConfig()
		watcherCfg.RPCURL = cfg.RPCURL
		watcherCfg.USDCContract = common.HexToAddress(cfg.USDCContract)
		watcherCfg.PlatformAddress = common.HexToAddress(s.wallet.Address())

		w, err := watcher.New(watcherCfg, s.ledger, &registryChecker{s.registry}, s.logger)
		if err != nil {
			s.logger.Warn("failed to create deposit watcher", "error", err)
		} else {
			s.depositWatcher = w
			s.logger.Info("deposit watcher configured",
				"platform", watcherCfg.PlatformAddress.Hex(),
				"usdc", watcherCfg.USDCContract.Hex(),
			)
		}
	}

	s.logger.Info("API authentication enabled")

	// Create realtime hub for WebSocket streaming
	s.realtimeHub = realtime.NewHub(s.logger)
	s.logger.Info("realtime streaming enabled")

	// Configure gin
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	s.router = gin.New()
	s.setupMiddleware()
	s.setupRoutes()

	s.healthy.Store(true)

	return s, nil
}

// maskDSN hides password in connection string for logging
func maskDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "***"
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}

// -----------------------------------------------------------------------------
// Middleware
// -----------------------------------------------------------------------------

func (s *Server) setupMiddleware() {
	// Recovery with logging
	s.router.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		logging.L(c.Request.Context()).Error("panic recovered",
			"error", recovered,
			"path", c.Request.URL.Path,
		)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "An unexpected error occurred",
		})
	}))

	// Security headers
	s.router.Use(security.HeadersMiddleware())

	// CORS (allow all origins for demo - restrict in production)
	s.router.Use(security.CORSMiddleware([]string{"*"}))

	// Gzip compression (after CORS, before request size limit)
	s.router.Use(gzipMiddleware())

	// Request size limit (1MB)
	s.router.Use(validation.RequestSizeMiddleware(validation.MaxRequestSize))

	// Rate limiting
	s.rateLimiter = ratelimit.New(ratelimit.Config{
		RequestsPerMinute: s.cfg.RateLimitRPS,
		BurstSize:         10,
		CleanupInterval:   time.Minute,
	})
	s.router.Use(s.rateLimiter.Middleware())

	// Prometheus metrics
	s.router.Use(metrics.Middleware())

	// Request ID
	s.router.Use(s.requestIDMiddleware())

	// Logging
	s.router.Use(s.loggingMiddleware())

	// Request timeout (after logging so timeouts are logged)
	s.router.Use(s.timeoutMiddleware())
}

func (s *Server) requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check for existing request ID (from load balancer, etc.)
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Add to context
		ctx := logging.WithRequestID(c.Request.Context(), requestID)
		ctx = logging.WithLogger(ctx, s.logger)
		c.Request = c.Request.WithContext(ctx)

		// Set response header
		c.Header("X-Request-ID", requestID)

		c.Next()
	}
}

func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		logger := logging.L(c.Request.Context())

		// Log level based on status code
		switch {
		case status >= 500:
			logger.Error("request completed",
				"method", c.Request.Method,
				"path", path,
				"status", status,
				"latency_ms", latency.Milliseconds(),
				"client_ip", c.ClientIP(),
			)
		case status >= 400:
			logger.Warn("request completed",
				"method", c.Request.Method,
				"path", path,
				"status", status,
				"latency_ms", latency.Milliseconds(),
			)
		default:
			logger.Info("request completed",
				"method", c.Request.Method,
				"path", path,
				"status", status,
				"latency_ms", latency.Milliseconds(),
			)
		}
	}
}

// -----------------------------------------------------------------------------
// Routes
// -----------------------------------------------------------------------------

func (s *Server) setupRoutes() {
	// Health & metrics endpoints
	s.router.GET("/health", s.healthHandler)
	s.router.GET("/health/live", s.livenessHandler)
	s.router.GET("/health/ready", s.readinessHandler)
	s.router.GET("/metrics", metrics.Handler())

	// PUBLIC PAGES - The "app" layer (what people browse)
	s.router.GET("/", dashboardHandler)                                                       // Main dashboard (the polished view)
	s.router.GET("/debug", debugPageHandler)                                                  // Debug page to diagnose issues
	s.router.GET("/feed", feedPageHandler)                                                    // Raw transaction feed
	s.router.GET("/timeline", timelinePageHandler)                                            // Real-time timeline (the magic)
	s.router.GET("/agents", agentsPageHandler)                                                // Agent directory
	s.router.GET("/services", servicesPageHandler)                                            // Service marketplace
	s.router.GET("/agent/:address", validation.AddressParamMiddleware(), agentProfileHandler) // Individual agent profiles
	s.router.GET("/docs", s.docsRedirectHandler)                                              // Redirect to GitHub/docs
	s.router.Static("/assets", "./assets")                                                    // Static assets (logo, etc.)

	// WebSocket for real-time streaming
	s.router.GET("/ws", func(c *gin.Context) {
		s.realtimeHub.HandleWebSocket(c.Writer, c.Request)
	})

	// API info endpoints
	s.router.GET("/api", s.infoHandler)
	s.router.GET("/wallet", s.walletInfoHandler)

	// V1 API group
	v1 := s.router.Group("/v1")
	// Validate :address URL params on all v1 routes (no-op when param absent)
	v1.Use(validation.AddressParamMiddleware())
	registryHandler := registry.NewHandler(s.registry)

	// Wire reputation into discovery so agents see trust scores when searching
	reputationProvider := reputation.NewRegistryProvider(s.registry)
	registryHandler.SetReputation(reputationProvider)

	// Wire verified agent status into discovery so buyers see guaranteed services
	if s.verifiedService != nil {
		registryHandler.SetVerification(s.verifiedService)
	}

	// Wire reputation impact tracking into escrow (dispute/confirm outcomes)
	s.escrowService.WithReputationImpactor(reputationProvider)

	// Wire on-chain transaction verifier so POST /transactions checks receipts.
	// Only in production (with DB) — in demo mode, transactions are auto-confirmed.
	if s.wallet != nil && s.db != nil {
		registryHandler.SetVerifier(&walletVerifierAdapter{s.wallet})
	}

	// PUBLIC ROUTES (no auth required)
	// These are the discovery/read endpoints
	v1.GET("/platform", s.platformHandler)
	v1.GET("/agents", cacheControl(30), registryHandler.ListAgents)
	v1.GET("/agents/:address", cacheControl(15), registryHandler.GetAgent)
	v1.GET("/services", cacheControl(30), registryHandler.DiscoverServices)
	v1.GET("/agents/:address/transactions", registryHandler.ListTransactions)
	v1.GET("/network/stats", cacheControl(60), registryHandler.GetNetworkStats)
	v1.GET("/network/stats/enhanced", cacheControl(60), s.enhancedStatsHandler) // Demo-friendly extended stats
	v1.GET("/feed", cacheControl(10), registryHandler.GetPublicFeed)

	// AI-powered natural language search
	v1.GET("/search", s.naturalLanguageSearch)
	v1.POST("/search", s.naturalLanguageSearch)

	// REGISTRATION (public but returns API key)
	v1.POST("/agents", s.registerAgentWithAPIKey)

	// AUTH INFO (public)
	authHandler := auth.NewHandler(s.authMgr)
	v1.GET("/auth/info", authHandler.Info)

	// PROTECTED ROUTES (require API key)
	// These modify agent data and require ownership
	protected := v1.Group("")
	protected.Use(auth.Middleware(s.authMgr))
	{
		// Transaction recording requires auth to prevent reputation manipulation
		protected.POST("/transactions", registryHandler.RecordTransaction)

		// Agent mutations (must own the agent)
		protected.DELETE("/agents/:address", auth.RequireOwnership(s.authMgr, "address"), registryHandler.DeleteAgent)

		// Service management (must own the agent)
		protected.POST("/agents/:address/services", auth.RequireOwnership(s.authMgr, "address"), registryHandler.AddService)
		protected.PUT("/agents/:address/services/:serviceId", auth.RequireOwnership(s.authMgr, "address"), registryHandler.UpdateService)
		protected.DELETE("/agents/:address/services/:serviceId", auth.RequireOwnership(s.authMgr, "address"), registryHandler.RemoveService)

		// API key management
		protected.GET("/auth/keys", authHandler.ListKeys)
		protected.POST("/auth/keys", authHandler.CreateKey)
		protected.DELETE("/auth/keys/:keyId", authHandler.RevokeKey)
		protected.POST("/auth/keys/:keyId/regenerate", authHandler.RegenerateKey)
		protected.GET("/auth/me", authHandler.GetCurrentAgent)
	}

	// Session key routes (bounded autonomy - the differentiator)
	// Session key creation requires auth, but using a session key doesn't
	var sessionHandler *sessionkeys.Handler
	if s.wallet != nil {
		// Enable real on-chain execution with balance checking
		var balanceAdapter sessionkeys.BalanceService
		if s.ledger != nil {
			balanceAdapter = &ledgerAdapter{s.ledger}
		}
		sessionHandler = sessionkeys.NewHandlerWithExecution(
			s.sessionMgr,
			&walletAdapter{s.wallet},
			&registryAdapter{s.registry},
			balanceAdapter,
			s.logger,
		)
	} else {
		// Dry-run mode (validation only, no execution)
		sessionHandler = sessionkeys.NewHandler(s.sessionMgr, s.logger)
	}

	// In demo mode (no DB), skip on-chain transfers and use ledger-only accounting
	if s.db == nil {
		sessionHandler = sessionHandler.WithDemoMode()
	}

	// Add real-time event emitter
	if s.realtimeHub != nil {
		sessionHandler = sessionHandler.WithEvents(&realtimeEventEmitter{s.realtimeHub})
	}

	// Add revenue accumulator for stakes interception
	if s.stakesService != nil {
		sessionHandler = sessionHandler.WithRevenueAccumulator(&revenueAccumulatorAdapter{s.stakesService})
	}

	// Add risk scoring engine for anomaly detection
	if s.riskEngine != nil {
		sessionHandler = sessionHandler.WithRiskScorer(s.riskEngine)
	}

	// Add budget/expiration alert checker backed by webhooks
	if s.webhooks != nil {
		alertChecker := sessionkeys.NewAlertChecker(&webhookAlertNotifier{d: s.webhooks})
		sessionHandler = sessionHandler.WithAlertChecker(alertChecker)
	}

	protectedSessions := v1.Group("")
	protectedSessions.Use(auth.Middleware(s.authMgr))
	{
		// Session key list/get require ownership — they expose spending limits and usage data
		protectedSessions.GET("/agents/:address/sessions", auth.RequireOwnership(s.authMgr, "address"), sessionHandler.ListSessionKeys)
		protectedSessions.GET("/agents/:address/sessions/:keyId", auth.RequireOwnership(s.authMgr, "address"), sessionHandler.GetSessionKey)
		protectedSessions.POST("/agents/:address/sessions", auth.RequireOwnership(s.authMgr, "address"), sessionHandler.CreateSessionKey)
		protectedSessions.DELETE("/agents/:address/sessions/:keyId", auth.RequireOwnership(s.authMgr, "address"), sessionHandler.RevokeSessionKey)
		protectedSessions.POST("/agents/:address/sessions/:keyId/rotate", auth.RequireOwnership(s.authMgr, "address"), sessionHandler.RotateSessionKey)
	}

	// Policy engine routes (CRUD + attach/detach, ownership required)
	protectedPolicies := v1.Group("")
	protectedPolicies.Use(auth.Middleware(s.authMgr))
	protectedPolicies.Use(auth.RequireOwnership(s.authMgr, "address"))
	sessionHandler.RegisterPolicyRoutes(protectedPolicies)
	// Using a session key to transact doesn't require API key (the session key IS the auth)
	v1.POST("/agents/:address/sessions/:keyId/transact", sessionHandler.Transact)

	// Delegation routes (A2A) — authenticated by session key signature, no API key needed
	v1.POST("/sessions/:keyId/delegate", sessionHandler.CreateDelegation)
	v1.GET("/sessions/:keyId/tree", sessionHandler.GetDelegationTree)

	// Ledger routes (agent balances)
	if s.ledger != nil {
		var ledgerHandler *ledger.Handler
		if s.wallet != nil {
			// Enable real withdrawals
			ledgerHandler = ledger.NewHandlerWithWithdrawals(s.ledger, &withdrawalAdapter{s.wallet}, s.logger)
		} else {
			ledgerHandler = ledger.NewHandler(s.ledger, s.logger)
		}
		v1.GET("/agents/:address/balance", ledgerHandler.GetBalance)
		v1.GET("/agents/:address/ledger", ledgerHandler.GetHistory)

		// Protected ledger routes
		protectedLedger := v1.Group("")
		protectedLedger.Use(auth.Middleware(s.authMgr))
		{
			protectedLedger.POST("/agents/:address/withdraw", auth.RequireOwnership(s.authMgr, "address"), ledgerHandler.RequestWithdrawal)
		}

		// Admin route for recording deposits (in production: webhook from blockchain indexer)
		// RequireAdmin checks X-Admin-Secret header (or allows any auth in demo mode).
		protectedLedger.POST("/admin/deposits", auth.RequireAdmin(), ledgerHandler.RecordDeposit)

		// Admin routes for reconciliation, audit, reversals, batch ops
		adminLedger := v1.Group("")
		adminLedger.Use(auth.Middleware(s.authMgr), auth.RequireAdmin())
		ledgerHandler.RegisterAdminRoutes(adminLedger)

		// Alert routes (per-agent)
		ledgerHandler.RegisterAlertRoutes(v1)
	}

	// Gas abstraction routes (agents pay USDC only, gas is sponsored)
	if s.paymaster != nil {
		gasHandler := gas.NewHandler(s.paymaster)
		gasHandler.RegisterRoutes(v1)
	}

	// Reputation routes (the network moat - agents build reputation over time)
	// reputationProvider is already created above for discovery enrichment
	//
	// Create signer from config (nil if no secret set)
	s.reputationSigner = reputation.NewSigner(s.cfg.ReputationHMACSecret)

	// Create worker for periodic snapshots
	if s.reputationStore != nil {
		workerInterval := time.Hour
		if s.db == nil {
			workerInterval = 10 * time.Second // Fast in demo mode
		}
		s.reputationWorker = reputation.NewWorker(reputationProvider, s.reputationStore, workerInterval, s.logger)
	}

	// Create matview refresher for service discovery (Postgres only)
	if s.db != nil {
		s.matviewRefresher = registry.NewMatviewRefresher(s.db, 30*time.Second, s.logger)
		s.partitionMaint = registry.NewPartitionMaintainer(s.db, 24*time.Hour, s.logger)
	}

	reputationHandler := reputation.NewHandlerFull(reputationProvider, s.reputationStore, s.reputationSigner)
	reputationHandler.RegisterRoutes(v1)

	// Webhook routes (event notifications to external services)
	if s.webhooks != nil {
		var webhookStore webhooks.Store
		if s.db != nil {
			webhookStore = webhooks.NewPostgresStore(s.db)
		} else {
			webhookStore = webhooks.NewMemoryStore()
		}
		webhookHandler := webhooks.NewHandler(webhookStore, s.webhooks)

		// Protected webhook management routes
		protectedWebhooks := v1.Group("")
		protectedWebhooks.Use(auth.Middleware(s.authMgr))
		{
			protectedWebhooks.POST("/agents/:address/webhooks", auth.RequireOwnership(s.authMgr, "address"), webhookHandler.CreateWebhook)
			protectedWebhooks.GET("/agents/:address/webhooks", auth.RequireOwnership(s.authMgr, "address"), webhookHandler.ListWebhooks)
			protectedWebhooks.DELETE("/agents/:address/webhooks/:webhookId", auth.RequireOwnership(s.authMgr, "address"), webhookHandler.DeleteWebhook)
		}
	}

	// Commentary routes (verbal agents - the social/insight layer)
	if s.commentary != nil {
		commentaryHandler := commentary.NewHandler(s.commentary)

		// Add real-time event emitter
		if s.realtimeHub != nil {
			commentaryHandler = commentaryHandler.WithEvents(&commentaryEventEmitter{s.realtimeHub})
		}

		// Public routes - anyone can read
		commentaryHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can post
		protectedCommentary := v1.Group("")
		protectedCommentary.Use(auth.Middleware(s.authMgr))
		commentaryHandler.RegisterProtectedRoutes(protectedCommentary)
	}

	// Escrow routes (buyer protection for service payments)
	if s.escrowService != nil {
		escrowHandler := escrow.NewHandler(s.escrowService)

		// Public routes - anyone can read
		escrowHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can create/confirm/dispute
		protectedEscrow := v1.Group("")
		protectedEscrow.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		escrowHandler.RegisterProtectedRoutes(protectedEscrow)
	}

	// Credit routes (agent credit lines - spend on credit, repay from earnings)
	if s.creditService != nil {
		creditHandler := credit.NewHandler(s.creditService)

		// Public routes - anyone can view credit status
		creditHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can apply/repay/review
		protectedCredit := v1.Group("")
		protectedCredit.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		creditHandler.RegisterProtectedRoutes(protectedCredit)

		// Admin routes - require authentication
		adminCredit := v1.Group("/admin")
		adminCredit.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		creditHandler.RegisterAdminRoutes(adminCredit)
	}

	// Verified agent routes (performance-guaranteed agents with bonds)
	if s.verifiedService != nil {
		verifiedHandler := verified.NewHandler(s.verifiedService)

		// Public routes - anyone can view verified status
		v1.GET("/verified", verifiedHandler.List)
		v1.GET("/verified/:address", verifiedHandler.GetByAgent)

		// Protected routes - only authenticated agents can apply/revoke/reinstate
		protectedVerified := v1.Group("")
		protectedVerified.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		protectedVerified.POST("/verified/apply", verifiedHandler.Apply)
		protectedVerified.POST("/verified/:address/revoke", auth.RequireOwnership(s.authMgr, "address"), verifiedHandler.Revoke)
		protectedVerified.POST("/verified/:address/reinstate", auth.RequireOwnership(s.authMgr, "address"), verifiedHandler.Reinstate)
	}

	// Contract routes (service agreements with SLA enforcement)
	if s.contractService != nil {
		contractHandler := contracts.NewHandler(s.contractService)

		// Public routes - anyone can read
		contractHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can propose/accept/reject/call/terminate
		protectedContracts := v1.Group("")
		protectedContracts.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		contractHandler.RegisterProtectedRoutes(protectedContracts)
	}

	// Streaming micropayment routes (per-tick payments for continuous services)
	if s.streamService != nil {
		streamHandler := streams.NewHandler(s.streamService)

		// Public routes - anyone can read
		streamHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can open/tick/close
		protectedStreams := v1.Group("")
		protectedStreams.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		streamHandler.RegisterProtectedRoutes(protectedStreams)
	}

	// Gateway routes (transparent payment proxy for AI agents)
	if s.gatewayService != nil {
		gatewayHandler := gateway.NewHandler(s.gatewayService)

		// Protected routes - session CRUD requires API key auth
		protectedGateway := v1.Group("")
		protectedGateway.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		gatewayHandler.RegisterProtectedRoutes(protectedGateway)

		// Proxy route - uses gateway token auth (X-Gateway-Token), no API key needed
		gatewayHandler.RegisterProxyRoute(v1)
	}

	// Negotiation routes (autonomous deal-making between agents)
	if s.negotiationService != nil {
		negotiationHandler := negotiation.NewHandler(s.negotiationService)

		// Public routes - anyone can browse RFPs and bids
		negotiationHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can publish/bid/counter/select/cancel
		protectedNeg := v1.Group("")
		protectedNeg.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		negotiationHandler.RegisterProtectedRoutes(protectedNeg)

		// Admin routes - analytics
		adminNeg := v1.Group("")
		adminNeg.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr), auth.RequireAdmin())
		negotiationHandler.RegisterAdminRoutes(adminNeg)
	}

	// Predictions routes (verifiable predictions with reputation stakes)
	if s.predictions != nil {
		predictionsHandler := predictions.NewHandler(s.predictions)

		// Public routes - anyone can read
		predictionsHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can make/vote
		protectedPredictions := v1.Group("")
		protectedPredictions.Use(auth.Middleware(s.authMgr))
		predictionsHandler.RegisterProtectedRoutes(protectedPredictions)
	}

	// Stakes routes (agent revenue staking / investment)
	if s.stakesService != nil {
		stakesHandler := stakes.NewHandler(s.stakesService)

		// Public routes - anyone can browse offerings and portfolios
		stakesHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can create/invest/trade
		protectedStakes := v1.Group("")
		protectedStakes.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		stakesHandler.RegisterProtectedRoutes(protectedStakes)
	}

	// Enhanced feed - transactions + commentary interleaved
	v1.GET("/timeline", s.getTimeline)

	// Paywall config
	paywallCfg := paywall.Config{
		Wallet:              s.wallet,
		DefaultPrice:        s.cfg.DefaultPrice,
		Chain:               "base-sepolia",
		ChainID:             s.cfg.ChainID,
		Contract:            s.cfg.USDCContract,
		RequireConfirmation: false,
		ConfirmationTimeout: 30 * time.Second,
		ValidFor:            5 * time.Minute,
		OnPaymentReceived: func(proof *paywall.PaymentProof, route string) {
			s.logger.Info("payment received",
				"tx_hash", proof.TxHash,
				"from", proof.From,
				"route", route,
			)
			// Record transaction in registry
			s.recordPayment(proof)
		},
		OnPaymentFailed: func(proof *paywall.PaymentProof, err error) {
			s.logger.Warn("payment failed",
				"tx_hash", proof.TxHash,
				"error", err,
			)
		},
	}

	// Paid API routes (demo endpoints)
	paid := s.router.Group("/api/v1")
	paid.Use(paywall.Middleware(paywallCfg))
	{
		paid.GET("/joke", s.jokeHandler)
		paid.POST("/echo", s.echoHandler)
	}

	// Premium endpoint with custom price
	s.router.GET("/api/v1/premium",
		paywall.MiddlewareWithPrice(paywallCfg, "0.01", "Premium content"),
		s.premiumHandler,
	)
}

// recordPayment records a payment in the registry (builds the data moat)
func (s *Server) recordPayment(proof *paywall.PaymentProof) {
	ctx := context.Background()

	// Paywall already verified the payment on-chain before calling this callback
	tx := &registry.Transaction{
		TxHash: proof.TxHash,
		From:   proof.From,
		To:     s.wallet.Address(),
		Status: "verified",
	}
	if err := s.registry.RecordTransaction(ctx, tx); err != nil {
		s.logger.Error("failed to record transaction", "error", err)
	}

	// Broadcast to realtime clients
	if s.realtimeHub != nil {
		s.realtimeHub.BroadcastTransaction(map[string]interface{}{
			"txHash":      proof.TxHash,
			"from":        proof.From,
			"to":          s.wallet.Address(),
			"serviceType": "payment",
			"status":      "confirmed",
		})
	}

	// Dispatch webhook to receiver
	if s.webhooks != nil {
		txHashID := proof.TxHash
		if len(txHashID) > 16 {
			txHashID = txHashID[:16]
		}
		event := &webhooks.Event{
			ID:        "evt_" + txHashID,
			Type:      webhooks.EventPaymentReceived,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"txHash": proof.TxHash,
				"from":   proof.From,
				"to":     s.wallet.Address(),
			},
		}
		_ = s.webhooks.DispatchToAgent(ctx, s.wallet.Address(), event)
	}
}

// registerAgentWithAPIKey handles POST /v1/agents
// This wraps the standard registration to also generate and return an API key
func (s *Server) registerAgentWithAPIKey(c *gin.Context) {
	ctx := c.Request.Context()

	// Parse request
	var req struct {
		Address     string                 `json:"address" binding:"required"`
		Name        string                 `json:"name" binding:"required"`
		Description string                 `json:"description"`
		Metadata    map[string]interface{} `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Validate address format
	if !validation.IsValidEthAddress(req.Address) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_address",
			"message": "address must be a valid Ethereum address (0x + 40 hex chars)",
		})
		return
	}

	// Sanitize string fields
	req.Name = validation.SanitizeString(req.Name, 200)
	req.Description = validation.SanitizeString(req.Description, validation.MaxStringLength)

	// Create agent
	agent := &registry.Agent{
		Address:     req.Address,
		Name:        req.Name,
		Description: req.Description,
		Metadata:    req.Metadata,
	}

	if err := s.registry.CreateAgent(ctx, agent); err != nil {
		if errors.Is(err, registry.ErrAgentExists) {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "agent_exists",
				"message": "An agent with this address is already registered",
			})
			return
		}
		s.logger.Error("failed to create agent", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to register agent",
		})
		return
	}

	// Generate API key for the new agent
	rawKey, keyInfo, err := s.authMgr.GenerateKey(ctx, agent.Address, "Primary key")
	if err != nil {
		s.logger.Error("failed to generate API key", "error", err)
		// Agent was created but key generation failed
		// Still return success but note the issue
		c.JSON(http.StatusCreated, gin.H{
			"agent":   agent,
			"warning": "Agent registered but API key generation failed. Contact support.",
		})
		return
	}

	s.logger.Info("agent registered with API key",
		"address", agent.Address,
		"name", agent.Name,
		"keyId", keyInfo.ID,
	)

	// Return agent and API key
	c.JSON(http.StatusCreated, gin.H{
		"agent":   agent,
		"apiKey":  rawKey,
		"keyId":   keyInfo.ID,
		"warning": "Store this API key securely. It will not be shown again.",
		"usage":   "Include 'Authorization: Bearer <apiKey>' header in requests.",
	})
}

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

// HealthResponse for health check endpoints
type HealthResponse struct {
	Status    string            `json:"status"`
	Version   string            `json:"version"`
	Checks    map[string]string `json:"checks,omitempty"`
	Timestamp string            `json:"timestamp"`
}

func (s *Server) healthHandler(c *gin.Context) {
	checks := make(map[string]string)

	// Check wallet connectivity
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if _, err := s.wallet.BalanceOf(ctx, common.Address{}); err != nil {
		checks["rpc"] = "unhealthy"
	} else {
		checks["rpc"] = "healthy"
	}

	status := "healthy"
	httpStatus := http.StatusOK
	for _, v := range checks {
		if v != "healthy" {
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
			break
		}
	}

	c.JSON(httpStatus, HealthResponse{
		Status:    status,
		Version:   "0.1.0",
		Checks:    checks,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) livenessHandler(c *gin.Context) {
	if !s.healthy.Load() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "alive"})
}

func (s *Server) readinessHandler(c *gin.Context) {
	if !s.ready.Load() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
		return
	}

	checks := make(map[string]string)
	allOK := true

	// DB check
	if s.db != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			checks["database"] = "unhealthy"
			allOK = false
		} else {
			checks["database"] = "healthy"
		}
	}

	// Timer checks
	checks["escrow_timer"] = timerStatus(s.escrowTimer)
	checks["stream_timer"] = timerStatus(s.streamTimer)
	checks["contract_timer"] = timerStatus(s.contractTimer)
	checks["gateway_timer"] = timerStatus(s.gatewayTimer)
	checks["negotiation_timer"] = timerStatus(s.negotiationTimer)

	status := "ready"
	httpStatus := http.StatusOK
	if !allOK {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}
	c.JSON(httpStatus, gin.H{"status": status, "checks": checks})
}

type runnable interface{ Running() bool }

func timerStatus(t interface{}) string {
	if t == nil {
		return "not_configured"
	}
	if tr, ok := t.(runnable); ok {
		if tr.Running() {
			return "running"
		}
		return "stopped"
	}
	return "unknown"
}

func (s *Server) docsRedirectHandler(c *gin.Context) {
	c.Redirect(http.StatusTemporaryRedirect, "https://github.com/mbd888/alancoin")
}

func (s *Server) infoHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"name":        "Alancoin",
		"description": "Payment infrastructure for AI agents",
		"version":     "0.1.0",
		"chain":       "base-sepolia",
		"currency":    "USDC",
	})
}

// platformHandler returns platform info including deposit address
func (s *Server) platformHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"platform": gin.H{
			"name":           "Alancoin",
			"version":        "0.1.0",
			"depositAddress": s.wallet.Address(),
			"chain":          "base-sepolia",
			"chainId":        s.cfg.ChainID,
			"usdcContract":   s.cfg.USDCContract,
		},
		"instructions": gin.H{
			"deposit":  "Send USDC to depositAddress. Balance is auto-credited within 30 seconds.",
			"withdraw": "POST /v1/agents/{address}/withdraw with API key auth",
			"spend":    "Create a session key, then POST to /v1/agents/{address}/sessions/{keyId}/transact",
		},
	})
}

func (s *Server) walletInfoHandler(c *gin.Context) {
	ctx := c.Request.Context()

	balance, err := s.wallet.Balance(ctx)
	if err != nil {
		logging.L(ctx).Error("failed to get balance", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "balance_error",
			"message": "Failed to retrieve wallet balance",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"address":  s.wallet.Address(),
		"balance":  balance,
		"currency": "USDC",
		"chain":    "base-sepolia",
		"chain_id": s.cfg.ChainID,
	})
}

var jokes = []string{
	"Why do programmers prefer dark mode? Because light attracts bugs.",
	"There are only 10 types of people: those who understand binary and those who don't.",
	"A SQL query walks into a bar, walks up to two tables and asks... 'Can I join you?'",
	"Why do Java developers wear glasses? Because they don't C#.",
	"!false - It's funny because it's true.",
}

func (s *Server) jokeHandler(c *gin.Context) {
	proof := paywall.GetPaymentProof(c)
	joke := jokes[time.Now().Unix()%int64(len(jokes))]

	c.JSON(http.StatusOK, gin.H{
		"joke":       joke,
		"paid":       true,
		"payment_tx": proof.TxHash,
	})
}

func (s *Server) echoHandler(c *gin.Context) {
	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_json",
			"message": "Request body must be valid JSON",
		})
		return
	}

	proof := paywall.GetPaymentProof(c)

	c.JSON(http.StatusOK, gin.H{
		"echo":       body,
		"paid":       true,
		"payment_tx": proof.TxHash,
	})
}

func (s *Server) premiumHandler(c *gin.Context) {
	proof := paywall.GetPaymentProof(c)

	c.JSON(http.StatusOK, gin.H{
		"content":    "This is premium content worth $0.01",
		"paid":       true,
		"payment_tx": proof.TxHash,
	})
}

// enhancedStatsHandler returns extended network stats for demos
// Aggregates data from multiple sources: registry, session keys, commentary, gas
func (s *Server) enhancedStatsHandler(c *gin.Context) {
	ctx := c.Request.Context()

	// Get base stats from registry
	baseStats, err := s.registry.GetNetworkStats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to get network stats",
		})
		return
	}

	// Build enhanced response
	enhanced := gin.H{
		"totalAgents":       baseStats.TotalAgents,
		"totalServices":     baseStats.TotalServices,
		"totalTransactions": baseStats.TotalTransactions,
		"totalVolume":       baseStats.TotalVolume,
		"updatedAt":         baseStats.UpdatedAt,
	}

	// Add session key stats (the differentiator!)
	if s.sessionMgr != nil {
		activeKeys, err := s.sessionMgr.CountActive(ctx)
		if err == nil {
			enhanced["activeSessionKeys"] = activeKeys
		}
	}

	// Add gas sponsorship stats
	if s.paymaster != nil {
		spent, limit := s.paymaster.GetDailySpending()
		enhanced["gasSponsoredToday"] = spent
		enhanced["gasDailyLimit"] = limit

		// Get paymaster balance
		balance, err := s.paymaster.GetBalance(ctx)
		if err == nil {
			// Format as ETH
			balanceETH := new(big.Float).SetInt(balance)
			balanceETH.Quo(balanceETH, big.NewFloat(1e18))
			f, _ := balanceETH.Float64()
			enhanced["paymasterBalance"] = fmt.Sprintf("%.4f ETH", f)
		}
	}

	// Add commentary stats
	if s.commentary != nil {
		// Count verbal agents and comments (if store exposes this)
		// For now, just indicate commentary is enabled
		enhanced["commentaryEnabled"] = true
	}

	c.JSON(http.StatusOK, enhanced)
}

// -----------------------------------------------------------------------------
// Lifecycle
// -----------------------------------------------------------------------------

// Run starts the HTTP server with graceful shutdown
func (s *Server) Run(ctx context.Context) error {
	// Create a cancellable context for background goroutines so Shutdown() can stop them.
	runCtx, cancel := context.WithCancel(ctx)
	s.cancelRunCtx = cancel

	s.httpSrv = &http.Server{
		Addr:              ":" + s.cfg.Port,
		Handler:           s.router,
		ReadTimeout:       s.cfg.HTTPReadTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      s.cfg.HTTPWriteTimeout,
		IdleTimeout:       s.cfg.HTTPIdleTimeout,
	}

	// Channel to catch server errors
	errChan := make(chan error, 1)

	// Start server in goroutine
	go func() {
		s.logger.Info("starting server",
			"port", s.cfg.Port,
			"wallet", s.wallet.Address(),
		)
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	// Start deposit watcher
	if s.depositWatcher != nil {
		if err := s.depositWatcher.Start(runCtx); err != nil {
			s.logger.Error("failed to start deposit watcher", "error", err)
		}
	}

	// Start realtime hub
	if s.realtimeHub != nil {
		go s.realtimeHub.Run(runCtx)
	}

	// Start escrow auto-release timer
	if s.escrowTimer != nil {
		go s.escrowTimer.Start(runCtx)
	}

	// Start credit default-check timer
	if s.creditTimer != nil {
		go s.creditTimer.Start(runCtx)
	}

	// Start contract expiration timer
	if s.contractTimer != nil {
		go s.contractTimer.Start(runCtx)
	}

	// Start stream stale-close timer
	if s.streamTimer != nil {
		go s.streamTimer.Start(runCtx)
	}

	// Start gateway session expiry timer
	if s.gatewayTimer != nil {
		go s.gatewayTimer.Start(runCtx)
	}

	// Start negotiation deadline timer
	if s.negotiationTimer != nil {
		go s.negotiationTimer.Start(runCtx)
	}

	// Start stakes distribution timer
	if s.stakesDistributor != nil {
		go s.stakesDistributor.Start(runCtx)
	}

	// Start reputation snapshot worker
	if s.reputationWorker != nil {
		go s.reputationWorker.Start(runCtx)
	}

	// Start verified agent guarantee enforcer
	if s.verifiedEnforcer != nil {
		go s.verifiedEnforcer.Start(runCtx)
	}

	// Start materialized view refresher for service discovery
	if s.matviewRefresher != nil {
		go s.matviewRefresher.Start(runCtx)
	}

	// Start partition maintainer for transactions table
	if s.partitionMaint != nil {
		go s.partitionMaint.Start(runCtx)
	}

	// Start DB connection pool stats collector
	if s.db != nil {
		go metrics.StartDBStatsCollector(runCtx, s.db, 15*time.Second)
	}

	// Mark as ready after brief delay for startup
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.ready.Store(true)
		s.logger.Info("server ready")
	}()

	// Wait for shutdown signal or error
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errChan:
		return fmt.Errorf("server error: %w", err)
	case sig := <-sigChan:
		s.logger.Info("shutdown signal received", "signal", sig.String())
	case <-ctx.Done():
		s.logger.Info("context cancelled")
	}

	return s.Shutdown()
}

// Shutdown gracefully stops the server
func (s *Server) Shutdown() error {
	s.ready.Store(false)
	s.logger.Info("starting graceful shutdown")

	// Cancel the context for all background goroutines (hub, timers, watcher)
	if s.cancelRunCtx != nil {
		s.cancelRunCtx()
	}

	// Give load balancers time to stop sending traffic
	time.Sleep(5 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.httpSrv.Shutdown(ctx); err != nil {
		s.logger.Error("shutdown error", "error", err)
		return err
	}

	// Stop escrow timer
	if s.escrowTimer != nil {
		s.escrowTimer.Stop()
		s.logger.Info("escrow timer stopped")
	}

	// Stop credit timer
	if s.creditTimer != nil {
		s.creditTimer.Stop()
		s.logger.Info("credit timer stopped")
	}

	// Stop contract timer
	if s.contractTimer != nil {
		s.contractTimer.Stop()
		s.logger.Info("contract timer stopped")
	}

	// Stop stream timer
	if s.streamTimer != nil {
		s.streamTimer.Stop()
		s.logger.Info("stream timer stopped")
	}

	// Stop gateway timer
	if s.gatewayTimer != nil {
		s.gatewayTimer.Stop()
		s.logger.Info("gateway timer stopped")
	}

	// Stop negotiation timer
	if s.negotiationTimer != nil {
		s.negotiationTimer.Stop()
		s.logger.Info("negotiation timer stopped")
	}

	// Stop stakes distributor
	if s.stakesDistributor != nil {
		s.stakesDistributor.Stop()
		s.logger.Info("stakes distributor stopped")
	}

	// Stop reputation worker
	if s.reputationWorker != nil {
		s.reputationWorker.Stop()
		s.logger.Info("reputation worker stopped")
	}

	// Stop matview refresher
	if s.matviewRefresher != nil {
		s.matviewRefresher.Stop()
		s.logger.Info("matview refresher stopped")
	}

	// Stop partition maintainer
	if s.partitionMaint != nil {
		s.partitionMaint.Stop()
		s.logger.Info("partition maintainer stopped")
	}

	// Stop rate limiter cleanup goroutine
	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
		s.logger.Info("rate limiter stopped")
	}

	// Stop deposit watcher
	if s.depositWatcher != nil {
		s.depositWatcher.Stop()
		s.logger.Info("deposit watcher stopped")
	}

	// Flush tracing spans
	if s.tracerShutdown != nil {
		if err := s.tracerShutdown(ctx); err != nil {
			s.logger.Error("tracer shutdown error", "error", err)
		} else {
			s.logger.Info("tracer shutdown complete")
		}
	}

	// Close wallet connection
	if err := s.wallet.Close(); err != nil {
		s.logger.Error("wallet close error", "error", err)
	}

	// Close database connection pool
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			s.logger.Error("database close error", "error", err)
		} else {
			s.logger.Info("database connection closed")
		}
	}

	s.logger.Info("server stopped")
	return nil
}

// Router returns the gin router for testing
func (s *Server) Router() *gin.Engine {
	return s.router
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// appendDSNParams adds connect_timeout and statement_timeout to a PostgreSQL DSN.
func appendDSNParams(dsn string, connectTimeout, statementTimeout int) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return fmt.Sprintf("%s%sconnect_timeout=%d&statement_timeout=%d", dsn, sep, connectTimeout, statementTimeout)
	}
	// Key-value format
	return fmt.Sprintf("%s connect_timeout=%d statement_timeout=%d", dsn, connectTimeout, statementTimeout)
}

func (s *Server) timeoutMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Upgrade") == "websocket" {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), s.cfg.RequestTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

type gzipWriter struct {
	gin.ResponseWriter
	writer *gzip.Writer
}

func (w *gzipWriter) Write(data []byte) (int, error) {
	return w.writer.Write(data)
}

func (w *gzipWriter) WriteString(s string) (int, error) {
	return w.writer.Write([]byte(s))
}

func gzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") || c.GetHeader("Upgrade") == "websocket" {
			c.Next()
			return
		}
		gz, err := gzip.NewWriterLevel(c.Writer, gzip.DefaultCompression)
		if err != nil {
			c.Next()
			return
		}
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")
		c.Writer = &gzipWriter{ResponseWriter: c.Writer, writer: gz}
		defer func() {
			if err := gz.Close(); err != nil {
				_ = c.Error(err)
			}
			c.Header("Content-Length", "")
		}()
		c.Next()
	}
}

func cacheControl(maxAge int) gin.HandlerFunc {
	value := fmt.Sprintf("public, max-age=%d", maxAge)
	return func(c *gin.Context) {
		c.Header("Cache-Control", value)
		c.Next()
	}
}

func generateRequestID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based ID
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

// -----------------------------------------------------------------------------
// Session Key Adapters (for real on-chain execution)
// -----------------------------------------------------------------------------

// walletAdapter adapts wallet.WalletService to sessionkeys.WalletService
type walletAdapter struct {
	w wallet.WalletService
}

func (a *walletAdapter) Transfer(ctx context.Context, to common.Address, amount *big.Int) (*sessionkeys.TransferResult, error) {
	result, err := a.w.Transfer(ctx, to, amount)
	if err != nil {
		return nil, err
	}
	return &sessionkeys.TransferResult{
		TxHash: result.TxHash,
		From:   result.From,
		To:     result.To,
		Amount: result.Amount,
	}, nil
}

// registryAdapter adapts registry.Store to sessionkeys.TransactionRecorder
type registryAdapter struct {
	r registry.Store
}

func (a *registryAdapter) RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID string) error {
	tx := &registry.Transaction{
		TxHash:    txHash,
		From:      from,
		To:        to,
		Amount:    amount,
		ServiceID: serviceID,
		Status:    "pending", // Recorded as pending; confirmed by deposit watcher or verification
	}
	return a.r.RecordTransaction(ctx, tx)
}

// walletVerifierAdapter adapts wallet.WalletService to registry.TxVerifier
type walletVerifierAdapter struct {
	w wallet.WalletService
}

func (a *walletVerifierAdapter) VerifyPayment(ctx context.Context, from string, minAmount string, txHash string) (bool, error) {
	return a.w.VerifyPayment(ctx, from, minAmount, txHash)
}

// ledgerAdapter adapts ledger.Ledger to sessionkeys.BalanceService
type ledgerAdapter struct {
	l *ledger.Ledger
}

func (a *ledgerAdapter) CanSpend(ctx context.Context, agentAddr, amount string) (bool, error) {
	return a.l.CanSpend(ctx, agentAddr, amount)
}

func (a *ledgerAdapter) Spend(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Spend(ctx, agentAddr, amount, reference)
}

func (a *ledgerAdapter) Refund(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Refund(ctx, agentAddr, amount, reference)
}

func (a *ledgerAdapter) Deposit(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Deposit(ctx, agentAddr, amount, reference)
}

func (a *ledgerAdapter) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Hold(ctx, agentAddr, amount, reference)
}

func (a *ledgerAdapter) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ConfirmHold(ctx, agentAddr, amount, reference)
}

func (a *ledgerAdapter) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ReleaseHold(ctx, agentAddr, amount, reference)
}

// escrowLedgerAdapter adapts ledger.Ledger to escrow.LedgerService
type escrowLedgerAdapter struct {
	l *ledger.Ledger
}

func (a *escrowLedgerAdapter) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.EscrowLock(ctx, agentAddr, amount, reference)
}

func (a *escrowLedgerAdapter) ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	return a.l.ReleaseEscrow(ctx, buyerAddr, sellerAddr, amount, reference)
}

func (a *escrowLedgerAdapter) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.RefundEscrow(ctx, agentAddr, amount, reference)
}

// creditMetricsAdapter adapts reputation.RegistryProvider to credit.MetricsProvider
type creditMetricsAdapter struct {
	rep *reputation.RegistryProvider
}

func (a *creditMetricsAdapter) GetAgentMetrics(ctx context.Context, address string) (int, float64, int, float64, error) {
	metrics, err := a.rep.GetAgentMetrics(ctx, address)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	var successRate float64
	if metrics.TotalTransactions > 0 {
		successRate = float64(metrics.SuccessfulTxns) / float64(metrics.TotalTransactions)
	}
	return metrics.TotalTransactions, successRate, metrics.DaysOnNetwork, metrics.TotalVolumeUSD, nil
}

// creditLedgerAdapter adapts ledger.Ledger to credit.LedgerService.
type creditLedgerAdapter struct {
	l *ledger.Ledger
}

func (a *creditLedgerAdapter) GetCreditInfo(ctx context.Context, agentAddr string) (string, string, error) {
	return a.l.GetCreditInfo(ctx, agentAddr)
}

func (a *creditLedgerAdapter) SetCreditLimit(ctx context.Context, agentAddr, limit string) error {
	return a.l.SetCreditLimit(ctx, agentAddr, limit)
}

func (a *creditLedgerAdapter) RepayCredit(ctx context.Context, agentAddr, amount string) error {
	return a.l.RepayCredit(ctx, agentAddr, amount)
}

// streamLedgerAdapter adapts ledger.Ledger to streams.LedgerService
type streamLedgerAdapter struct {
	l *ledger.Ledger
}

func (a *streamLedgerAdapter) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Hold(ctx, agentAddr, amount, reference)
}

func (a *streamLedgerAdapter) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ConfirmHold(ctx, agentAddr, amount, reference)
}

func (a *streamLedgerAdapter) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ReleaseHold(ctx, agentAddr, amount, reference)
}

func (a *streamLedgerAdapter) Deposit(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Deposit(ctx, agentAddr, amount, reference)
}

// negotiationLedgerAdapter adapts ledger.Ledger to negotiation.LedgerService
type negotiationLedgerAdapter struct {
	l *ledger.Ledger
}

func (a *negotiationLedgerAdapter) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Hold(ctx, agentAddr, amount, reference)
}

func (a *negotiationLedgerAdapter) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ConfirmHold(ctx, agentAddr, amount, reference)
}

func (a *negotiationLedgerAdapter) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ReleaseHold(ctx, agentAddr, amount, reference)
}

func (a *negotiationLedgerAdapter) Deposit(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Deposit(ctx, agentAddr, amount, reference)
}

// registryChecker implements watcher.AgentChecker
type registryChecker struct {
	r registry.Store
}

func (c *registryChecker) IsAgent(ctx context.Context, address string) bool {
	agent, err := c.r.GetAgent(ctx, address)
	return err == nil && agent != nil
}

// withdrawalAdapter adapts wallet to ledger.WithdrawalExecutor
type withdrawalAdapter struct {
	w wallet.WalletService
}

func (a *withdrawalAdapter) Transfer(ctx context.Context, to common.Address, amount *big.Int) (string, error) {
	result, err := a.w.Transfer(ctx, to, amount)
	if err != nil {
		return "", err
	}
	return result.TxHash, nil
}

// TimelineItem represents an item in the unified timeline
type TimelineItem struct {
	Type      string      `json:"type"` // "transaction" or "comment"
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// getTimeline returns a unified feed of transactions + commentary
func (s *Server) getTimeline(c *gin.Context) {
	ctx := c.Request.Context()
	limit := 50

	var items []TimelineItem

	// Get recent transactions
	txs, err := s.registry.GetRecentTransactions(ctx, limit)
	if err == nil {
		for _, tx := range txs {
			items = append(items, TimelineItem{
				Type:      "transaction",
				Timestamp: tx.CreatedAt,
				Data:      tx,
			})
		}
	}

	// Get recent commentary if enabled
	if s.commentary != nil {
		comments, err := s.commentary.GetFeed(ctx, limit)
		if err == nil {
			for _, comment := range comments {
				items = append(items, TimelineItem{
					Type:      "comment",
					Timestamp: comment.CreatedAt,
					Data:      comment,
				})
			}
		}
	}

	// Get recent predictions if enabled
	if s.predictions != nil {
		preds, err := s.predictions.Store().List(ctx, predictions.ListOptions{Limit: limit})
		if err == nil {
			for _, pred := range preds {
				items = append(items, TimelineItem{
					Type:      "prediction",
					Timestamp: pred.CreatedAt,
					Data:      pred,
				})
			}
		}
	}

	// Sort by timestamp descending
	sortTimelineItems(items)

	// Limit to requested count
	if len(items) > limit {
		items = items[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"timeline": items,
		"count":    len(items),
	})
}

func sortTimelineItems(items []TimelineItem) {
	// Simple bubble sort for small lists
	for i := 0; i < len(items)-1; i++ {
		for j := 0; j < len(items)-i-1; j++ {
			if items[j].Timestamp.Before(items[j+1].Timestamp) {
				items[j], items[j+1] = items[j+1], items[j]
			}
		}
	}
}

// naturalLanguageSearch handles AI-powered service discovery
// GET/POST /v1/search?q=find me a cheap translator
func (s *Server) naturalLanguageSearch(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		// Try POST body
		var req struct {
			Query string `json:"query"`
		}
		if err := c.ShouldBindJSON(&req); err == nil {
			query = req.Query
		}
	}

	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "missing_query",
			"message": "Provide a search query with ?q= or {\"query\": \"...\"}",
			"examples": []string{
				"find me a cheap translator",
				"best rated inference service",
				"code review under $1",
			},
		})
		return
	}

	// Create discovery engine with registry adapter
	engine := discovery.NewEngine(&registryServiceProvider{s.registry})

	results, recommendation, err := engine.Recommend(c.Request.Context(), query, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "search_failed",
			"message": "Search failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"query":          query,
		"recommendation": recommendation,
		"results":        results,
		"count":          len(results),
	})
}

// registryServiceProvider adapts registry to discovery.ServiceProvider
type registryServiceProvider struct {
	r registry.Store
}

func (p *registryServiceProvider) ListAllServices(ctx context.Context) ([]discovery.SearchResult, error) {
	// Get all services from registry
	services, err := p.r.ListServices(ctx, registry.AgentQuery{Limit: 1000})
	if err != nil {
		return nil, err
	}

	var results []discovery.SearchResult
	for _, svc := range services {
		// Get agent stats for reputation
		agent, _ := p.r.GetAgent(ctx, svc.AgentAddress)

		var rep float64 = 50 // Default
		var successRate = 0.95
		var txCount int

		if agent != nil {
			rep = float64(agent.Stats.TransactionCount) / 10 // Simple reputation calc
			if rep > 100 {
				rep = 100
			}
			successRate = agent.Stats.SuccessRate
			txCount = int(agent.Stats.TransactionCount)
		}

		var priceFloat float64
		if svc.Price != "" {
			_, _ = fmt.Sscanf(svc.Price, "%f", &priceFloat)
		}

		results = append(results, discovery.SearchResult{
			ServiceID:    svc.ID,
			ServiceName:  svc.Name,
			ServiceType:  svc.Type,
			AgentAddress: svc.AgentAddress,
			AgentName:    svc.AgentName,
			Price:        svc.Price,
			PriceFloat:   priceFloat,
			Reputation:   rep,
			SuccessRate:  successRate,
			TxCount:      txCount,
		})
	}

	return results, nil
}

// realtimeEventEmitter adapts realtime.Hub to sessionkeys.EventEmitter
type realtimeEventEmitter struct {
	hub *realtime.Hub
}

func (e *realtimeEventEmitter) EmitTransaction(tx map[string]interface{}) {
	if e.hub != nil {
		e.hub.BroadcastTransaction(tx)
	}
}

func (e *realtimeEventEmitter) EmitSessionKeyUsed(keyID, agentAddr, amount string) {
	if e.hub != nil {
		e.hub.Broadcast(&realtime.Event{
			Type:      realtime.EventTransaction,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"sessionKeyId": keyID,
				"agentAddr":    agentAddr,
				"amount":       amount,
				"event":        "session_key_used",
			},
		})
	}
}

// commentaryEventEmitter adapts realtime.Hub to commentary.CommentEventEmitter
type commentaryEventEmitter struct {
	hub *realtime.Hub
}

func (e *commentaryEventEmitter) EmitComment(comment map[string]interface{}) {
	if e.hub != nil {
		e.hub.BroadcastComment(comment)
	}
}

// registryMetricProvider implements predictions.MetricProvider using registry data
type registryMetricProvider struct {
	r registry.Store
}

func (p *registryMetricProvider) GetAgentMetric(ctx context.Context, agentAddr, metric string) (float64, error) {
	agent, err := p.r.GetAgent(ctx, agentAddr)
	if err != nil {
		return 0, err
	}

	switch metric {
	case "tx_count", "transaction_count":
		return float64(agent.Stats.TransactionCount), nil
	case "success_rate":
		return agent.Stats.SuccessRate, nil
	case "volume", "total_volume":
		// Parse TotalReceived string to float
		var volume float64
		if agent.Stats.TotalReceived != "" {
			_, _ = fmt.Sscanf(agent.Stats.TotalReceived, "%f", &volume)
		}
		return volume, nil
	default:
		return 0, errors.New("unknown metric: " + metric)
	}
}

func (p *registryMetricProvider) GetServiceTypeMetric(ctx context.Context, serviceType, metric string) (float64, error) {
	// Get aggregate metrics for a service type
	services, err := p.r.ListServices(ctx, registry.AgentQuery{ServiceType: serviceType, Limit: 1000})
	if err != nil {
		return 0, err
	}

	switch metric {
	case "count":
		return float64(len(services)), nil
	case "avg_price":
		if len(services) == 0 {
			return 0, nil
		}
		var total float64
		for _, svc := range services {
			var price float64
			_, _ = fmt.Sscanf(svc.Price, "%f", &price)
			total += price
		}
		return total / float64(len(services)), nil
	default:
		return 0, errors.New("unknown metric: " + metric)
	}
}

func (p *registryMetricProvider) GetMarketMetric(ctx context.Context, metric string) (float64, error) {
	stats, err := p.r.GetNetworkStats(ctx)
	if err != nil {
		return 0, err
	}

	switch metric {
	case "total_agents":
		return float64(stats.TotalAgents), nil
	case "total_services":
		return float64(stats.TotalServices), nil
	case "total_transactions":
		return float64(stats.TotalTransactions), nil
	case "total_volume":
		// Parse TotalVolume string to float
		var volume float64
		if stats.TotalVolume != "" {
			_, _ = fmt.Sscanf(stats.TotalVolume, "%f", &volume)
		}
		return volume, nil
	default:
		return 0, errors.New("unknown metric: " + metric)
	}
}

// revenueAccumulatorAdapter adapts stakes.Service to the RevenueAccumulator
// interface used by sessionkeys, escrow, and streams.
type revenueAccumulatorAdapter struct {
	stakes *stakes.Service
}

func (a *revenueAccumulatorAdapter) AccumulateRevenue(ctx context.Context, agentAddr, amount, txRef string) error {
	return a.stakes.AccumulateRevenue(ctx, agentAddr, amount, txRef)
}

// stakesLedgerAdapter adapts ledger.Ledger to stakes.LedgerService
type stakesLedgerAdapter struct {
	l *ledger.Ledger
}

func (a *stakesLedgerAdapter) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.EscrowLock(ctx, agentAddr, amount, reference)
}

func (a *stakesLedgerAdapter) ReleaseEscrow(ctx context.Context, fromAddr, toAddr, amount, reference string) error {
	return a.l.ReleaseEscrow(ctx, fromAddr, toAddr, amount, reference)
}

func (a *stakesLedgerAdapter) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.RefundEscrow(ctx, agentAddr, amount, reference)
}

func (a *stakesLedgerAdapter) Deposit(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Deposit(ctx, agentAddr, amount, reference)
}

func (a *stakesLedgerAdapter) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Hold(ctx, agentAddr, amount, reference)
}

func (a *stakesLedgerAdapter) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ConfirmHold(ctx, agentAddr, amount, reference)
}

func (a *stakesLedgerAdapter) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ReleaseHold(ctx, agentAddr, amount, reference)
}

// contractFormerAdapter adapts contracts.Service to negotiation.ContractFormer
type contractFormerAdapter struct {
	contracts *contracts.Service
}

func (a *contractFormerAdapter) FormContract(ctx context.Context, rfp *negotiation.RFP, bid *negotiation.Bid) (string, error) {
	contract, err := a.contracts.Propose(ctx, contracts.ProposeRequest{
		BuyerAddr:      rfp.BuyerAddr,
		SellerAddr:     bid.SellerAddr,
		ServiceType:    rfp.ServiceType,
		PricePerCall:   bid.PricePerCall,
		BuyerBudget:    bid.TotalBudget,
		Duration:       bid.Duration,
		MinVolume:      rfp.MinVolume,
		SellerPenalty:  bid.SellerPenalty,
		MaxLatencyMs:   bid.MaxLatencyMs,
		MinSuccessRate: bid.SuccessRate,
	})
	if err != nil {
		return "", fmt.Errorf("failed to propose contract: %w", err)
	}

	// Auto-accept on behalf of the seller (they agreed via their bid)
	_, err = a.contracts.Accept(ctx, contract.ID, bid.SellerAddr)
	if err != nil {
		return contract.ID, fmt.Errorf("contract proposed but accept failed: %w", err)
	}

	return contract.ID, nil
}

// gatewayRecorderAdapter adapts registry.Store to gateway.TransactionRecorder
type gatewayRecorderAdapter struct {
	r registry.Store
}

func (a *gatewayRecorderAdapter) RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID, status string) error {
	tx := &registry.Transaction{
		TxHash:    txHash,
		From:      from,
		To:        to,
		Amount:    amount,
		ServiceID: serviceID,
		Status:    status,
	}
	return a.r.RecordTransaction(ctx, tx)
}

// gatewayLedgerAdapter adapts ledger.Ledger to gateway.LedgerService
type gatewayLedgerAdapter struct {
	l *ledger.Ledger
}

func (a *gatewayLedgerAdapter) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Hold(ctx, agentAddr, amount, reference)
}

func (a *gatewayLedgerAdapter) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ConfirmHold(ctx, agentAddr, amount, reference)
}

func (a *gatewayLedgerAdapter) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ReleaseHold(ctx, agentAddr, amount, reference)
}

func (a *gatewayLedgerAdapter) Deposit(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Deposit(ctx, agentAddr, amount, reference)
}

// gatewayRegistryAdapter adapts registry.Store to gateway.RegistryProvider
type gatewayRegistryAdapter struct {
	store registry.Store
}

func (a *gatewayRegistryAdapter) ListServices(ctx context.Context, serviceType, maxPrice string) ([]gateway.ServiceCandidate, error) {
	active := true
	query := registry.AgentQuery{
		ServiceType: serviceType,
		MaxPrice:    maxPrice,
		Active:      &active,
		Limit:       10,
	}

	listings, err := a.store.ListServices(ctx, query)
	if err != nil {
		return nil, err
	}

	var candidates []gateway.ServiceCandidate
	for _, l := range listings {
		endpoint := l.Endpoint
		candidates = append(candidates, gateway.ServiceCandidate{
			AgentAddress:    l.AgentAddress,
			AgentName:       l.AgentName,
			ServiceID:       l.ID,
			ServiceName:     l.Name,
			Price:           l.Price,
			Endpoint:        endpoint,
			ReputationScore: l.ReputationScore,
		})
	}
	return candidates, nil
}

// webhookAlertNotifier adapts webhooks.Dispatcher to sessionkeys.AlertNotifier.
type webhookAlertNotifier struct {
	d *webhooks.Dispatcher
}

func (n *webhookAlertNotifier) NotifyAlert(ctx context.Context, alert sessionkeys.AlertEvent) error {
	eventType := webhooks.EventSessionKeyBudgetWarning
	if alert.Type == "expiring" {
		eventType = webhooks.EventSessionKeyExpiring
	}
	return n.d.DispatchToAgent(ctx, alert.OwnerAddr, &webhooks.Event{
		ID:        alert.KeyID + ":" + alert.Type,
		Type:      eventType,
		Timestamp: alert.TriggeredAt,
		Data: map[string]interface{}{
			"keyId":      alert.KeyID,
			"ownerAddr":  alert.OwnerAddr,
			"threshold":  alert.Threshold,
			"usedPct":    alert.UsedPct,
			"totalSpent": alert.TotalSpent,
			"maxTotal":   alert.MaxTotal,
			"expiresAt":  alert.ExpiresAt,
			"expiresIn":  alert.ExpiresIn,
		},
	})
}

// verifiedLedgerAdapter adapts ledger.Ledger to verified.LedgerService
type verifiedLedgerAdapter struct {
	l *ledger.Ledger
}

func (a *verifiedLedgerAdapter) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Hold(ctx, agentAddr, amount, reference)
}

func (a *verifiedLedgerAdapter) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ConfirmHold(ctx, agentAddr, amount, reference)
}

func (a *verifiedLedgerAdapter) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ReleaseHold(ctx, agentAddr, amount, reference)
}

func (a *verifiedLedgerAdapter) Deposit(ctx context.Context, agentAddr, amount, txHash string) error {
	return a.l.Deposit(ctx, agentAddr, amount, txHash)
}

// contractCallAdapter adapts contracts.Service to verified.ContractCallProvider
type contractCallAdapter struct {
	svc *contracts.Service
}

func (a *contractCallAdapter) GetRecentCallsByAgent(ctx context.Context, agentAddr string, windowSize int) (int, int, error) {
	// Get all active contracts where this agent is the seller
	allContracts, err := a.svc.ListByAgent(ctx, agentAddr, "active", 100)
	if err != nil {
		return 0, 0, err
	}

	successCount := 0
	totalCount := 0
	for _, c := range allContracts {
		if c.SellerAddr != agentAddr {
			continue
		}
		calls, err := a.svc.ListCalls(ctx, c.ID, windowSize)
		if err != nil {
			continue
		}
		for _, call := range calls {
			totalCount++
			if call.Status == "success" {
				successCount++
			}
			if totalCount >= windowSize {
				return successCount, totalCount, nil
			}
		}
	}
	return successCount, totalCount, nil
}

// buyerPaymentAdapter adapts contracts.Service to verified.BuyerPaymentProvider
type buyerPaymentAdapter struct {
	svc *contracts.Service
}

func (a *buyerPaymentAdapter) GetRecentBuyerPayments(ctx context.Context, sellerAddr string, windowSize int) ([]verified.BuyerPayment, error) {
	allContracts, err := a.svc.ListByAgent(ctx, sellerAddr, "active", 100)
	if err != nil {
		return nil, err
	}

	// Aggregate payments per buyer across all contracts where this agent is seller
	buyerTotals := make(map[string]float64)
	for _, c := range allContracts {
		if c.SellerAddr != strings.ToLower(sellerAddr) {
			continue
		}
		calls, err := a.svc.ListCalls(ctx, c.ID, windowSize)
		if err != nil {
			continue
		}
		pricePerCall, _ := new(big.Float).SetString(c.PricePerCall)
		if pricePerCall == nil {
			continue
		}
		pf, _ := pricePerCall.Float64()
		for _, call := range calls {
			if call.Status == "success" {
				buyerTotals[c.BuyerAddr] += pf
			}
		}
	}

	var result []verified.BuyerPayment
	for addr, amount := range buyerTotals {
		result = append(result, verified.BuyerPayment{
			BuyerAddr: addr,
			Amount:    amount,
		})
	}
	return result, nil
}

// gatewayVerificationAdapter adapts verified.Service to gateway.VerificationChecker
type gatewayVerificationAdapter struct {
	svc *verified.Service
}

func (a *gatewayVerificationAdapter) IsVerified(ctx context.Context, agentAddr string) (bool, error) {
	return a.svc.IsVerified(ctx, agentAddr)
}

func (a *gatewayVerificationAdapter) GetGuarantee(ctx context.Context, agentAddr string) (float64, float64, error) {
	return a.svc.GetGuarantee(ctx, agentAddr)
}

// gatewayContractAdapter adapts contracts.Service to gateway.ContractManager
type gatewayContractAdapter struct {
	svc *contracts.Service
}

func (a *gatewayContractAdapter) EnsureContract(ctx context.Context, buyerAddr, sellerAddr, serviceType, pricePerCall string, guaranteedSuccessRate float64, slaWindowSize int) (string, error) {
	// Look for an existing active contract between this buyer and seller for this service type
	existing, err := a.svc.ListByAgent(ctx, sellerAddr, "active", 50)
	if err == nil {
		for _, c := range existing {
			if c.BuyerAddr == strings.ToLower(buyerAddr) && c.ServiceType == serviceType {
				return c.ID, nil
			}
		}
	}

	// No existing contract — create one with auto-accept
	contract, err := a.svc.Propose(ctx, contracts.ProposeRequest{
		BuyerAddr:      buyerAddr,
		SellerAddr:     sellerAddr,
		ServiceType:    serviceType,
		PricePerCall:   pricePerCall,
		BuyerBudget:    "100.000000", // default budget for auto-contracts
		Duration:       "168h",       // 7 days
		MinVolume:      1,
		MinSuccessRate: guaranteedSuccessRate,
		SLAWindowSize:  slaWindowSize,
	})
	if err != nil {
		return "", fmt.Errorf("auto-propose contract: %w", err)
	}

	// Auto-accept on behalf of the seller (they agreed by being verified)
	if _, err := a.svc.Accept(ctx, contract.ID, sellerAddr); err != nil {
		return contract.ID, fmt.Errorf("auto-accept contract: %w", err)
	}

	return contract.ID, nil
}

func (a *gatewayContractAdapter) RecordCall(ctx context.Context, contractID string, status string, latencyMs int) error {
	// Use the seller address as caller — the gateway acts on behalf of the buyer/seller pair
	contract, err := a.svc.Get(ctx, contractID)
	if err != nil {
		return err
	}

	_, err = a.svc.RecordCall(ctx, contractID, contracts.RecordCallRequest{
		Status:    status,
		LatencyMs: latencyMs,
	}, contract.BuyerAddr)
	return err
}
