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
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/config"
	"github.com/mbd888/alancoin/internal/dashboard"
	"github.com/mbd888/alancoin/internal/escrow"
	"github.com/mbd888/alancoin/internal/gateway"
	"github.com/mbd888/alancoin/internal/ledger"
	"github.com/mbd888/alancoin/internal/logging"
	"github.com/mbd888/alancoin/internal/metrics"
	"github.com/mbd888/alancoin/internal/policy"
	"github.com/mbd888/alancoin/internal/ratelimit"
	"github.com/mbd888/alancoin/internal/realtime"
	"github.com/mbd888/alancoin/internal/receipts"
	"github.com/mbd888/alancoin/internal/registry"
	"github.com/mbd888/alancoin/internal/reputation"
	"github.com/mbd888/alancoin/internal/security"
	"github.com/mbd888/alancoin/internal/sessionkeys"
	"github.com/mbd888/alancoin/internal/streams"
	"github.com/mbd888/alancoin/internal/supervisor"
	"github.com/mbd888/alancoin/internal/tenant"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/validation"
	"github.com/mbd888/alancoin/internal/webhooks"
)

// -----------------------------------------------------------------------------
// Server
// -----------------------------------------------------------------------------

// Server wraps the HTTP server and dependencies
type Server struct {
	cfg                    *config.Config
	registry               registry.Store
	sessionMgr             *sessionkeys.Manager
	authMgr                *auth.Manager
	ledger                 *ledger.Ledger
	ledgerService          ledger.Service // supervised access for payment paths
	webhooks               *webhooks.Dispatcher
	realtimeHub            *realtime.Hub
	escrowService          *escrow.Service
	escrowTimer            *escrow.Timer
	multiStepEscrowService *escrow.MultiStepService
	streamService          *streams.Service
	streamTimer            *streams.Timer
	gatewayService         *gateway.Service
	gatewayTimer           *gateway.Timer
	receiptService         *receipts.Service
	reputationStore        reputation.SnapshotStore
	reputationWorker       *reputation.Worker
	reputationSigner       *reputation.Signer
	matviewRefresher       *registry.MatviewRefresher
	partitionMaint         *registry.PartitionMaintainer
	rateLimiter            *ratelimit.Limiter
	baselineTimer          *supervisor.BaselineTimer
	eventWriter            *supervisor.EventWriter
	tenantStore            tenant.Store
	policyStore            policy.Store  // tenant-scoped spend policies
	gatewayStore           gateway.Store // for billing aggregation
	db                     *sql.DB       // nil if using in-memory
	router                 *gin.Engine
	httpSrv                *http.Server
	logger                 *slog.Logger
	cancelRunCtx           context.CancelFunc // cancels background goroutines started in Run
	tracerShutdown         func(context.Context) error

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
		delegationAuditLogger := sessionkeys.NewPostgresAuditLogger(db)
		s.sessionMgr = sessionkeys.NewManager(sessionStore, nil, policyStore).
			WithDelegationAuditLogger(delegationAuditLogger)

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
		s.ledger = ledger.NewWithEvents(ledgerStore, eventStore).
			WithAuditLogger(auditLogger)
		baselineStore := supervisor.NewPostgresBaselineStore(db)
		s.ledgerService = supervisor.New(s.ledger,
			supervisor.WithLogger(s.logger),
			supervisor.WithBaselineStore(baselineStore),
		)
		s.eventWriter = supervisor.NewEventWriter(baselineStore, s.logger)
		if sv, ok := s.ledgerService.(*supervisor.Supervisor); ok {
			sv.SetEventWriter(s.eventWriter)
			s.baselineTimer = supervisor.NewBaselineTimer(baselineStore, sv, s.logger)
		}
		s.logger.Info("agent balance tracking enabled (with baselines)")

		// Webhooks with Postgres
		webhookStore := webhooks.NewPostgresStore(db)
		if err := webhookStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate webhook store", "error", err)
		}
		s.webhooks = webhooks.NewDispatcher(webhookStore)
		s.logger.Info("webhooks enabled")

		// Escrow with PostgreSQL store
		escrowStore := escrow.NewPostgresStore(db)
		s.escrowService = escrow.NewService(escrowStore, &escrowLedgerAdapter{s.ledgerService}).WithLogger(s.logger)
		s.escrowTimer = escrow.NewTimer(s.escrowService, escrowStore, s.logger)
		s.multiStepEscrowService = escrow.NewMultiStepService(
			escrow.NewMultiStepPostgresStore(db), &escrowLedgerAdapter{s.ledgerService},
		).WithLogger(s.logger)
		s.logger.Info("escrow enabled (postgres)")

		// Streams with PostgreSQL store (streaming micropayments)
		streamStore := streams.NewPostgresStore(db)
		s.streamService = streams.NewService(streamStore, &streamLedgerAdapter{s.ledgerService})
		s.streamTimer = streams.NewTimer(s.streamService, streamStore, s.logger)
		s.logger.Info("streams enabled (postgres)")

		// Receipt signing (cryptographic payment proofs)
		receiptStore := receipts.NewPostgresStore(db)
		if err := receiptStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate receipt store", "error", err)
		}
		receiptSigner := receipts.NewSigner(cfg.ReceiptHMACSecret)
		s.receiptService = receipts.NewService(receiptStore, receiptSigner)
		s.logger.Info("receipt signing enabled (postgres)")

		// Gateway with PostgreSQL store (transparent payment proxy)
		gwStore := gateway.NewPostgresStore(db)
		gwResolver := gateway.NewResolver(&gatewayRegistryAdapter{s.registry})
		gwForwarder := gateway.NewForwarder(0)
		gwLedger := &gatewayLedgerAdapter{s.ledgerService}
		s.gatewayStore = gwStore
		s.gatewayService = gateway.NewService(gwStore, gwResolver, gwForwarder, gwLedger, s.logger)
		s.gatewayTimer = gateway.NewTimer(s.gatewayService, gwStore, s.logger)
		s.logger.Info("gateway enabled (postgres)")

		// Release any ledger holds orphaned by a previous crash.
		gateway.ReconcileOrphanedHolds(ctx, db, gwLedger, s.logger)

		s.gatewayService.WithRecorder(&gatewayRecorderAdapter{s.registry})
		s.gatewayService.WithPlatformAddress(cfg.PlatformAddress)

		// Wire receipt issuer into all payment paths
		if s.receiptService != nil {
			rcptAdapter := &receiptIssuerAdapter{s.receiptService}
			s.gatewayService.WithReceiptIssuer(rcptAdapter)
			s.streamService.WithReceiptIssuer(rcptAdapter)
			s.escrowService.WithReceiptIssuer(rcptAdapter)
		}

		// Tenant store (PostgreSQL)
		tenantPgStore := tenant.NewPostgresStore(db)
		if err := tenantPgStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate tenant store", "error", err)
		}
		s.tenantStore = tenantPgStore
		s.logger.Info("tenant store enabled (postgres)")

		// Wire tenant settings into gateway for fee computation
		s.gatewayService.WithTenantSettings(&gatewayTenantSettingsAdapter{s.tenantStore})

		// Spend policies (PostgreSQL)
		spendPolicyStore := policy.NewPostgresStore(db)
		if err := spendPolicyStore.Migrate(ctx); err != nil {
			s.logger.Warn("failed to migrate spend policy store", "error", err)
		}
		s.gatewayService.WithPolicyEvaluator(policy.NewEvaluator(spendPolicyStore))
		s.policyStore = spendPolicyStore
		s.logger.Info("spend policies enabled (postgres)")

		// Reputation snapshots (PostgreSQL)
		s.reputationStore = reputation.NewPostgresSnapshotStore(db)
		s.logger.Info("reputation snapshots enabled (postgres)")

	} else {
		s.registry = registry.NewMemoryStore()
		s.logger.Info("using in-memory storage (data will not persist)")

		// Session keys with in-memory store
		sessionStore := sessionkeys.NewMemoryStore()
		policyStore := sessionkeys.NewPolicyMemoryStore()
		memDelegationAuditLogger := sessionkeys.NewMemoryAuditLogger()
		s.sessionMgr = sessionkeys.NewManager(sessionStore, nil, policyStore).
			WithDelegationAuditLogger(memDelegationAuditLogger)

		// API keys with in-memory store
		s.authMgr = auth.NewManager(auth.NewMemoryStore())

		// In-memory ledger for demo mode
		memStore := ledger.NewMemoryStore()
		memEventStore := ledger.NewMemoryEventStore()
		memAuditLogger := ledger.NewMemoryAuditLogger()
		s.ledger = ledger.NewWithEvents(memStore, memEventStore).
			WithAuditLogger(memAuditLogger)
		s.ledgerService = supervisor.New(s.ledger, supervisor.WithLogger(s.logger))
		s.logger.Info("agent balance tracking enabled (in-memory)")

		// Webhooks with in-memory store
		s.webhooks = webhooks.NewDispatcher(webhooks.NewMemoryStore())

		// Escrow with in-memory store
		escrowStore := escrow.NewMemoryStore()
		s.escrowService = escrow.NewService(escrowStore, &escrowLedgerAdapter{s.ledgerService}).WithLogger(s.logger)
		s.escrowTimer = escrow.NewTimer(s.escrowService, escrowStore, s.logger)
		s.multiStepEscrowService = escrow.NewMultiStepService(
			escrow.NewMultiStepMemoryStore(), &escrowLedgerAdapter{s.ledgerService},
		).WithLogger(s.logger)
		s.logger.Info("escrow enabled (in-memory)")

		// Streams with in-memory store (streaming micropayments)
		streamStore := streams.NewMemoryStore()
		s.streamService = streams.NewService(streamStore, &streamLedgerAdapter{s.ledgerService})
		s.streamTimer = streams.NewTimer(s.streamService, streamStore, s.logger)
		s.logger.Info("streams enabled (in-memory)")

		// Receipt signing (cryptographic payment proofs, in-memory)
		receiptSigner := receipts.NewSigner(cfg.ReceiptHMACSecret)
		s.receiptService = receipts.NewService(receipts.NewMemoryStore(), receiptSigner)
		s.logger.Info("receipt signing enabled (in-memory)")

		// Gateway with in-memory store (transparent payment proxy)
		gwStore2 := gateway.NewMemoryStore()
		gwResolver2 := gateway.NewResolver(&gatewayRegistryAdapter{s.registry})
		gwForwarder2 := gateway.NewForwarder(0)
		s.gatewayStore = gwStore2
		s.gatewayService = gateway.NewService(gwStore2, gwResolver2, gwForwarder2, &gatewayLedgerAdapter{s.ledgerService}, s.logger)
		s.gatewayTimer = gateway.NewTimer(s.gatewayService, gwStore2, s.logger)
		s.logger.Info("gateway enabled (in-memory)")

		s.gatewayService.WithRecorder(&gatewayRecorderAdapter{s.registry})
		s.gatewayService.WithPlatformAddress(cfg.PlatformAddress)

		// Wire receipt issuer into all payment paths
		if s.receiptService != nil {
			rcptAdapter := &receiptIssuerAdapter{s.receiptService}
			s.gatewayService.WithReceiptIssuer(rcptAdapter)
			s.streamService.WithReceiptIssuer(rcptAdapter)
			s.escrowService.WithReceiptIssuer(rcptAdapter)
		}

		// Tenant store (in-memory)
		s.tenantStore = tenant.NewMemoryStore()
		s.logger.Info("tenant store enabled (in-memory)")

		// Wire tenant settings into gateway for fee computation
		s.gatewayService.WithTenantSettings(&gatewayTenantSettingsAdapter{s.tenantStore})

		// Spend policies (in-memory)
		memPolicyStore := policy.NewMemoryStore()
		s.gatewayService.WithPolicyEvaluator(policy.NewEvaluator(memPolicyStore))
		s.policyStore = memPolicyStore
		s.logger.Info("spend policies enabled (in-memory)")

		// Reputation snapshots (in-memory)
		s.reputationStore = reputation.NewMemorySnapshotStore()
		s.logger.Info("reputation snapshots enabled (in-memory)")

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
		RequestsPerMinute: s.cfg.RateLimitRPM,
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

	// WebSocket for real-time streaming
	s.router.GET("/ws", func(c *gin.Context) {
		s.realtimeHub.HandleWebSocket(c.Writer, c.Request)
	})

	// API info endpoints
	s.router.GET("/api", s.infoHandler)

	// V1 API group
	v1 := s.router.Group("/v1")
	// Validate :address URL params on all v1 routes (no-op when param absent)
	v1.Use(validation.AddressParamMiddleware())
	registryHandler := registry.NewHandler(s.registry)

	// Wire reputation into discovery so agents see trust scores when searching
	reputationProvider := reputation.NewRegistryProvider(s.registry)
	registryHandler.SetReputation(reputationProvider)

	// Wire reputation into supervisor so spending rules are tier-aware
	if sv, ok := s.ledgerService.(*supervisor.Supervisor); ok {
		sv.SetReputation(reputationProvider)
	}

	// Wire reputation impact tracking into escrow (dispute/confirm outcomes)
	s.escrowService.WithReputationImpactor(reputationProvider)

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
	sessionHandler := sessionkeys.NewHandler(s.sessionMgr, s.logger)

	// Always use demo mode (ledger-only accounting, no on-chain transfers)
	sessionHandler = sessionHandler.WithDemoMode()

	// Add real-time event emitter
	if s.realtimeHub != nil {
		sessionHandler = sessionHandler.WithEvents(&realtimeEventEmitter{s.realtimeHub})
	}

	// Add budget/expiration alert checker backed by webhooks
	if s.webhooks != nil {
		alertChecker := sessionkeys.NewAlertChecker(&webhookAlertNotifier{d: s.webhooks})
		sessionHandler = sessionHandler.WithAlertChecker(alertChecker)
	}

	// Add receipt issuer for cryptographic payment proofs
	if s.receiptService != nil {
		sessionHandler = sessionHandler.WithReceiptIssuer(&receiptIssuerAdapter{s.receiptService})
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
	v1.GET("/sessions/:keyId/delegation-log", sessionHandler.GetDelegationLog)

	// =========================================================================
	// Gateway — transparent payment proxy (the primary path for AI agents)
	// One call: discover -> pay -> forward -> settle -> receipt -> reputation
	// =========================================================================
	if s.gatewayService != nil {
		gatewayHandler := gateway.NewHandler(s.gatewayService)

		// Protected routes - session CRUD requires API key auth
		protectedGateway := v1.Group("")
		protectedGateway.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		gatewayHandler.RegisterProtectedRoutes(protectedGateway)

		// Proxy route - requires both API key auth AND gateway token.
		// API key verifies caller identity, gateway token authorizes session access.
		protectedProxy := v1.Group("")
		protectedProxy.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		gatewayHandler.RegisterProxyRoute(protectedProxy)
	}

	// =========================================================================
	// Tenant management — multi-tenancy CRUD + agent binding
	// =========================================================================
	if s.tenantStore != nil {
		tenantHandler := tenant.NewHandler(s.tenantStore, s.authMgr, s.registry)
		if s.gatewayStore != nil {
			tenantHandler.WithBilling(&gatewayBillingAdapter{s.gatewayStore})
		}

		// Admin routes: create tenant (requires admin secret)
		adminTenants := v1.Group("")
		adminTenants.Use(auth.Middleware(s.authMgr), auth.RequireAdmin())
		tenantHandler.RegisterAdminRoutes(adminTenants)

		// Protected routes: tenant CRUD, agent binding, key management (requires API key)
		protectedTenants := v1.Group("")
		protectedTenants.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		tenantHandler.RegisterProtectedRoutes(protectedTenants)

		// Policy routes: spend policy CRUD under tenant-protected group
		if s.policyStore != nil {
			policyHandler := policy.NewHandler(s.policyStore)
			policyHandler.RegisterRoutes(protectedTenants)
		}

		// Dashboard analytics routes (tenant-scoped)
		if s.gatewayStore != nil {
			dashHandler := dashboard.NewHandler(s.gatewayStore, s.tenantStore)
			dashHandler.RegisterRoutes(protectedTenants)
		}
	}

	// Ledger routes (agent balances)
	if s.ledger != nil {
		ledgerHandler := ledger.NewHandler(s.ledger, s.logger)
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

	// Escrow routes (buyer protection for service payments)
	if s.escrowService != nil {
		escrowHandler := escrow.NewHandler(s.escrowService)
		if s.sessionMgr != nil {
			escrowHandler = escrowHandler.WithScopeChecker(s.sessionMgr)
		}

		// Public routes - anyone can read
		escrowHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can create/confirm/dispute
		protectedEscrow := v1.Group("")
		protectedEscrow.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		escrowHandler.RegisterProtectedRoutes(protectedEscrow)
	}

	// MultiStep escrow routes (atomic N-step pipeline payments)
	if s.multiStepEscrowService != nil {
		msHandler := escrow.NewMultiStepHandler(s.multiStepEscrowService)

		msHandler.RegisterRoutes(v1)

		protectedMS := v1.Group("")
		protectedMS.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		msHandler.RegisterProtectedRoutes(protectedMS)
	}

	// Streaming micropayment routes (per-tick payments for continuous services)
	if s.streamService != nil {
		streamHandler := streams.NewHandler(s.streamService)
		if s.sessionMgr != nil {
			streamHandler = streamHandler.WithScopeChecker(s.sessionMgr)
		}

		// Public routes - anyone can read
		streamHandler.RegisterRoutes(v1)

		// Protected routes - only authenticated agents can open/tick/close
		protectedStreams := v1.Group("")
		protectedStreams.Use(auth.Middleware(s.authMgr), auth.RequireAuth(s.authMgr))
		streamHandler.RegisterProtectedRoutes(protectedStreams)
	}

	// Receipt routes (cryptographic payment proofs — public, read-only)
	if s.receiptService != nil {
		receiptHandler := receipts.NewHandler(s.receiptService)
		receiptHandler.RegisterRoutes(v1)
	}

	// Timeline feed
	v1.GET("/timeline", s.getTimeline)
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

	// DB check
	if s.db != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			checks["database"] = "unhealthy"
		} else {
			checks["database"] = "healthy"
		}
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
	checks["gateway_timer"] = timerStatus(s.gatewayTimer)
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

func (s *Server) infoHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"name":        "Alancoin",
		"description": "Payment infrastructure for AI agents",
		"version":     "0.1.0",
		"chain":       "base-sepolia",
		"currency":    "USDC",
	})
}

