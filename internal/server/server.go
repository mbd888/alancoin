// Package server sets up the HTTP server with all routes
package server

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq" // PostgreSQL driver
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/ethereum/go-ethereum/common"

	"github.com/mbd888/alancoin/internal/admin"
	"github.com/mbd888/alancoin/internal/arbitration"
	"github.com/mbd888/alancoin/internal/auth"
	"github.com/mbd888/alancoin/internal/billing"
	"github.com/mbd888/alancoin/internal/chargeback"
	"github.com/mbd888/alancoin/internal/circuitbreaker"
	"github.com/mbd888/alancoin/internal/config"
	"github.com/mbd888/alancoin/internal/contracts"
	"github.com/mbd888/alancoin/internal/dashboard"
	"github.com/mbd888/alancoin/internal/escrow"
	"github.com/mbd888/alancoin/internal/eventbus"
	"github.com/mbd888/alancoin/internal/flywheel"
	"github.com/mbd888/alancoin/internal/forensics"
	"github.com/mbd888/alancoin/internal/gateway"
	"github.com/mbd888/alancoin/internal/health"
	"github.com/mbd888/alancoin/internal/intelligence"
	"github.com/mbd888/alancoin/internal/kya"
	"github.com/mbd888/alancoin/internal/ledger"
	"github.com/mbd888/alancoin/internal/logging"
	"github.com/mbd888/alancoin/internal/metrics"
	"github.com/mbd888/alancoin/internal/offers"
	"github.com/mbd888/alancoin/internal/policy"
	"github.com/mbd888/alancoin/internal/ratelimit"
	"github.com/mbd888/alancoin/internal/realtime"
	"github.com/mbd888/alancoin/internal/receipts"
	"github.com/mbd888/alancoin/internal/reconciliation"
	"github.com/mbd888/alancoin/internal/registry"
	"github.com/mbd888/alancoin/internal/reputation"
	"github.com/mbd888/alancoin/internal/security"
	"github.com/mbd888/alancoin/internal/sessionkeys"
	"github.com/mbd888/alancoin/internal/streams"
	"github.com/mbd888/alancoin/internal/supervisor"
	"github.com/mbd888/alancoin/internal/tenant"
	"github.com/mbd888/alancoin/internal/tracerank"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"github.com/mbd888/alancoin/internal/validation"
	"github.com/mbd888/alancoin/internal/watcher"
	"github.com/mbd888/alancoin/internal/webhooks"
	"github.com/mbd888/alancoin/internal/workflows"
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
	coalitionService       *escrow.CoalitionService
	coalitionTimer         *escrow.CoalitionTimer
	contractService        *contracts.Service
	offerService           *offers.Service
	offerTimer             *offers.Timer
	workflowService        *workflows.Service
	streamService          *streams.Service
	streamTimer            *streams.Timer
	gatewayService         *gateway.Service
	gatewayTimer           *gateway.Timer
	receiptService         *receipts.Service
	reputationStore        reputation.SnapshotStore
	reputationWorker       *reputation.Worker
	reputationSigner       *reputation.Signer
	traceRankStore         tracerank.Store
	traceRankWorker        *tracerank.Worker
	flywheelEngine         *flywheel.Engine
	flywheelWorker         *flywheel.Worker
	flywheelStore          flywheel.SnapshotStore
	incentiveEngine        *flywheel.IncentiveEngine
	revenueAccumulator     *flywheel.RevenueAccumulator
	matviewRefresher       *registry.MatviewRefresher
	partitionMaint         *registry.PartitionMaintainer
	rateLimiter            *ratelimit.Limiter
	baselineTimer          *supervisor.BaselineTimer
	eventWriter            *supervisor.EventWriter
	tenantStore            tenant.Store
	billingProvider        billing.Provider
	billingMeter           *billing.Meter
	policyStore            policy.Store           // tenant-scoped spend policies
	gatewayStore           gateway.Store          // for billing aggregation
	webhookEmitter         *webhooks.Emitter      // tracked for graceful shutdown
	denialExporter         admin.DenialExporter   // denial log exporter for admin API
	reconcileRunner        *reconciliation.Runner // cross-subsystem reconciliation
	reconcileTimer         *reconciliation.Timer  // periodic reconciliation
	depositWatcher         *watcher.Watcher       // on-chain USDC deposit watcher (optional)
	kyaService             *kya.Service           // KYA identity certificates
	chargebackService      *chargeback.Service    // FinOps cost attribution
	arbitrationService     *arbitration.Service   // Dispute resolution
	forensicsService       *forensics.Service     // Spend anomaly detection
	intelligenceStore      intelligence.Store     // Unified agent intelligence profiles
	intelligenceWorker     *intelligence.Worker   // Periodic intelligence computation
	eventBus               *eventbus.MemoryBus    // Settlement event bus
	db                     *sql.DB                // nil if using in-memory
	router                 *gin.Engine
	httpSrv                *http.Server
	logger                 *slog.Logger
	cancelRunCtx           context.CancelFunc // cancels background goroutines started in Run
	tracerShutdown         func(context.Context) error

	// Health state
	ready          atomic.Bool
	healthy        atomic.Bool
	isDraining     atomic.Bool
	inFlight       atomic.Int64
	healthRegistry *health.Registry
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
		s.denialExporter = &adminDenialExportAdapter{store: baselineStore}
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
		coalitionStore := escrow.NewCoalitionPostgresStore(db)
		s.coalitionService = escrow.NewCoalitionService(coalitionStore, &escrowLedgerAdapter{s.ledgerService}).
			WithLogger(s.logger)
		s.coalitionTimer = escrow.NewCoalitionTimer(s.coalitionService, coalitionStore, s.logger)
		s.contractService = contracts.NewService(contracts.NewMemoryStore()).WithLogger(s.logger)
		offerStore := offers.NewMemoryStore()
		s.offerService = offers.NewService(offerStore, &escrowLedgerAdapter{s.ledgerService}).
			WithLogger(s.logger)
		s.offerTimer = offers.NewTimer(s.offerService, s.logger)
		s.workflowService = workflows.NewService(workflows.NewMemoryStore(), &escrowLedgerAdapter{s.ledgerService}).
			WithLogger(s.logger)
		s.logger.Info("escrow enabled (postgres)")

		// KYA identity certificates
		kyaAgentProvider := &kyaRegistryAdapter{s.registry}
		kyaRepProvider := &kyaReputationAdapter{} // wired to real reputation in setupRoutes
		s.kyaService = kya.NewService(kya.NewPostgresStore(db), kyaAgentProvider, kyaRepProvider,
			[]byte(s.cfg.ReceiptHMACSecret), s.logger)
		s.logger.Info("kya enabled (postgres)")

		// FinOps chargeback engine (postgres)
		s.chargebackService = chargeback.NewService(chargeback.NewPostgresStore(db), s.logger)
		s.logger.Info("chargeback enabled (postgres)")

		// Dispute arbitration (postgres)
		s.arbitrationService = arbitration.NewService(arbitration.NewPostgresStore(db),
			&arbitrationEscrowAdapter{s.escrowService}, nil, s.logger)
		s.logger.Info("arbitration enabled (postgres)")

		// Spend forensics (postgres)
		s.forensicsService = forensics.NewService(forensics.NewPostgresStore(db), forensics.DefaultConfig(), s.logger)
		s.logger.Info("forensics enabled (postgres)")

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
		gwResolver := gateway.NewResolver(&gatewayRegistryAdapter{store: s.registry, traceRankStore: s.traceRankStore})
		gwForwarder := gateway.NewForwarder(0)
		if cfg.AllowLocalEndpoints {
			gwForwarder = gwForwarder.WithAllowLocalEndpoints()
		}
		gwLedger := &gatewayLedgerAdapter{s.ledgerService}
		s.gatewayStore = gwStore
		gwCB := circuitbreaker.New(s.cfg.CircuitBreakerThreshold, s.cfg.CircuitBreakerDuration)
		s.gatewayService = gateway.NewService(gwStore, gwResolver, gwForwarder, gwLedger, s.logger).
			WithCircuitBreaker(gwCB)
		s.gatewayTimer = gateway.NewTimer(s.gatewayService, gwStore, s.logger)
		s.logger.Info("gateway enabled (postgres)")

		// Release any ledger holds orphaned by a previous crash.
		gateway.ReconcileOrphanedHolds(ctx, db, gwLedger, s.logger)

		// Wire webhook emitter into all payment subsystems.
		s.webhookEmitter = webhooks.NewEmitter(s.webhooks, s.logger)
		s.gatewayService.WithWebhookEmitter(s.webhookEmitter)
		s.escrowService.WithWebhookEmitter(s.webhookEmitter)
		s.streamService.WithWebhookEmitter(s.webhookEmitter)
		if s.coalitionService != nil {
			s.coalitionService.WithWebhookEmitter(s.webhookEmitter)
			if s.realtimeHub != nil {
				s.coalitionService.WithRealtimeBroadcaster(&coalitionRealtimeAdapter{s.realtimeHub})
			}
			if s.contractService != nil {
				s.coalitionService.WithContractChecker(&coalitionContractAdapter{s.contractService})
			}
		}

		s.gatewayService.WithRecorder(&gatewayRecorderAdapter{s.registry})
		if s.coalitionService != nil {
			s.coalitionService.WithRecorder(&gatewayRecorderAdapter{s.registry})
		}
		if s.offerService != nil {
			s.offerService.WithRecorder(&gatewayRecorderAdapter{s.registry})
			s.offerService.WithRevenueAccumulator(s.revenueAccumulator)
		}
		s.gatewayService.WithPlatformAddress(cfg.PlatformAddress)

		// Wire receipt issuer into all payment paths
		if s.receiptService != nil {
			rcptAdapter := &receiptIssuerAdapter{s.receiptService}
			s.gatewayService.WithReceiptIssuer(rcptAdapter)
			s.streamService.WithReceiptIssuer(rcptAdapter)
			s.escrowService.WithReceiptIssuer(rcptAdapter)
			if s.coalitionService != nil {
				s.coalitionService.WithReceiptIssuer(rcptAdapter)
			}
		}

		// Wire forensics into gateway for automatic anomaly detection.
		if s.forensicsService != nil {
			s.gatewayService.WithForensics(&forensicsGatewayAdapter{s.forensicsService})
		}

		// Wire intelligence into gateway for credit-gated escrow + dynamic fees.
		if s.intelligenceStore != nil {
			s.gatewayService.WithIntelligence(intelligence.NewCreditGate(s.intelligenceStore))
		}

		// Wire chargeback into gateway for automatic cost attribution + pre-flight budget check.
		if s.chargebackService != nil {
			s.gatewayService.WithChargeback(&chargebackGatewayAdapter{s.chargebackService})
			s.gatewayService.WithBudgetPreFlight(&budgetPreFlightAdapter{s.chargebackService})
		}

		// Event bus: replaces fire-and-forget goroutines with durable, batched processing.
		s.eventBus = eventbus.NewMemoryBus(s.cfg.EventBusBufferSize, s.logger)
		if db != nil {
			s.eventBus.WithWAL(eventbus.NewWALStore(db, s.logger))
			s.logger.Info("event bus WAL enabled (postgres)")
		}
		s.eventBus.Subscribe(eventbus.TopicSettlement, "forensics", 50, time.Second,
			s.makeForensicsConsumer())
		s.eventBus.Subscribe(eventbus.TopicSettlement, "chargeback", 50, time.Second,
			s.makeChargebackConsumer())
		s.eventBus.Subscribe(eventbus.TopicSettlement, "webhooks", 20, 500*time.Millisecond,
			s.makeWebhookConsumer())
		s.gatewayService.WithEventBus(&eventBusGatewayAdapter{s.eventBus})
		s.logger.Info("event bus enabled (in-memory, 10K buffer, 3 consumers)")

		// Wire revenue accumulator into all payment paths (GAP-1 fix).
		// Records seller revenue from gateway, escrow, streams for flywheel
		// velocity metrics and future staking/distribution.
		s.revenueAccumulator = flywheel.NewRevenueAccumulator(s.logger)
		s.gatewayService.WithRevenueAccumulator(s.revenueAccumulator)
		s.escrowService.WithRevenueAccumulator(s.revenueAccumulator)
		s.streamService.WithRevenueAccumulator(s.revenueAccumulator)
		if s.coalitionService != nil {
			s.coalitionService.WithRevenueAccumulator(s.revenueAccumulator)
		}
		s.logger.Info("revenue accumulator wired into all payment paths")

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

		// TraceRank graph-based reputation (PostgreSQL)
		s.traceRankStore = tracerank.NewPostgresStore(db)
		s.logger.Info("tracerank enabled (postgres)")

		// Billing provider (Stripe if configured, noop otherwise)
		s.billingProvider = initBillingProvider(cfg, s.logger)
		s.billingMeter = billing.NewMeter(s.billingProvider, s.logger)
		s.gatewayService.WithUsageMeter(s.billingMeter)
		s.gatewayTimer.WithMeter(s.billingMeter)
		s.logger.Info("billing enabled", "provider", billingProviderName(cfg))

		// Flywheel incentives (fee discounts + discovery boosts by reputation tier)
		s.incentiveEngine = flywheel.NewIncentiveEngine()
		s.gatewayService.WithIncentives(s.incentiveEngine)
		gwResolver.WithDiscoveryBooster(s.incentiveEngine)
		s.flywheelStore = flywheel.NewPostgresStore(db)
		s.logger.Info("flywheel enabled (postgres)")

		// Intelligence engine (unified agent intelligence profiles)
		s.intelligenceStore = intelligence.NewPostgresStore(db)
		gwResolver.WithIntelligenceRanker(intelligence.NewCreditGate(s.intelligenceStore))
		s.logger.Info("intelligence enabled (postgres)")

		// Cross-subsystem reconciliation (PostgreSQL only)
		s.reconcileRunner = reconciliation.NewRunner(s.logger).
			WithLedger(&reconcileLedgerAdapter{eventStore: eventStore, ledgerStore: ledgerStore}).
			WithEscrow(&reconcileEscrowAdapter{store: escrowStore}).
			WithStream(&reconcileStreamAdapter{store: streamStore}).
			WithHold(&reconcileHoldAdapter{db: db})
		s.reconcileTimer = reconciliation.NewTimer(s.reconcileRunner, s.logger)
		s.logger.Info("reconciliation enabled (postgres)")

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
		coalitionStore := escrow.NewCoalitionMemoryStore()
		s.coalitionService = escrow.NewCoalitionService(coalitionStore, &escrowLedgerAdapter{s.ledgerService}).
			WithLogger(s.logger)
		s.coalitionTimer = escrow.NewCoalitionTimer(s.coalitionService, coalitionStore, s.logger)
		s.contractService = contracts.NewService(contracts.NewMemoryStore()).WithLogger(s.logger)
		offerStore := offers.NewMemoryStore()
		s.offerService = offers.NewService(offerStore, &escrowLedgerAdapter{s.ledgerService}).
			WithLogger(s.logger)
		s.offerTimer = offers.NewTimer(s.offerService, s.logger)
		s.workflowService = workflows.NewService(workflows.NewMemoryStore(), &escrowLedgerAdapter{s.ledgerService}).
			WithLogger(s.logger)
		s.logger.Info("escrow enabled (in-memory)")

		// KYA identity certificates (in-memory)
		kyaAgentProvider := &kyaRegistryAdapter{s.registry}
		kyaRepProvider := &kyaReputationAdapter{} // wired to real reputation in setupRoutes
		s.kyaService = kya.NewService(kya.NewMemoryStore(), kyaAgentProvider, kyaRepProvider,
			[]byte(s.cfg.ReceiptHMACSecret), s.logger)
		s.logger.Info("kya enabled (in-memory)")

		// FinOps chargeback engine (in-memory)
		s.chargebackService = chargeback.NewService(chargeback.NewMemoryStore(), s.logger)
		s.logger.Info("chargeback enabled (in-memory)")

		// Dispute arbitration (in-memory)
		s.arbitrationService = arbitration.NewService(arbitration.NewMemoryStore(),
			&arbitrationEscrowAdapter{s.escrowService}, nil, s.logger)
		s.logger.Info("arbitration enabled (in-memory)")

		// Spend forensics (in-memory)
		s.forensicsService = forensics.NewService(forensics.NewMemoryStore(), forensics.DefaultConfig(), s.logger)
		s.logger.Info("forensics enabled (in-memory)")

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
		gwResolver2 := gateway.NewResolver(&gatewayRegistryAdapter{store: s.registry, traceRankStore: s.traceRankStore})
		gwForwarder2 := gateway.NewForwarder(0)
		if cfg.AllowLocalEndpoints {
			gwForwarder2 = gwForwarder2.WithAllowLocalEndpoints()
		}
		s.gatewayStore = gwStore2
		gwCB2 := circuitbreaker.New(s.cfg.CircuitBreakerThreshold, s.cfg.CircuitBreakerDuration)
		s.gatewayService = gateway.NewService(gwStore2, gwResolver2, gwForwarder2, &gatewayLedgerAdapter{s.ledgerService}, s.logger).
			WithCircuitBreaker(gwCB2)
		s.gatewayTimer = gateway.NewTimer(s.gatewayService, gwStore2, s.logger)
		s.logger.Info("gateway enabled (in-memory)")

		s.gatewayService.WithRecorder(&gatewayRecorderAdapter{s.registry})
		if s.coalitionService != nil {
			s.coalitionService.WithRecorder(&gatewayRecorderAdapter{s.registry})
		}
		if s.offerService != nil {
			s.offerService.WithRecorder(&gatewayRecorderAdapter{s.registry})
			s.offerService.WithRevenueAccumulator(s.revenueAccumulator)
		}
		s.gatewayService.WithPlatformAddress(cfg.PlatformAddress)

		// Wire webhook emitter into all payment subsystems (in-memory mode).
		s.webhookEmitter = webhooks.NewEmitter(s.webhooks, s.logger)
		s.gatewayService.WithWebhookEmitter(s.webhookEmitter)
		s.escrowService.WithWebhookEmitter(s.webhookEmitter)
		s.streamService.WithWebhookEmitter(s.webhookEmitter)
		if s.coalitionService != nil {
			s.coalitionService.WithWebhookEmitter(s.webhookEmitter)
			if s.realtimeHub != nil {
				s.coalitionService.WithRealtimeBroadcaster(&coalitionRealtimeAdapter{s.realtimeHub})
			}
			if s.contractService != nil {
				s.coalitionService.WithContractChecker(&coalitionContractAdapter{s.contractService})
			}
		}

		// Wire receipt issuer into all payment paths
		if s.receiptService != nil {
			rcptAdapter := &receiptIssuerAdapter{s.receiptService}
			s.gatewayService.WithReceiptIssuer(rcptAdapter)
			s.streamService.WithReceiptIssuer(rcptAdapter)
			s.escrowService.WithReceiptIssuer(rcptAdapter)
			if s.coalitionService != nil {
				s.coalitionService.WithReceiptIssuer(rcptAdapter)
			}
		}

		// Wire forensics into gateway (in-memory mode).
		if s.forensicsService != nil {
			s.gatewayService.WithForensics(&forensicsGatewayAdapter{s.forensicsService})
		}

		// Wire intelligence into gateway (in-memory mode).
		if s.intelligenceStore != nil {
			s.gatewayService.WithIntelligence(intelligence.NewCreditGate(s.intelligenceStore))
		}

		// Wire chargeback into gateway (in-memory mode).
		if s.chargebackService != nil {
			s.gatewayService.WithChargeback(&chargebackGatewayAdapter{s.chargebackService})
			s.gatewayService.WithBudgetPreFlight(&budgetPreFlightAdapter{s.chargebackService})
		}

		// Event bus (in-memory mode).
		s.eventBus = eventbus.NewMemoryBus(s.cfg.EventBusBufferSize, s.logger)
		s.eventBus.Subscribe(eventbus.TopicSettlement, "forensics", 50, time.Second,
			s.makeForensicsConsumer())
		s.eventBus.Subscribe(eventbus.TopicSettlement, "chargeback", 50, time.Second,
			s.makeChargebackConsumer())
		s.eventBus.Subscribe(eventbus.TopicSettlement, "webhooks", 20, 500*time.Millisecond,
			s.makeWebhookConsumer())
		s.gatewayService.WithEventBus(&eventBusGatewayAdapter{s.eventBus})
		s.logger.Info("event bus enabled (in-memory, 3 consumers)")

		// Wire revenue accumulator into all payment paths (in-memory mode).
		s.revenueAccumulator = flywheel.NewRevenueAccumulator(s.logger)
		s.gatewayService.WithRevenueAccumulator(s.revenueAccumulator)
		s.escrowService.WithRevenueAccumulator(s.revenueAccumulator)
		s.streamService.WithRevenueAccumulator(s.revenueAccumulator)
		if s.coalitionService != nil {
			s.coalitionService.WithRevenueAccumulator(s.revenueAccumulator)
		}
		s.logger.Info("revenue accumulator wired into all payment paths")

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

		// TraceRank graph-based reputation (in-memory)
		s.traceRankStore = tracerank.NewMemoryStore()
		s.logger.Info("tracerank enabled (in-memory)")

		// Billing provider (Stripe if configured, noop otherwise)
		s.billingProvider = initBillingProvider(cfg, s.logger)
		s.billingMeter = billing.NewMeter(s.billingProvider, s.logger)
		s.gatewayService.WithUsageMeter(s.billingMeter)
		s.gatewayTimer.WithMeter(s.billingMeter)
		s.logger.Info("billing enabled", "provider", billingProviderName(cfg))

		// Flywheel incentives (fee discounts + discovery boosts by reputation tier)
		s.incentiveEngine = flywheel.NewIncentiveEngine()
		s.gatewayService.WithIncentives(s.incentiveEngine)
		gwResolver2.WithDiscoveryBooster(s.incentiveEngine)
		s.flywheelStore = flywheel.NewMemoryStore()
		s.logger.Info("flywheel enabled (in-memory)")

		// Intelligence engine (in-memory)
		s.intelligenceStore = intelligence.NewMemoryStore()
		gwResolver2.WithIntelligenceRanker(intelligence.NewCreditGate(s.intelligenceStore))
		s.logger.Info("intelligence enabled (in-memory)")

	}

	// Register subsystem health checkers.
	s.healthRegistry = health.NewRegistry()
	if s.db != nil {
		db := s.db
		s.healthRegistry.Register("database", func(ctx context.Context) health.Status {
			if err := db.PingContext(ctx); err != nil {
				return health.Status{Name: "database", Healthy: false, Detail: err.Error()}
			}
			return health.Status{Name: "database", Healthy: true}
		})
	}
	if s.gatewayService != nil && s.gatewayService.CircuitBreaker() != nil {
		cb := s.gatewayService.CircuitBreaker()
		s.healthRegistry.Register("circuit_breaker", func(_ context.Context) health.Status {
			total, open, halfOpen := cb.Snapshot()
			detail := fmt.Sprintf("tracked=%d open=%d half_open=%d", total, open, halfOpen)
			// Degrade when more than half of tracked endpoints are open.
			healthy := total == 0 || open <= total/2
			return health.Status{Name: "circuit_breaker", Healthy: healthy, Detail: detail}
		})
	}

	if s.reconcileRunner != nil {
		runner := s.reconcileRunner
		s.healthRegistry.Register("reconciliation", func(_ context.Context) health.Status {
			report := runner.LastReport()
			if report == nil {
				return health.Status{Name: "reconciliation", Healthy: true, Detail: "no run yet"}
			}
			detail := fmt.Sprintf("mismatches=%d stuck_escrows=%d stale_streams=%d orphaned_holds=%d",
				report.LedgerMismatches, report.StuckEscrows, report.StaleStreams, report.OrphanedHolds)
			return health.Status{Name: "reconciliation", Healthy: report.Healthy, Detail: detail}
		})
	}

	// DB pool exhaustion check: degrade when >90% of connections are in use.
	if s.db != nil {
		db := s.db
		maxOpen := cfg.DBMaxOpenConns
		s.healthRegistry.Register("db_pool", func(_ context.Context) health.Status {
			stats := db.Stats()
			detail := fmt.Sprintf("in_use=%d idle=%d open=%d max=%d wait_count=%d",
				stats.InUse, stats.Idle, stats.OpenConnections, maxOpen, stats.WaitCount)
			healthy := maxOpen == 0 || float64(stats.InUse) < 0.9*float64(maxOpen)
			return health.Status{Name: "db_pool", Healthy: healthy, Detail: detail}
		})
	}

	// Intelligence health check
	if s.intelligenceStore != nil {
		s.healthRegistry.Register("intelligence", func(_ context.Context) health.Status {
			return health.Status{Name: "intelligence", Healthy: true, Detail: "ok"}
		})
	}

	// Event bus health check
	if s.eventBus != nil {
		s.healthRegistry.Register("event_bus", func(_ context.Context) health.Status {
			m := s.eventBus.Metrics()
			healthy := s.eventBus.IsHealthy()
			detail := fmt.Sprintf("pending=%d published=%d consumed=%d dropped=%d dlq=%d",
				m.Pending, m.Published, m.Consumed, m.Dropped, m.DeadLettered)
			return health.Status{Name: "event_bus", Healthy: healthy, Detail: detail}
		})
	}

	s.logger.Info("API authentication enabled")

	// Initialize deposit watcher (optional — only if explicitly enabled)
	if cfg.DepositWatcherEnabled {
		var checkpoint watcher.CheckpointStore
		if s.db != nil {
			checkpoint = watcher.NewPostgresCheckpoint(s.db, "deposit_watcher")
		} else {
			checkpoint = watcher.NewMemoryCheckpoint()
		}
		watcherCfg := watcher.Config{
			RPCURL:          cfg.RPCURL,
			USDCContract:    common.HexToAddress(cfg.USDCContract),
			PlatformAddress: common.HexToAddress(cfg.WalletAddress),
			StartBlock:      cfg.DepositWatcherStart,
		}
		s.depositWatcher = watcher.New(
			watcherCfg,
			&watcherCreditorAdapter{store: s.ledger.StoreRef()},
			&watcherAgentResolverAdapter{reg: s.registry},
			checkpoint,
			s.logger,
		)
		s.logger.Info("deposit watcher configured",
			"rpc_url", cfg.RPCURL,
			"usdc_contract", cfg.USDCContract)
	} else {
		s.logger.Warn("deposit watcher not enabled (set DEPOSIT_WATCHER_ENABLED=true to enable)")
	}

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
	// Drain middleware: once draining, reject new requests with 503.
	// Health and metrics endpoints are exempt so load balancers can observe the drain.
	s.router.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/health" || path == "/health/live" || path == "/health/ready" || path == "/metrics" {
			c.Next()
			return
		}
		if s.isDraining.Load() {
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error":   "draining",
				"message": "Server is shutting down. Retry with another instance.",
			})
			return
		}
		s.inFlight.Add(1)
		defer s.inFlight.Add(-1)
		c.Next()
	})

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

	// CORS — use configured origins in production, restrict in development.
	var corsOrigins []string
	if s.cfg.CORSAllowedOrigins != "" {
		corsOrigins = strings.Split(s.cfg.CORSAllowedOrigins, ",")
		for i := range corsOrigins {
			corsOrigins[i] = strings.TrimSpace(corsOrigins[i])
		}
	} else if s.cfg.IsProduction() {
		// Production with no explicit origins: deny all cross-origin requests.
		corsOrigins = nil
	} else {
		// Development: allow localhost origins only (not wildcard).
		corsOrigins = []string{
			"http://localhost:3000",
			"http://localhost:5173",
			"http://localhost:8080",
			"http://127.0.0.1:3000",
			"http://127.0.0.1:5173",
			"http://127.0.0.1:8080",
		}
	}
	s.router.Use(security.CORSMiddleware(corsOrigins))

	// Gzip compression (after CORS, before request size limit)
	s.router.Use(gzipMiddleware())

	// Request size limit (1MB)
	s.router.Use(validation.RequestSizeMiddleware(validation.MaxRequestSize))

	// Rate limiting
	s.rateLimiter = ratelimit.New(ratelimit.Config{
		RequestsPerMinute: s.cfg.RateLimitRPM,
		BurstSize:         s.cfg.RateLimitBurst,
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
	tracer := otel.Tracer("github.com/mbd888/alancoin/server")
	return func(c *gin.Context) {
		// Check for existing request ID (from load balancer, etc.)
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Start a server-level span so downstream spans inherit the trace.
		ctx := c.Request.Context()
		ctx, span := tracer.Start(ctx, c.Request.Method+" "+c.FullPath(),
			trace.WithAttributes(attribute.String("request_id", requestID)),
		)
		defer span.End()

		// Add request ID and trace context to logger.
		ctx = logging.WithRequestID(ctx, requestID)
		logger := s.logger
		if sc := span.SpanContext(); sc.HasTraceID() {
			logger = logger.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
		}
		ctx = logging.WithLogger(ctx, logger)
		c.Request = c.Request.WithContext(ctx)

		// Set response header
		c.Header("X-Request-ID", requestID)

		c.Next()

		// Record HTTP status on span
		status := c.Writer.Status()
		if status >= 400 {
			span.SetAttributes(attribute.Int("http.status_code", status))
		}
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

	// WebSocket for real-time streaming (requires API key auth)
	s.router.GET("/ws", func(c *gin.Context) {
		// Authenticate via query param or header before upgrading to WebSocket.
		// WebSocket clients cannot set custom headers after upgrade, so we also
		// accept the API key as a query parameter for the initial handshake.
		apiKey := c.GetHeader("Authorization")
		if apiKey == "" {
			apiKey = c.GetHeader("X-API-Key")
		}
		if apiKey == "" {
			apiKey = c.Query("token")
		}
		if apiKey == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "API key required. Pass 'Authorization' header or 'token' query parameter.",
			})
			return
		}
		if _, err := s.authMgr.ValidateKey(c.Request.Context(), apiKey); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "Invalid API key.",
			})
			return
		}
		s.realtimeHub.HandleWebSocket(c.Writer, c.Request)
	})

	// API info endpoints
	s.router.GET("/api", s.infoHandler)

	// V1 API group
	v1 := s.router.Group("/v1")
	// Validate :address URL params on all v1 routes (no-op when param absent)
	v1.Use(validation.AddressParamMiddleware())
	registryHandler := registry.NewHandler(s.registry)
	if s.cfg.AllowLocalEndpoints {
		registryHandler.SetAllowLocalEndpoints(true)
		s.logger.Info("SSRF checks disabled for service endpoint registration (ALLOW_LOCAL_ENDPOINTS=true)")
	}

	// Wire reputation into discovery so agents see trust scores when searching
	reputationProvider := reputation.NewRegistryProvider(s.registry)

	// Wire TraceRank into reputation for graph-based scoring
	if s.traceRankStore != nil {
		traceRankScoreProvider := tracerank.NewStoreScoreProvider(s.traceRankStore)
		reputationProvider.WithTraceRank(traceRankScoreProvider)
	}

	registryHandler.SetReputation(reputationProvider)

	// Wire reputation into supervisor so spending rules are tier-aware
	if sv, ok := s.ledgerService.(*supervisor.Supervisor); ok {
		sv.SetReputation(reputationProvider)
	}

	// Wire reputation impact tracking into escrow (dispute/confirm outcomes)
	s.escrowService.WithReputationImpactor(reputationProvider)
	if s.coalitionService != nil {
		s.coalitionService.WithReputationImpactor(reputationProvider)
	}

	// Wire KYA trust gate into escrow (verify seller trust before locking funds)
	if s.kyaService != nil {
		s.escrowService.WithTrustGate(&kyaTrustGateAdapter{s.kyaService})
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

	// REGISTRATION (public but returns API key)
	v1.POST("/agents", s.registerAgentWithAPIKey)

	// AUTH INFO (public)
	authHandler := auth.NewHandler(s.authMgr)
	v1.GET("/auth/info", authHandler.Info)

	// PROTECTED ROUTES (require API key)
	// These modify agent data and require ownership
	tenantRL := s.tenantRateLimitMiddleware()
	protected := v1.Group("")
	protected.Use(auth.Middleware(s.authMgr), tenantRL)
	{
		// Transaction recording requires hard auth to prevent reputation manipulation.
		// Soft middleware (Middleware) populates context; RequireAuth rejects unauthenticated callers.
		protected.POST("/transactions", auth.RequireAuth(s.authMgr), registryHandler.RecordTransaction)

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

	// Use demo mode (ledger-only accounting) unless SESSION_KEY_MODE=production
	if s.cfg.SessionKeyMode != "production" {
		sessionHandler = sessionHandler.WithDemoMode()
	}

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

	// Wire revenue accumulator for session key transactions
	if s.revenueAccumulator != nil {
		sessionHandler = sessionHandler.WithRevenueAccumulator(s.revenueAccumulator)
	}

	protectedSessions := v1.Group("")
	protectedSessions.Use(auth.Middleware(s.authMgr), tenantRL)
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
	protectedPolicies.Use(auth.Middleware(s.authMgr), tenantRL)
	protectedPolicies.Use(auth.RequireOwnership(s.authMgr, "address"))
	sessionHandler.RegisterPolicyRoutes(protectedPolicies)
	// Using a session key to transact doesn't require API key (the session key IS the auth).
	// Still apply rate limiting to prevent brute-force against the ECDSA signature.
	v1.POST("/agents/:address/sessions/:keyId/transact", s.rateLimiter.Middleware(), sessionHandler.Transact)

	// Delegation creation (A2A) — authenticated by session key ECDSA signature, no API key needed.
	// Rate limited to prevent brute-force against ECDSA signatures.
	v1.POST("/sessions/:keyId/delegate", s.rateLimiter.Middleware(), sessionHandler.CreateDelegation)

	// HMAC-chain delegation with proof — same auth as regular delegation (ECDSA signature)
	v1.POST("/session-keys/:id/delegate-with-proof", s.rateLimiter.Middleware(), sessionHandler.DelegateWithProof)

	// Proof verification — stateless except for root secret lookup. No API key needed
	// since the proof itself is the authentication mechanism.
	v1.POST("/session-keys/verify-proof", sessionHandler.VerifyDelegationProof)

	// Delegation read endpoints — require API key auth because they expose budget/spending data.
	// Moved from public v1 group to prevent unauthenticated enumeration of financial PII.
	protectedDelegation := v1.Group("")
	protectedDelegation.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
	protectedDelegation.GET("/sessions/:keyId/tree", sessionHandler.GetDelegationTree)
	protectedDelegation.GET("/sessions/:keyId/delegation-log", sessionHandler.GetDelegationLog)

	// =========================================================================
	// Gateway — transparent payment proxy (the primary path for AI agents)
	// One call: discover -> pay -> forward -> settle -> receipt -> reputation
	// =========================================================================
	if s.gatewayService != nil {
		gatewayHandler := gateway.NewHandler(s.gatewayService)

		// Protected routes - session CRUD requires API key auth
		protectedGateway := v1.Group("")
		protectedGateway.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		gatewayHandler.RegisterProtectedRoutes(protectedGateway)

		// Proxy route - requires both API key auth AND gateway token.
		// API key verifies caller identity, gateway token authorizes session access.
		protectedProxy := v1.Group("")
		protectedProxy.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
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
		adminTenants.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAdmin())
		tenantHandler.RegisterAdminRoutes(adminTenants)

		// Wire billing customer creator into tenant handler.
		if s.billingProvider != nil {
			tenantHandler.WithCustomerCreator(s.billingProvider)
		}

		// Protected routes: tenant CRUD, agent binding, key management (requires API key)
		protectedTenants := v1.Group("")
		protectedTenants.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
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

		// Billing subscription management routes (tenant-scoped, requires API key)
		if s.billingProvider != nil {
			billingHandler := billing.NewHandler(s.billingProvider, s.tenantStore)
			billingHandler.RegisterRoutes(protectedTenants)
		}

		// Stripe webhook handler (no auth — uses Stripe signature verification)
		if s.cfg.StripeWebhookSecret != "" {
			webhookHandler := billing.NewWebhookHandler(s.cfg.StripeWebhookSecret, s.tenantStore, s.logger)
			webhookHandler.RegisterRoute(v1)
		}
	}

	// Ledger routes (agent balances)
	if s.ledger != nil {
		ledgerHandler := ledger.NewHandler(s.ledger, s.logger)
		ledgerHandler.WithReputation(reputationProvider)

		// Ledger read routes — require ownership (financial PII).
		protectedLedgerRead := v1.Group("")
		protectedLedgerRead.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireOwnership(s.authMgr, "address"))
		protectedLedgerRead.GET("/agents/:address/balance", ledgerHandler.GetBalance)
		protectedLedgerRead.GET("/agents/:address/ledger", ledgerHandler.GetHistory)
		protectedLedgerRead.GET("/agents/:address/credit", ledgerHandler.GetCreditInfo)

		// Active credit listing is admin-only.
		adminCreditList := v1.Group("")
		adminCreditList.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAdmin())
		adminCreditList.GET("/credit/active", ledgerHandler.ListActiveCredit)

		protectedCredit := v1.Group("")
		protectedCredit.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireOwnership(s.authMgr, "address"))
		protectedCredit.POST("/agents/:address/credit/apply", ledgerHandler.ApplyForCredit)

		// Protected ledger routes
		protectedLedger := v1.Group("")
		protectedLedger.Use(auth.Middleware(s.authMgr), tenantRL)
		{
			protectedLedger.POST("/agents/:address/withdraw", auth.RequireOwnership(s.authMgr, "address"), ledgerHandler.RequestWithdrawal)
		}

		// Admin route for recording deposits (in production: webhook from blockchain indexer)
		// RequireAdmin checks X-Admin-Secret header (or allows any auth in demo mode).
		protectedLedger.POST("/admin/deposits", auth.RequireAdmin(), ledgerHandler.RecordDeposit)

		// Admin routes for reconciliation, audit, reversals, batch ops
		adminLedger := v1.Group("")
		adminLedger.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAdmin())
		ledgerHandler.RegisterAdminRoutes(adminLedger)

	}

	// Admin operations routes (resolve stuck financial states)
	{
		adminHandler := admin.NewHandler()
		if s.gatewayService != nil {
			adminHandler.WithGatewayService(&adminGatewayAdapter{svc: s.gatewayService})
		}
		if s.escrowService != nil {
			adminHandler.WithEscrowService(&adminEscrowAdapter{svc: s.escrowService})
		}
		if s.coalitionService != nil {
			adminHandler.WithCoalitionService(&adminCoalitionAdapter{svc: s.coalitionService})
		}
		if s.streamService != nil {
			adminHandler.WithStreamService(&adminStreamAdapter{svc: s.streamService})
		}
		if s.denialExporter != nil {
			adminHandler.WithDenialExporter(s.denialExporter)
		}
		if s.reconcileRunner != nil {
			adminHandler.WithReconciler(&adminReconcileAdapter{runner: s.reconcileRunner})
		}

		// State inspection providers
		if s.db != nil {
			adminHandler.WithStateProvider("db", &adminDBStateProvider{db: s.db})
		}
		if s.realtimeHub != nil {
			adminHandler.WithStateProvider("websocket", &adminWSStateProvider{hub: s.realtimeHub})
		}
		if s.reconcileRunner != nil {
			adminHandler.WithStateProvider("reconciliation", &adminReconcileStateProvider{runner: s.reconcileRunner})
		}

		adminOps := v1.Group("")
		adminOps.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAdmin())
		adminHandler.RegisterRoutes(adminOps)

		// Event bus observability (admin-only)
		if s.eventBus != nil {
			adminOps.GET("/admin/eventbus/stats", func(c *gin.Context) {
				c.JSON(200, gin.H{
					"metrics": s.eventBus.Metrics(),
					"healthy": s.eventBus.IsHealthy(),
				})
			})
			adminOps.GET("/admin/eventbus/dlq", func(c *gin.Context) {
				dlq := s.eventBus.DeadLetterQueue()
				c.JSON(200, gin.H{"events": dlq, "count": len(dlq)})
			})
			adminOps.POST("/admin/eventbus/dlq/replay", func(c *gin.Context) {
				replayed := s.eventBus.ReplayDeadLetters(c.Request.Context())
				c.JSON(200, gin.H{"replayed": replayed})
			})
		}
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

	// Create TraceRank worker for periodic graph recomputation
	if s.traceRankStore != nil {
		txSource := tracerank.NewRegistryTransactionSource(s.registry)
		agentInfoProvider := tracerank.NewRegistryAgentInfoProvider(s.registry)
		seedProvider := tracerank.NewTimeSeedProvider(agentInfoProvider)
		trEngine := tracerank.NewEngine(txSource, seedProvider, tracerank.DefaultConfig())

		trInterval := 5 * time.Minute
		if s.db == nil {
			trInterval = 30 * time.Second // Fast in demo mode
		}
		s.traceRankWorker = tracerank.NewWorker(trEngine, s.traceRankStore, trInterval, s.logger)
	}

	// Flywheel engine and periodic worker
	s.flywheelEngine = flywheel.NewEngine(s.registry)
	{
		fwInterval := 5 * time.Minute
		if s.db == nil {
			fwInterval = 30 * time.Second // Fast in demo mode
		}
		s.flywheelWorker = flywheel.NewWorker(s.flywheelEngine, fwInterval, s.logger).
			WithStore(s.flywheelStore).
			WithRevenueAccumulator(s.revenueAccumulator)
	}

	// Intelligence engine and periodic worker
	if s.intelligenceStore != nil {
		intelEngine := intelligence.NewEngine(
			&intelligenceTraceRankAdapter{s.traceRankStore},
			&intelligenceForensicsAdapter{s.forensicsService},
			&intelligenceReputationAdapter{reputationProvider},
			&intelligenceAgentSourceAdapter{s.registry},
			s.intelligenceStore,
			s.logger,
		)

		intelInterval := 5 * time.Minute
		if s.db == nil {
			intelInterval = 30 * time.Second
		}
		s.intelligenceWorker = intelligence.NewWorker(intelEngine, s.intelligenceStore, intelInterval, s.logger)

		// Wire tier transition notifications into webhooks
		if s.webhookEmitter != nil {
			s.intelligenceWorker.WithNotifier(s.webhookEmitter)
		}

		// Subscribe to settlement events for real-time profile updates
		if s.eventBus != nil {
			s.eventBus.Subscribe(eventbus.TopicSettlement, "intelligence", 50, time.Second,
				intelligence.MakeSettlementConsumer(intelEngine, s.intelligenceStore, s.logger))
		}
	}

	// Create matview refresher for service discovery (Postgres only)
	if s.db != nil {
		s.matviewRefresher = registry.NewMatviewRefresher(s.db, 30*time.Second, s.logger)
		s.partitionMaint = registry.NewPartitionMaintainer(s.db, 24*time.Hour, s.logger)
	}

	reputationHandler := reputation.NewHandlerFull(reputationProvider, s.reputationStore, s.reputationSigner)
	reputationHandler.RegisterRoutes(v1)

	// TraceRank routes (graph-based reputation scoring)
	if s.traceRankStore != nil {
		trHandler := tracerank.NewHandler(s.traceRankStore)
		trHandler.RegisterRoutes(v1)
	}

	// Intelligence routes (unified agent intelligence profiles)
	if s.intelligenceStore != nil {
		intelHandler := intelligence.NewHandler(s.intelligenceStore)
		intelHandler.RegisterRoutes(v1)
	}

	// Flywheel routes (network health and incentive observability)
	flywheelHandler := flywheel.NewHandler(s.flywheelEngine, s.incentiveEngine).
		WithStore(s.flywheelStore)
	flywheelHandler.RegisterRoutes(v1)

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
		protectedWebhooks.Use(auth.Middleware(s.authMgr), tenantRL)
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

		// Escrow read routes require authentication to protect financial PII.
		authedEscrow := v1.Group("")
		authedEscrow.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		escrowHandler.RegisterRoutes(authedEscrow)

		// Protected routes - only authenticated agents can create/confirm/dispute
		protectedEscrow := v1.Group("")
		protectedEscrow.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		escrowHandler.RegisterProtectedRoutes(protectedEscrow)
	}

	// MultiStep escrow routes (atomic N-step pipeline payments)
	if s.multiStepEscrowService != nil {
		msHandler := escrow.NewMultiStepHandler(s.multiStepEscrowService)

		// MultiStep escrow read routes require authentication.
		authedMS := v1.Group("")
		authedMS.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		msHandler.RegisterRoutes(authedMS)

		protectedMS := v1.Group("")
		protectedMS.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		msHandler.RegisterProtectedRoutes(protectedMS)
	}

	// Coalition escrow routes (outcome-triggered multi-agent settlement)
	if s.coalitionService != nil {
		coaHandler := escrow.NewCoalitionHandler(s.coalitionService)

		authedCoa := v1.Group("")
		authedCoa.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		coaHandler.RegisterRoutes(authedCoa)

		protectedCoa := v1.Group("")
		protectedCoa.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		coaHandler.RegisterProtectedRoutes(protectedCoa)
	}

	// Behavioral contract routes (SLA enforcement for coalition escrows)
	if s.contractService != nil {
		contractHandler := contracts.NewHandler(s.contractService)

		authedContracts := v1.Group("")
		authedContracts.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		contractHandler.RegisterRoutes(authedContracts)

		protectedContracts := v1.Group("")
		protectedContracts.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		contractHandler.RegisterProtectedRoutes(protectedContracts)
	}

	// Standing offers / marketplace routes
	if s.offerService != nil {
		offerHandler := offers.NewHandler(s.offerService)

		authedOffers := v1.Group("")
		authedOffers.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		offerHandler.RegisterRoutes(authedOffers)

		protectedOffers := v1.Group("")
		protectedOffers.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		offerHandler.RegisterProtectedRoutes(protectedOffers)
	}

	// Workflow budget management routes (enterprise cost attribution)
	if s.workflowService != nil {
		wfHandler := workflows.NewHandler(s.workflowService)

		authedWF := v1.Group("")
		authedWF.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		wfHandler.RegisterRoutes(authedWF)

		protectedWF := v1.Group("")
		protectedWF.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		wfHandler.RegisterProtectedRoutes(protectedWF)
	}

	// KYA identity certificate routes
	if s.kyaService != nil {
		kyaHandler := kya.NewHandler(s.kyaService)
		kyaHandler.RegisterRoutes(v1) // public reads

		protectedKYA := v1.Group("")
		protectedKYA.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		kyaHandler.RegisterProtectedRoutes(protectedKYA)
	}

	// FinOps chargeback routes (cost attribution + budget enforcement)
	if s.chargebackService != nil {
		cbHandler := chargeback.NewHandler(s.chargebackService)

		authedCB := v1.Group("")
		authedCB.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		cbHandler.RegisterRoutes(authedCB)

		protectedCB := v1.Group("")
		protectedCB.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		cbHandler.RegisterProtectedRoutes(protectedCB)
	}

	// Dispute arbitration routes
	if s.arbitrationService != nil {
		arbHandler := arbitration.NewHandler(s.arbitrationService)

		authedArb := v1.Group("")
		authedArb.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		arbHandler.RegisterRoutes(authedArb)

		protectedArb := v1.Group("")
		protectedArb.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		arbHandler.RegisterProtectedRoutes(protectedArb)
	}

	// Spend forensics routes (anomaly detection)
	if s.forensicsService != nil {
		forHandler := forensics.NewHandler(s.forensicsService)

		authedFor := v1.Group("")
		authedFor.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		forHandler.RegisterRoutes(authedFor)

		protectedFor := v1.Group("")
		protectedFor.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		forHandler.RegisterProtectedRoutes(protectedFor)
	}

	// Streaming micropayment routes (per-tick payments for continuous services)
	if s.streamService != nil {
		streamHandler := streams.NewHandler(s.streamService)
		if s.sessionMgr != nil {
			streamHandler = streamHandler.WithScopeChecker(s.sessionMgr)
		}

		// Stream read routes require authentication to protect financial PII.
		authedStreams := v1.Group("")
		authedStreams.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
		streamHandler.RegisterRoutes(authedStreams)

		// Protected routes - only authenticated agents can open/tick/close
		protectedStreams := v1.Group("")
		protectedStreams.Use(auth.Middleware(s.authMgr), tenantRL, auth.RequireAuth(s.authMgr))
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
	if s.isDraining.Load() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "draining"})
		return
	}
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
	checks["coalition_timer"] = timerStatus(s.coalitionTimer)
	checks["offers_timer"] = timerStatus(s.offerTimer)
	checks["stream_timer"] = timerStatus(s.streamTimer)
	checks["gateway_timer"] = timerStatus(s.gatewayTimer)
	checks["reconcile_timer"] = timerStatus(s.reconcileTimer)

	// Subsystem health checks.
	var subsystems []health.Status
	if s.healthRegistry != nil {
		subHealthy, statuses := s.healthRegistry.CheckAll(c.Request.Context())
		subsystems = statuses
		if !subHealthy {
			allOK = false
		}
	}

	status := "ready"
	httpStatus := http.StatusOK
	if !allOK {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}
	resp := gin.H{"status": status, "checks": checks}
	if len(subsystems) > 0 {
		resp["subsystems"] = subsystems
	}
	c.JSON(httpStatus, resp)
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

	// Start event bus (settlement event consumers)
	if s.eventBus != nil {
		go s.eventBus.Start(runCtx)
	}

	// Start materialized view refresher (5-minute interval)
	if s.db != nil {
		refresher := eventbus.NewMatviewRefresher(s.db, 5*time.Minute, s.logger)
		go refresher.Start(runCtx)
		s.logger.Info("materialized view refresher started (5m interval)")
	}

	// Start deposit watcher
	if s.depositWatcher != nil {
		go func() {
			if err := s.depositWatcher.Start(runCtx); err != nil && runCtx.Err() == nil {
				s.logger.Error("deposit watcher stopped with error", "error", err)
			}
		}()
	}

	// Start escrow auto-release timer
	if s.escrowTimer != nil {
		go s.escrowTimer.Start(runCtx)
	}

	// Start coalition escrow auto-settle timer
	if s.coalitionTimer != nil {
		go s.coalitionTimer.Start(runCtx)
	}

	// Start offers expiry timer
	if s.offerTimer != nil {
		go s.offerTimer.Start(runCtx)
	}

	// Start stream stale-close timer
	if s.streamTimer != nil {
		go s.streamTimer.Start(runCtx)
	}

	// Start gateway session expiry timer
	if s.gatewayTimer != nil {
		go s.gatewayTimer.Start(runCtx)
	}

	// Start reconciliation timer
	if s.reconcileTimer != nil {
		go s.reconcileTimer.Start(runCtx)
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

	// Start TraceRank computation worker
	if s.traceRankWorker != nil {
		go s.traceRankWorker.Start(runCtx)
	}

	// Start flywheel computation worker
	if s.flywheelWorker != nil {
		go s.flywheelWorker.Start(runCtx)
	}

	// Start intelligence computation worker
	if s.intelligenceWorker != nil {
		go s.intelligenceWorker.Start(runCtx)
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
		select {
		case <-time.After(100 * time.Millisecond):
			s.ready.Store(true)
			s.logger.Info("server ready")
		case <-runCtx.Done():
		}
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

// Shutdown gracefully stops the server using a 3-phase drain:
//  1. Set draining — new requests get 503, /health/ready reports "draining"
//  2. Wait for in-flight requests to complete (up to 15s)
//  3. Cancel background goroutines and shut down HTTP server
func (s *Server) Shutdown() error {
	// Phase 1: Signal drain — load balancers see /health/ready → 503 "draining",
	// new non-health requests get 503 + Retry-After.
	s.isDraining.Store(true)
	s.ready.Store(false)
	s.logger.Info("starting graceful shutdown (phase 1: draining)")

	// Phase 2: Wait for in-flight requests to finish.
	deadline := time.After(s.cfg.DrainDeadline)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
drainLoop:
	for {
		if s.inFlight.Load() == 0 {
			s.logger.Info("all in-flight requests drained")
			break
		}
		select {
		case <-deadline:
			s.logger.Warn("drain deadline exceeded, proceeding with shutdown",
				"in_flight", s.inFlight.Load())
			break drainLoop
		case <-ticker.C:
		}
	}

	// Phase 3: Cancel background goroutines and shut down HTTP server.
	s.logger.Info("starting graceful shutdown (phase 3: stopping subsystems)")
	if s.cancelRunCtx != nil {
		s.cancelRunCtx()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.httpSrv.Shutdown(ctx); err != nil {
		s.logger.Error("shutdown error", "error", err)
		return err
	}

	// Stop deposit watcher
	if s.depositWatcher != nil {
		s.depositWatcher.Stop()
		s.logger.Info("deposit watcher stopped")
	}

	// Stop escrow timer
	if s.escrowTimer != nil {
		s.escrowTimer.Stop()
		s.logger.Info("escrow timer stopped")
	}

	// Stop coalition timer
	if s.coalitionTimer != nil {
		s.coalitionTimer.Stop()
		s.logger.Info("coalition timer stopped")
	}

	// Stop offers timer
	if s.offerTimer != nil {
		s.offerTimer.Stop()
		s.logger.Info("offers timer stopped")
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

	// Stop reconciliation timer
	if s.reconcileTimer != nil {
		s.reconcileTimer.Stop()
		s.logger.Info("reconciliation timer stopped")
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

	// Stop TraceRank worker
	if s.traceRankWorker != nil {
		s.traceRankWorker.Stop()
		s.logger.Info("tracerank worker stopped")
	}

	// Stop intelligence worker
	if s.intelligenceWorker != nil {
		s.intelligenceWorker.Stop()
		s.logger.Info("intelligence worker stopped")
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

	// Drain in-flight webhook deliveries
	if s.webhookEmitter != nil {
		s.webhookEmitter.Shutdown(10 * time.Second)
		s.logger.Info("webhook emitter stopped")
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

// tenantRateLimitMiddleware returns a middleware that enforces per-tenant RPM
// limits. Must run AFTER auth.Middleware so the tenant context key is set.
func (s *Server) tenantRateLimitMiddleware() gin.HandlerFunc {
	if s.tenantStore == nil || s.rateLimiter == nil {
		return func(c *gin.Context) { c.Next() }
	}
	store := s.tenantStore
	return s.rateLimiter.TenantMiddleware(auth.ContextKeyTenantID, func(tenantID string) int {
		t, err := store.Get(context.Background(), tenantID)
		if err != nil {
			return 0
		}
		return t.Settings.RateLimitRPM
	})
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

func (a *gatewayTenantSettingsAdapter) GetStripeCustomerID(ctx context.Context, tenantID string) (string, error) {
	t, err := a.store.Get(ctx, tenantID)
	if err != nil {
		return "", err
	}
	return t.StripeCustomerID, nil
}

// gatewayRegistryAdapter adapts registry.Store to gateway.RegistryProvider
type gatewayRegistryAdapter struct {
	store          registry.Store
	traceRankStore tracerank.Store
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

	// Batch-load TraceRank scores for all candidates
	var trScores map[string]*tracerank.AgentScore
	if a.traceRankStore != nil && len(listings) > 0 {
		addrs := make([]string, 0, len(listings))
		for _, l := range listings {
			addrs = append(addrs, l.AgentAddress)
		}
		trScores, err = a.traceRankStore.GetScores(ctx, addrs)
		if err != nil {
			slog.Debug("tracerank scores unavailable for gateway enrichment", "error", err)
		}
	}

	var candidates []gateway.ServiceCandidate
	for _, l := range listings {
		endpoint := l.Endpoint
		c := gateway.ServiceCandidate{
			AgentAddress:    l.AgentAddress,
			AgentName:       l.AgentName,
			ServiceID:       l.ID,
			ServiceName:     l.Name,
			Price:           l.Price,
			Endpoint:        endpoint,
			ReputationScore: l.ReputationScore,
		}
		if trScores != nil {
			if s, ok := trScores[strings.ToLower(l.AgentAddress)]; ok && s != nil {
				c.TraceRankScore = s.GraphScore
			}
		}
		candidates = append(candidates, c)
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

// --- Admin adapters ---

// adminGatewayAdapter adapts gateway.Service to admin.GatewayService
type adminGatewayAdapter struct {
	svc *gateway.Service
}

func (a *adminGatewayAdapter) GetSession(ctx context.Context, id string) (admin.GatewaySession, error) {
	s, err := a.svc.GetSession(ctx, id)
	if err != nil {
		return admin.GatewaySession{}, err
	}
	return admin.GatewaySession{
		ID:        s.ID,
		AgentAddr: s.AgentAddr,
		MaxTotal:  s.MaxTotal,
		Status:    string(s.Status),
	}, nil
}

func (a *adminGatewayAdapter) CloseSession(ctx context.Context, sessionID, callerAddr string) error {
	_, err := a.svc.CloseSession(ctx, sessionID, callerAddr)
	return err
}

func (a *adminGatewayAdapter) ListStuckSessions(ctx context.Context, limit int) ([]admin.StuckSession, error) {
	sessions, err := a.svc.ListByStatus(ctx, gateway.StatusSettlementFailed, limit)
	if err != nil {
		return nil, err
	}
	result := make([]admin.StuckSession, len(sessions))
	for i, s := range sessions {
		result[i] = admin.StuckSession{
			ID:         s.ID,
			AgentAddr:  s.AgentAddr,
			TenantID:   s.TenantID,
			MaxTotal:   s.MaxTotal,
			TotalSpent: s.TotalSpent,
			Status:     string(s.Status),
			ExpiresAt:  s.ExpiresAt,
			UpdatedAt:  s.UpdatedAt,
		}
	}
	return result, nil
}

// adminEscrowAdapter adapts escrow.Service to admin.EscrowService
type adminEscrowAdapter struct {
	svc *escrow.Service
}

func (a *adminEscrowAdapter) ForceCloseExpired(ctx context.Context) (int, error) {
	return a.svc.ForceCloseExpired(ctx)
}

// coalitionContractAdapter adapts contracts.Service to escrow.ContractChecker
type coalitionContractAdapter struct {
	svc *contracts.Service
}

func (a *coalitionContractAdapter) GetContractByEscrow(ctx context.Context, escrowID string) (*escrow.BoundContract, error) {
	c, err := a.svc.GetByEscrow(ctx, escrowID)
	if err != nil {
		return nil, err
	}
	return &escrow.BoundContract{
		ID:             c.ID,
		Status:         string(c.Status),
		QualityPenalty: c.QualityPenalty,
		HardViolations: c.HardViolations,
	}, nil
}

func (a *coalitionContractAdapter) BindContract(ctx context.Context, contractID, escrowID string) error {
	_, err := a.svc.BindToEscrow(ctx, contractID, escrowID)
	return err
}

func (a *coalitionContractAdapter) MarkContractPassed(ctx context.Context, contractID string) error {
	_, err := a.svc.MarkPassed(ctx, contractID)
	return err
}

// coalitionRealtimeAdapter adapts realtime.Hub to escrow.RealtimeBroadcaster
type coalitionRealtimeAdapter struct {
	hub *realtime.Hub
}

func (a *coalitionRealtimeAdapter) BroadcastCoalitionEvent(eventType string, coalitionID, buyerAddr, status string) {
	a.hub.BroadcastCoalition(map[string]interface{}{
		"event":       eventType,
		"coalitionId": coalitionID,
		"buyerAddr":   buyerAddr,
		"status":      status,
	})
}

// adminCoalitionAdapter adapts escrow.CoalitionService to admin.CoalitionService
type adminCoalitionAdapter struct {
	svc *escrow.CoalitionService
}

func (a *adminCoalitionAdapter) ForceCloseExpired(ctx context.Context) (int, error) {
	return a.svc.ForceCloseExpired(ctx)
}

// adminStreamAdapter adapts streams.Service to admin.StreamService
type adminStreamAdapter struct {
	svc *streams.Service
}

func (a *adminStreamAdapter) ForceCloseStale(ctx context.Context) (int, error) {
	return a.svc.ForceCloseStale(ctx)
}

// adminDenialExportAdapter adapts supervisor.BaselineStore to admin.DenialExporter
type adminDenialExportAdapter struct {
	store supervisor.BaselineStore
}

func (a *adminDenialExportAdapter) ListDenials(ctx context.Context, since time.Time, limit int) ([]admin.DenialExportRecord, error) {
	denials, err := a.store.ListDenials(ctx, since, limit)
	if err != nil {
		return nil, err
	}
	result := make([]admin.DenialExportRecord, len(denials))
	for i, d := range denials {
		result[i] = admin.DenialExportRecord{
			ID:              d.ID,
			AgentAddr:       d.AgentAddr,
			RuleName:        d.RuleName,
			Reason:          d.Reason,
			Amount:          formatBigInt(d.Amount),
			OpType:          d.OpType,
			Tier:            d.Tier,
			Counterparty:    d.Counterparty,
			HourlyTotal:     formatBigInt(d.HourlyTotal),
			BaselineMean:    formatBigInt(d.BaselineMean),
			BaselineStddev:  formatBigInt(d.BaselineStddev),
			OverrideAllowed: d.OverrideAllowed,
			CreatedAt:       d.CreatedAt,
		}
	}
	return result, nil
}

func formatBigInt(b *big.Int) string {
	if b == nil {
		return "0.000000"
	}
	return usdc.Format(b)
}

// --- Reconciliation adapters ---

// reconcileLedgerAdapter adapts ledger event store to reconciliation.LedgerChecker
type reconcileLedgerAdapter struct {
	eventStore  ledger.EventStore
	ledgerStore ledger.Store
}

func (a *reconcileLedgerAdapter) CheckAll(ctx context.Context) (int, error) {
	results, err := ledger.ReconcileAll(ctx, a.eventStore, a.ledgerStore)
	if err != nil {
		return 0, err
	}
	mismatches := 0
	for _, r := range results {
		if !r.Match {
			mismatches++
		}
	}
	return mismatches, nil
}

// reconcileEscrowAdapter adapts escrow.Store to reconciliation.EscrowChecker
type reconcileEscrowAdapter struct {
	store escrow.Store
}

func (a *reconcileEscrowAdapter) CountExpired(ctx context.Context) (int, error) {
	expired, err := a.store.ListExpired(ctx, time.Now(), 1000)
	if err != nil {
		return 0, err
	}
	return len(expired), nil
}

// reconcileStreamAdapter adapts streams.Store to reconciliation.StreamChecker
type reconcileStreamAdapter struct {
	store streams.Store
}

func (a *reconcileStreamAdapter) CountStale(ctx context.Context) (int, error) {
	stale, err := a.store.ListStale(ctx, time.Now(), 1000)
	if err != nil {
		return 0, err
	}
	return len(stale), nil
}

// reconcileHoldAdapter checks for orphaned ledger holds via SQL.
type reconcileHoldAdapter struct {
	db *sql.DB
}

func (a *reconcileHoldAdapter) CountOrphaned(ctx context.Context) (int, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM ledger_entries le
		LEFT JOIN gateway_sessions gs ON gs.id = le.reference
		WHERE le.type = 'hold'
		  AND le.reference LIKE 'gw_%'
		  AND gs.id IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM ledger_entries le2
		      WHERE le2.agent_address = le.agent_address
		        AND le2.reference = le.reference
		        AND le2.type IN ('settle_hold_out', 'release_hold')
		  )
	`).Scan(&count)
	return count, err
}

// adminReconcileAdapter adapts reconciliation.Runner to admin.ReconciliationRunner
type adminReconcileAdapter struct {
	runner *reconciliation.Runner
}

func (a *adminReconcileAdapter) RunAll(ctx context.Context) (*admin.ReconciliationReport, error) {
	report, err := a.runner.RunAll(ctx)
	if err != nil {
		return nil, err
	}
	return &admin.ReconciliationReport{
		LedgerMismatches: report.LedgerMismatches,
		StuckEscrows:     report.StuckEscrows,
		StaleStreams:     report.StaleStreams,
		OrphanedHolds:    report.OrphanedHolds,
		Healthy:          report.Healthy,
		Duration:         report.Duration,
		Timestamp:        report.Timestamp,
	}, nil
}

// --- Admin state inspection providers ---

type adminDBStateProvider struct {
	db *sql.DB
}

func (p *adminDBStateProvider) AdminState(_ context.Context) map[string]interface{} {
	stats := p.db.Stats()
	return map[string]interface{}{
		"open_connections":    stats.OpenConnections,
		"in_use":              stats.InUse,
		"idle":                stats.Idle,
		"max_open":            stats.MaxOpenConnections,
		"wait_count":          stats.WaitCount,
		"wait_duration_ms":    stats.WaitDuration.Milliseconds(),
		"max_idle_closed":     stats.MaxIdleClosed,
		"max_lifetime_closed": stats.MaxLifetimeClosed,
	}
}

type adminWSStateProvider struct {
	hub *realtime.Hub
}

func (p *adminWSStateProvider) AdminState(_ context.Context) map[string]interface{} {
	return p.hub.Stats()
}

type adminReconcileStateProvider struct {
	runner *reconciliation.Runner
}

func (p *adminReconcileStateProvider) AdminState(_ context.Context) map[string]interface{} {
	report := p.runner.LastReport()
	if report == nil {
		return map[string]interface{}{"last_run": nil}
	}
	return map[string]interface{}{
		"healthy":           report.Healthy,
		"ledger_mismatches": report.LedgerMismatches,
		"stuck_escrows":     report.StuckEscrows,
		"stale_streams":     report.StaleStreams,
		"orphaned_holds":    report.OrphanedHolds,
		"last_run":          report.Timestamp,
		"duration_ms":       report.Duration.Milliseconds(),
	}
}

// initBillingProvider returns the Stripe provider if configured, otherwise noop.
func initBillingProvider(cfg *config.Config, logger *slog.Logger) billing.Provider {
	if cfg.StripeSecretKey == "" {
		return billing.NewNoopProvider(logger)
	}
	priceIDs := map[tenant.Plan]string{}
	if cfg.StripePriceStarterID != "" {
		priceIDs[tenant.PlanStarter] = cfg.StripePriceStarterID
	}
	if cfg.StripePriceGrowthID != "" {
		priceIDs[tenant.PlanGrowth] = cfg.StripePriceGrowthID
	}
	if cfg.StripePriceEnterpriseID != "" {
		priceIDs[tenant.PlanEnterprise] = cfg.StripePriceEnterpriseID
	}
	return billing.NewStripeProvider(cfg.StripeSecretKey, priceIDs)
}

func billingProviderName(cfg *config.Config) string {
	if cfg.StripeSecretKey != "" {
		return "stripe"
	}
	return "noop"
}

// watcherCreditorAdapter adapts ledger.Store to watcher.Creditor
type watcherCreditorAdapter struct {
	store ledger.Store
}

func (a *watcherCreditorAdapter) Credit(ctx context.Context, agentAddr, amount, txHash, description string) error {
	return a.store.Credit(ctx, agentAddr, amount, txHash, description)
}

func (a *watcherCreditorAdapter) HasDeposit(ctx context.Context, txHash string) (bool, error) {
	return a.store.HasDeposit(ctx, txHash)
}

// watcherAgentResolverAdapter adapts registry.Store to watcher.AgentResolver
type watcherAgentResolverAdapter struct {
	reg registry.Store
}

func (a *watcherAgentResolverAdapter) IsRegisteredAgent(ctx context.Context, addr string) (bool, error) {
	agent, err := a.reg.GetAgent(ctx, addr)
	if err != nil {
		return false, nil // treat errors as "not found"
	}
	return agent != nil, nil
}

// --- KYA adapters ---

type kyaRegistryAdapter struct {
	reg registry.Store
}

func (a *kyaRegistryAdapter) GetAgentName(ctx context.Context, addr string) (string, error) {
	agent, err := a.reg.GetAgent(ctx, addr)
	if err != nil {
		return "", err
	}
	return agent.Name, nil
}

func (a *kyaRegistryAdapter) GetAgentCreatedAt(ctx context.Context, addr string) (time.Time, error) {
	agent, err := a.reg.GetAgent(ctx, addr)
	if err != nil {
		return time.Time{}, err
	}
	return agent.CreatedAt, nil
}

type kyaReputationAdapter struct {
	rep *reputation.RegistryProvider
}

func (a *kyaReputationAdapter) GetScore(ctx context.Context, addr string) (float64, error) {
	if a.rep == nil {
		return 50.0, nil
	}
	score, _, err := a.rep.GetScore(ctx, addr)
	return score, err
}

func (a *kyaReputationAdapter) GetSuccessRate(ctx context.Context, addr string) (float64, error) {
	if a.rep == nil {
		return 0.95, nil
	}
	m, err := a.rep.GetAgentMetrics(ctx, addr)
	if err != nil {
		return 0, err
	}
	if m.TotalTransactions == 0 {
		return 0, nil
	}
	return float64(m.SuccessfulTxns) / float64(m.TotalTransactions), nil
}

func (a *kyaReputationAdapter) GetDisputeRate(_ context.Context, addr string) (float64, error) {
	if a.rep == nil {
		return 0.02, nil
	}
	return a.rep.DisputeRate(addr), nil
}

func (a *kyaReputationAdapter) GetTxCount(ctx context.Context, addr string) (int, error) {
	if a.rep == nil {
		return 50, nil
	}
	m, err := a.rep.GetAgentMetrics(ctx, addr)
	if err != nil {
		return 0, err
	}
	return m.TotalTransactions, nil
}

// --- Arbitration adapters ---

type arbitrationEscrowAdapter struct {
	svc *escrow.Service
}

func (a *arbitrationEscrowAdapter) RefundBuyer(ctx context.Context, escrowID string) error {
	_, err := a.svc.Dispute(ctx, escrowID, "", "arbitration: buyer wins")
	return err
}

func (a *arbitrationEscrowAdapter) ReleaseSeller(ctx context.Context, escrowID string) error {
	_, err := a.svc.Confirm(ctx, escrowID, "")
	return err
}

func (a *arbitrationEscrowAdapter) SplitFunds(ctx context.Context, escrowID string, buyerPct int) error {
	esc, err := a.svc.Get(ctx, escrowID)
	if err != nil {
		return fmt.Errorf("get escrow for split: %w", err)
	}
	totalBig, ok := usdc.Parse(esc.Amount)
	if !ok {
		return fmt.Errorf("invalid escrow amount: %s", esc.Amount)
	}
	sellerPct := 100 - buyerPct
	releaseAmt := new(big.Int).Mul(totalBig, big.NewInt(int64(sellerPct)))
	releaseAmt.Div(releaseAmt, big.NewInt(100))

	_, err = a.svc.ResolveArbitration(ctx, escrowID, esc.ArbitratorAddr, escrow.ResolveRequest{
		Resolution:    "partial",
		ReleaseAmount: usdc.Format(releaseAmt),
		Reason:        fmt.Sprintf("arbitration split: %d%% buyer / %d%% seller", buyerPct, sellerPct),
	})
	return err
}

// --- Forensics adapter ---

type forensicsGatewayAdapter struct {
	svc *forensics.Service
}

func (a *forensicsGatewayAdapter) IngestSpend(ctx context.Context, agentAddr, counterparty string, amountFloat float64, serviceType string) error {
	_, err := a.svc.Ingest(ctx, forensics.SpendEvent{
		AgentAddr:    agentAddr,
		Counterparty: counterparty,
		Amount:       amountFloat,
		ServiceType:  serviceType,
		Timestamp:    time.Now(),
	})
	return err
}

// --- Chargeback adapter ---

type chargebackGatewayAdapter struct {
	svc *chargeback.Service
}

func (a *chargebackGatewayAdapter) RecordGatewaySpend(ctx context.Context, tenantID, agentAddr, amount, serviceType, sessionID string) error {
	// Auto-attribute to the first active cost center for the tenant.
	centers, err := a.svc.ListCostCenters(ctx, tenantID)
	if err != nil || len(centers) == 0 {
		return nil // No cost centers configured — skip silently
	}
	for _, cc := range centers {
		if cc.Active {
			_, err := a.svc.RecordSpend(ctx, cc.ID, tenantID, agentAddr, amount, serviceType, chargeback.SpendOpts{
				SessionID:      sessionID,
				Description:    "auto: gateway proxy",
				IdempotencyKey: sessionID + ":" + amount, // Prevent double-count on event bus redelivery
			})
			return err
		}
	}
	return nil
}

// --- Budget pre-flight adapter (gateway → chargeback budget check) ---

type budgetPreFlightAdapter struct {
	svc *chargeback.Service
}

func (a *budgetPreFlightAdapter) CheckBudget(ctx context.Context, tenantID, _, _ string) error {
	hasRemaining, err := a.svc.HasBudgetRemaining(ctx, tenantID)
	if err != nil {
		return nil // Don't block on check failures
	}
	if !hasRemaining {
		return fmt.Errorf("all cost centers for tenant %s have exhausted their monthly budget", tenantID)
	}
	return nil
}

// --- KYA trust gate adapter (escrow → KYA verification) ---

type kyaTrustGateAdapter struct {
	kyaSvc *kya.Service
}

func (a *kyaTrustGateAdapter) CheckCounterpartyTrust(ctx context.Context, agentAddr string) error {
	if a.kyaSvc == nil {
		return nil // no KYA service → allow all
	}
	cert, err := a.kyaSvc.GetByAgent(ctx, agentAddr)
	if err != nil {
		// No certificate found — allow (KYA is optional, not mandatory)
		return nil
	}
	if !cert.IsValid() {
		return fmt.Errorf("agent %s has an expired or revoked KYA certificate", agentAddr)
	}
	if cert.Reputation.TrustTier == kya.TierD {
		return fmt.Errorf("agent %s has trust tier D (no history) — escrow requires higher trust", agentAddr)
	}
	return nil
}

// --- Event bus adapter ---

type eventBusGatewayAdapter struct {
	bus *eventbus.MemoryBus
}

func (a *eventBusGatewayAdapter) PublishSettlement(ctx context.Context, sessionID, tenantID, buyerAddr, sellerAddr, amount, fee, serviceType, serviceID, reference string, latencyMs int64) error {
	amountFloat, _ := strconv.ParseFloat(amount, 64)
	event, err := eventbus.NewEvent(eventbus.TopicSettlement, buyerAddr, eventbus.SettlementPayload{
		SessionID:   sessionID,
		TenantID:    tenantID,
		BuyerAddr:   buyerAddr,
		SellerAddr:  sellerAddr,
		Amount:      amount,
		ServiceType: serviceType,
		ServiceID:   serviceID,
		Fee:         fee,
		Reference:   reference,
		LatencyMs:   latencyMs,
		AmountFloat: amountFloat,
	})
	if err != nil {
		return err
	}
	return a.bus.Publish(ctx, event)
}

// makeForensicsConsumer creates an event bus consumer that feeds settlement events
// into the forensics anomaly detection engine in batches.
func (s *Server) makeForensicsConsumer() eventbus.Handler {
	return func(ctx context.Context, events []eventbus.Event) error {
		if s.forensicsService == nil {
			return nil
		}
		for _, e := range events {
			var p eventbus.SettlementPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				continue
			}
			_, _ = s.forensicsService.Ingest(ctx, forensics.SpendEvent{
				AgentAddr:    p.BuyerAddr,
				Counterparty: p.SellerAddr,
				Amount:       p.AmountFloat,
				ServiceType:  p.ServiceType,
				Timestamp:    e.Timestamp,
			})
		}
		return nil
	}
}

// makeChargebackConsumer creates an event bus consumer that auto-attributes
// settlement costs to tenant cost centers in batches.
func (s *Server) makeChargebackConsumer() eventbus.Handler {
	return func(ctx context.Context, events []eventbus.Event) error {
		if s.chargebackService == nil {
			return nil
		}
		for _, e := range events {
			var p eventbus.SettlementPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				continue
			}
			if p.TenantID == "" {
				continue
			}
			adapter := &chargebackGatewayAdapter{s.chargebackService}
			if err := adapter.RecordGatewaySpend(ctx, p.TenantID, p.BuyerAddr, p.Amount, p.ServiceType, p.SessionID); err != nil {
				return fmt.Errorf("chargeback record spend for tenant %s buyer %s: %w", p.TenantID, p.BuyerAddr, err)
			}
		}
		return nil
	}
}

// makeWebhookConsumer creates an event bus consumer that delivers settlement
// events to webhook subscribers, replacing fire-and-forget goroutines.
func (s *Server) makeWebhookConsumer() eventbus.Handler {
	return func(_ context.Context, events []eventbus.Event) error {
		if s.webhooks == nil {
			return nil
		}
		emitter := webhooks.NewEmitter(s.webhooks, s.logger)
		for _, e := range events {
			var p eventbus.SettlementPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				continue
			}
			emitter.EmitProxySuccess(p.BuyerAddr, p.SessionID, p.SellerAddr, p.Amount)
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// Intelligence engine adapters
// ---------------------------------------------------------------------------

// intelligenceTraceRankAdapter bridges tracerank.Store → intelligence.TraceRankProvider.
type intelligenceTraceRankAdapter struct {
	store tracerank.Store
}

func (a *intelligenceTraceRankAdapter) GetScore(ctx context.Context, address string) (graphScore float64, inDegree, outDegree int, inVolume, outVolume float64, err error) {
	if a.store == nil {
		return 0, 0, 0, 0, 0, nil
	}
	s, err := a.store.GetScore(ctx, address)
	if err != nil || s == nil {
		return 0, 0, 0, 0, 0, err
	}
	return s.GraphScore, s.InDegree, s.OutDegree, s.InVolume, s.OutVolume, nil
}

func (a *intelligenceTraceRankAdapter) GetAllScores(ctx context.Context) (map[string]intelligence.TraceRankData, error) {
	if a.store == nil {
		return map[string]intelligence.TraceRankData{}, nil
	}
	scores, err := a.store.GetTopScores(ctx, 10000)
	if err != nil {
		return nil, err
	}
	result := make(map[string]intelligence.TraceRankData, len(scores))
	for _, s := range scores {
		result[s.Address] = intelligence.TraceRankData{
			GraphScore: s.GraphScore,
			InDegree:   s.InDegree,
			OutDegree:  s.OutDegree,
			InVolume:   s.InVolume,
			OutVolume:  s.OutVolume,
		}
	}
	return result, nil
}

// intelligenceForensicsAdapter bridges forensics.Service → intelligence.ForensicsProvider.
type intelligenceForensicsAdapter struct {
	svc *forensics.Service
}

func (a *intelligenceForensicsAdapter) GetBaseline(ctx context.Context, agentAddr string) (txCount int, meanAmount, stdDevAmount float64, err error) {
	if a.svc == nil {
		return 0, 0, 0, nil
	}
	b, err := a.svc.GetBaseline(ctx, agentAddr)
	if err != nil || b == nil {
		return 0, 0, 0, err
	}
	return b.TxCount, b.MeanAmount, b.StdDevAmount, nil
}

func (a *intelligenceForensicsAdapter) CountAlerts30d(ctx context.Context, agentAddr string) (total, critical int, err error) {
	if a.svc == nil {
		return 0, 0, nil
	}
	alerts, err := a.svc.ListAlerts(ctx, agentAddr, 1000)
	if err != nil {
		return 0, 0, err
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	for _, alert := range alerts {
		if alert.DetectedAt.After(cutoff) {
			total++
			if alert.Severity == forensics.SeverityCritical {
				critical++
			}
		}
	}
	return total, critical, nil
}

// intelligenceReputationAdapter bridges reputation.RegistryProvider → intelligence.ReputationProvider.
type intelligenceReputationAdapter struct {
	provider *reputation.RegistryProvider
}

func (a *intelligenceReputationAdapter) GetMetrics(ctx context.Context, address string) (*intelligence.ReputationData, error) {
	m, err := a.provider.GetAgentMetrics(ctx, address)
	if err != nil || m == nil {
		return nil, err
	}
	// Compute score in-memory from metrics to avoid double-fetching.
	calc := reputation.NewCalculatorWithTraceRank()
	repScore := calc.Calculate(address, *m)
	return &intelligence.ReputationData{
		Score:                repScore.Score,
		TotalTransactions:    m.TotalTransactions,
		SuccessfulTxns:       m.SuccessfulTxns,
		FailedTxns:           m.FailedTxns,
		TotalVolumeUSD:       m.TotalVolumeUSD,
		UniqueCounterparties: m.UniqueCounterparties,
		DaysOnNetwork:        m.DaysOnNetwork,
	}, nil
}

func (a *intelligenceReputationAdapter) GetAllMetrics(ctx context.Context) (map[string]*intelligence.ReputationData, error) {
	metrics, err := a.provider.GetAllAgentMetrics(ctx)
	if err != nil {
		return nil, err
	}
	// Use a local calculator to compute scores from metrics in-memory,
	// avoiding N+1 calls to GetScore (which re-fetches metrics per agent).
	calc := reputation.NewCalculatorWithTraceRank()
	result := make(map[string]*intelligence.ReputationData, len(metrics))
	for addr, m := range metrics {
		repScore := calc.Calculate(addr, *m)
		result[addr] = &intelligence.ReputationData{
			Score:                repScore.Score,
			TotalTransactions:    m.TotalTransactions,
			SuccessfulTxns:       m.SuccessfulTxns,
			FailedTxns:           m.FailedTxns,
			TotalVolumeUSD:       m.TotalVolumeUSD,
			UniqueCounterparties: m.UniqueCounterparties,
			DaysOnNetwork:        m.DaysOnNetwork,
		}
	}
	return result, nil
}

// intelligenceAgentSourceAdapter bridges registry.Store → intelligence.AgentSource.
type intelligenceAgentSourceAdapter struct {
	store registry.Store
}

func (a *intelligenceAgentSourceAdapter) ListAllAddresses(ctx context.Context) ([]string, error) {
	agents, err := a.store.ListAgents(ctx, registry.AgentQuery{Limit: 10000})
	if err != nil {
		return nil, err
	}
	addrs := make([]string, len(agents))
	for i, agent := range agents {
		addrs[i] = agent.Address
	}
	return addrs, nil
}