// platformHandler returns platform info
func (s *Server) platformHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"platform": gin.H{
			"name":    "Alancoin",
			"version": "0.1.0",
		},
		"instructions": gin.H{
			"deposit": "POST /v1/admin/deposits with admin auth",
			"spend":   "Create a session key, then POST to /v1/agents/{address}/sessions/{keyId}/transact",
			"gateway": "POST /v1/gateway/sessions to create a gateway session",
		},
	})
}

// enhancedStatsHandler returns extended network stats for demos
// Aggregates data from multiple sources: registry, session keys, gas
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
		)
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	// Start realtime hub
	if s.realtimeHub != nil {
		go s.realtimeHub.Run(runCtx)
	}

	// Start escrow auto-release timer
	if s.escrowTimer != nil {
		go s.escrowTimer.Start(runCtx)
	}

	// Start stream stale-close timer
	if s.streamTimer != nil {
		go s.streamTimer.Start(runCtx)
	}

	// Start gateway session expiry timer
	if s.gatewayTimer != nil {
		go s.gatewayTimer.Start(runCtx)
	}

	// Start baseline learning event writer and timer
	if s.eventWriter != nil {
		go s.eventWriter.Start(runCtx)
	}
	if s.baselineTimer != nil {
		go s.baselineTimer.Start(runCtx)
	}

	// Start reputation snapshot worker
	if s.reputationWorker != nil {
		go s.reputationWorker.Start(runCtx)
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

	// Stop baseline learning components
	if s.eventWriter != nil {
		s.eventWriter.Stop()
		s.logger.Info("event writer stopped")
	}
	if s.baselineTimer != nil {
		s.baselineTimer.Stop()
		s.logger.Info("baseline timer stopped")
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

	// Flush tracing spans
	if s.tracerShutdown != nil {
		if err := s.tracerShutdown(ctx); err != nil {
			s.logger.Error("tracer shutdown error", "error", err)
		} else {
			s.logger.Info("tracer shutdown complete")
		}
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

// escrowLedgerAdapter adapts ledger.Service to escrow.LedgerService
type escrowLedgerAdapter struct {
	l ledger.Service
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

func (a *escrowLedgerAdapter) PartialEscrowSettle(ctx context.Context, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference string) error {
	return a.l.PartialEscrowSettle(ctx, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference)
}

// streamLedgerAdapter adapts ledger.Service to streams.LedgerService
type streamLedgerAdapter struct {
	l ledger.Service
}

func (a *streamLedgerAdapter) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Hold(ctx, agentAddr, amount, reference)
}

func (a *streamLedgerAdapter) SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	return a.l.SettleHold(ctx, buyerAddr, sellerAddr, amount, reference)
}

func (a *streamLedgerAdapter) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ReleaseHold(ctx, agentAddr, amount, reference)
}

// TimelineItem represents an item in the unified timeline
type TimelineItem struct {
	Type      string      `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
}

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

// gatewayLedgerAdapter adapts ledger.Service to gateway.LedgerService
type gatewayLedgerAdapter struct {
	l ledger.Service
}

func (a *gatewayLedgerAdapter) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.Hold(ctx, agentAddr, amount, reference)
}

func (a *gatewayLedgerAdapter) SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	return a.l.SettleHold(ctx, buyerAddr, sellerAddr, amount, reference)
}

func (a *gatewayLedgerAdapter) SettleHoldWithFee(ctx context.Context, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference string) error {
	return a.l.SettleHoldWithFee(ctx, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference)
}

func (a *gatewayLedgerAdapter) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	return a.l.ReleaseHold(ctx, agentAddr, amount, reference)
}

// gatewayBillingAdapter adapts gateway.Store to tenant.BillingProvider
type gatewayBillingAdapter struct {
	store gateway.Store
}

func (a *gatewayBillingAdapter) GetBillingSummary(ctx context.Context, tenantID string) (*tenant.BillingSummary, error) {
	row, err := a.store.GetBillingSummary(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return &tenant.BillingSummary{
		TotalRequests:   row.TotalRequests,
		SettledRequests: row.SettledRequests,
		SettledVolume:   row.SettledVolume,
		FeesCollected:   row.FeesCollected,
	}, nil
}

// gatewayTenantSettingsAdapter adapts tenant.Store to gateway.TenantSettingsProvider
type gatewayTenantSettingsAdapter struct {
	store tenant.Store
}

func (a *gatewayTenantSettingsAdapter) GetTakeRateBPS(ctx context.Context, tenantID string) (int, error) {
	t, err := a.store.Get(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	return t.Settings.TakeRateBPS, nil
}

func (a *gatewayTenantSettingsAdapter) GetTenantStatus(ctx context.Context, tenantID string) (string, error) {
	t, err := a.store.Get(ctx, tenantID)
	if err != nil {
		return "", err
	}
	return string(t.Status), nil
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

// receiptIssuerAdapter adapts receipts.Service to the ReceiptIssuer interface
// used by gateway, streams, escrow, and session keys. A single adapter satisfies
// all four payment paths via Go structural typing.
type receiptIssuerAdapter struct {
	svc *receipts.Service
}

func (a *receiptIssuerAdapter) IssueReceipt(ctx context.Context, path, reference, from, to, amount, serviceID, status, metadata string) error {
	return a.svc.IssueReceipt(ctx, receipts.IssueRequest{
		Path:      receipts.PaymentPath(path),
		Reference: reference,
		From:      from,
		To:        to,
		Amount:    amount,
		ServiceID: serviceID,
		Status:    status,
		Metadata:  metadata,
	})
}
