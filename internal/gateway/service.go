package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/circuitbreaker"
	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/retry"
	"github.com/mbd888/alancoin/internal/syncutil"
	"github.com/mbd888/alancoin/internal/usdc"
)

var validServiceType = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

// idempotencyEntry caches a proxy result for deduplication.
// The done channel is closed when processing completes, waking any waiters.
type idempotencyEntry struct {
	result    *ProxyResult
	err       error
	expiresAt time.Time
	done      chan struct{} // closed when result is ready
}

// idempotencyCache is a bounded TTL cache for proxy result deduplication.
// It supports in-flight dedup: concurrent requests with the same key wait
// for the first to complete rather than both processing.
// Max 10000 entries; expired entries are swept by the Timer.
type idempotencyCache struct {
	mu      sync.Mutex
	entries map[string]*idempotencyEntry
	ttl     time.Duration
	maxSize int
}

func newIdempotencyCache(ttl time.Duration) *idempotencyCache {
	return &idempotencyCache{
		entries: make(map[string]*idempotencyEntry),
		ttl:     ttl,
		maxSize: 10000,
	}
}

func (c *idempotencyCache) key(sessionID, idempotencyKey string) string {
	return sessionID + ":" + idempotencyKey
}

// getOrReserve checks for a cached result or reserves the key for processing.
//
// Returns:
//   - (result, err, true)  — cached result found, use it directly
//   - (nil, nil, false)    — key reserved, caller must call complete() or cancel()
//
// If another goroutine is currently processing the same key, this blocks
// until that goroutine calls complete() or cancel(), or ctx is cancelled.
func (c *idempotencyCache) getOrReserve(ctx context.Context, sessionID, idempotencyKey string) (*ProxyResult, error, bool) {
	k := c.key(sessionID, idempotencyKey)

	c.mu.Lock()
	entry, ok := c.entries[k]
	if ok && time.Now().After(entry.expiresAt) {
		delete(c.entries, k)
		ok = false
	}

	if ok {
		done := entry.done
		c.mu.Unlock()

		// Wait for the in-flight request to complete.
		select {
		case <-done:
			// Re-read under lock in case cancel() deleted the entry.
			c.mu.Lock()
			entry, ok = c.entries[k]
			c.mu.Unlock()
			if ok {
				return entry.result, entry.err, true
			}
			// Entry was cancelled — fall through to re-reserve below.
			return c.getOrReserve(ctx, sessionID, idempotencyKey)
		case <-ctx.Done():
			return nil, ctx.Err(), true
		}
	}

	// Reject if cache is at capacity (prevents unbounded growth).
	if len(c.entries) >= c.maxSize {
		c.mu.Unlock()
		// Process normally without idempotency — better than rejecting the request.
		return nil, nil, false
	}

	// Reserve the key for this goroutine.
	c.entries[k] = &idempotencyEntry{
		done:      make(chan struct{}),
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
	return nil, nil, false
}

// complete stores the result and wakes all waiters.
// Only call this for successful results that should be cached.
func (c *idempotencyCache) complete(sessionID, idempotencyKey string, result *ProxyResult) {
	k := c.key(sessionID, idempotencyKey)
	c.mu.Lock()
	entry, ok := c.entries[k]
	if !ok {
		c.mu.Unlock()
		return
	}
	entry.result = result
	entry.err = nil
	entry.expiresAt = time.Now().Add(c.ttl)
	c.mu.Unlock()
	close(entry.done)
}

// cancel removes the reservation and wakes waiters so they can retry.
// Call this when processing fails and the result should NOT be cached.
func (c *idempotencyCache) cancel(sessionID, idempotencyKey string) {
	k := c.key(sessionID, idempotencyKey)
	c.mu.Lock()
	entry, ok := c.entries[k]
	if ok {
		delete(c.entries, k)
	}
	c.mu.Unlock()
	if ok {
		close(entry.done)
	}
}

// sweep removes all expired entries. Called by the Timer goroutine.
func (c *idempotencyCache) sweep() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	removed := 0
	for k, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, k)
			removed++
		}
	}
	return removed
}

// size returns the current number of entries.
func (c *idempotencyCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Service implements gateway business logic.
type Service struct {
	store           Store
	resolver        *Resolver
	forwarder       *Forwarder
	ledger          LedgerService
	recorder        TransactionRecorder
	receiptIssuer   ReceiptIssuer
	policyEvaluator PolicyEvaluator
	tenantSettings  TenantSettingsProvider
	webhookEmitter  WebhookEmitter
	circuitBreaker  *circuitbreaker.Breaker
	platformAddr    string // ledger address collecting platform fees
	logger          *slog.Logger
	locks           syncutil.ShardedMutex
	idemCache       *idempotencyCache
	rateLimit       *rateLimiter

	// pendingSpend tracks in-flight budget reservations per session.
	// Key: sessionID, Value: *big.Int (sum of reserved amounts).
	// Accessed under per-session lock (s.locks) for correctness.
	pendingSpend sync.Map
}

// SweepIdempotencyCache removes expired entries. Called by the Timer.
func (s *Service) SweepIdempotencyCache() int {
	return s.idemCache.sweep()
}

// SweepRateLimiter removes stale rate limit entries. Called by the Timer.
func (s *Service) SweepRateLimiter() int {
	return s.rateLimit.sweep()
}

// NewService creates a new gateway service.
func NewService(store Store, resolver *Resolver, forwarder *Forwarder, ledger LedgerService, logger *slog.Logger) *Service {
	return &Service{
		store:     store,
		resolver:  resolver,
		forwarder: forwarder,
		ledger:    ledger,
		logger:    logger,
		idemCache: newIdempotencyCache(10 * time.Minute),
		rateLimit: newRateLimiter(),
	}
}

// WithRecorder adds a transaction recorder for reputation integration.
func (s *Service) WithRecorder(r TransactionRecorder) *Service {
	s.recorder = r
	return s
}

// WithReceiptIssuer adds a receipt issuer for cryptographic payment proofs.
func (s *Service) WithReceiptIssuer(r ReceiptIssuer) *Service {
	s.receiptIssuer = r
	return s
}

// WithPolicyEvaluator adds a policy evaluator for spending constraints.
func (s *Service) WithPolicyEvaluator(p PolicyEvaluator) *Service {
	s.policyEvaluator = p
	return s
}

// WithTenantSettings adds a tenant settings provider for fee computation.
func (s *Service) WithTenantSettings(ts TenantSettingsProvider) *Service {
	s.tenantSettings = ts
	return s
}

// WithPlatformAddress sets the ledger address that collects platform fees.
func (s *Service) WithPlatformAddress(addr string) *Service {
	s.platformAddr = strings.ToLower(addr)
	return s
}

// WithWebhookEmitter adds a webhook emitter for lifecycle event notifications.
func (s *Service) WithWebhookEmitter(e WebhookEmitter) *Service {
	s.webhookEmitter = e
	return s
}

// WithCircuitBreaker adds per-endpoint circuit breaking to the forwarder.
func (s *Service) WithCircuitBreaker(cb *circuitbreaker.Breaker) *Service {
	s.circuitBreaker = cb
	return s
}

// CircuitBreaker returns the circuit breaker (or nil). Used for health reporting.
func (s *Service) CircuitBreaker() *circuitbreaker.Breaker {
	return s.circuitBreaker
}

// getPendingSpend returns a copy of the current pending spend for a session.
// Must be called under the per-session lock.
func (s *Service) getPendingSpend(sessionID string) *big.Int {
	val, ok := s.pendingSpend.Load(sessionID)
	if !ok {
		return new(big.Int)
	}
	return new(big.Int).Set(val.(*big.Int))
}

// addPendingSpend reserves budget for an in-flight request.
// Must be called under the per-session lock.
func (s *Service) addPendingSpend(sessionID string, amount *big.Int) {
	current := s.getPendingSpend(sessionID)
	s.pendingSpend.Store(sessionID, new(big.Int).Add(current, amount))
}

// removePendingSpend releases a budget reservation.
// Must be called under the per-session lock.
func (s *Service) removePendingSpend(sessionID string, amount *big.Int) {
	current := s.getPendingSpend(sessionID)
	result := new(big.Int).Sub(current, amount)
	if result.Sign() <= 0 {
		s.pendingSpend.Delete(sessionID)
	} else {
		s.pendingSpend.Store(sessionID, result)
	}
}

// CreateSession creates a gateway session and holds the buyer's budget.
// tenantID is optional; pass "" for non-tenant sessions.
func (s *Service) CreateSession(ctx context.Context, agentAddr, tenantID string, req CreateSessionRequest) (*Session, error) {
	maxTotalBig, ok := usdc.Parse(req.MaxTotal)
	if !ok || maxTotalBig.Sign() <= 0 {
		return nil, fmt.Errorf("%w: maxTotal", ErrInvalidAmount)
	}

	maxPerBig, ok := usdc.Parse(req.MaxPerRequest)
	if !ok || maxPerBig.Sign() <= 0 {
		return nil, fmt.Errorf("%w: maxPerRequest", ErrInvalidAmount)
	}

	if len(req.AllowedTypes) > 100 {
		return nil, fmt.Errorf("%w: allowedTypes exceeds maximum of 100 entries", ErrInvalidAmount)
	}
	for _, t := range req.AllowedTypes {
		if !validServiceType.MatchString(t) {
			return nil, fmt.Errorf("%w: invalid service type %q (must match [a-zA-Z0-9_-]{1,100})", ErrInvalidAmount, t)
		}
	}

	// Check tenant status before proceeding.
	if tenantID != "" && s.tenantSettings != nil {
		if status, err := s.tenantSettings.GetTenantStatus(ctx, tenantID); err == nil {
			if status == "suspended" || status == "cancelled" {
				return nil, ErrTenantSuspended
			}
		}
	}

	strategy := req.Strategy
	if strategy == "" {
		strategy = "cheapest"
	}

	expiresIn := time.Duration(req.ExpiresInSec) * time.Second
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}
	if req.ExpiresInSec > 0 && req.ExpiresInSec < 60 {
		return nil, fmt.Errorf("%w: expiresInSecs must be at least 60", ErrInvalidAmount)
	}
	if req.ExpiresInSec > 86400 {
		return nil, fmt.Errorf("%w: expiresInSecs must be at most 86400 (24h)", ErrInvalidAmount)
	}

	warnAt := req.WarnAtPercent
	if warnAt < 0 || warnAt > 100 {
		warnAt = 0
	}

	rpmLimit := req.MaxRequestsPerMinute
	if rpmLimit <= 0 {
		rpmLimit = defaultMaxRequestsPerMinute
	}
	if rpmLimit > 1000 {
		rpmLimit = 1000
	}

	now := time.Now()
	session := &Session{
		ID:                   idgen.WithPrefix("gw_"),
		AgentAddr:            strings.ToLower(agentAddr),
		TenantID:             tenantID,
		MaxTotal:             req.MaxTotal,
		MaxPerRequest:        req.MaxPerRequest,
		TotalSpent:           "0.000000",
		RequestCount:         0,
		Strategy:             strategy,
		AllowedTypes:         req.AllowedTypes,
		WarnAtPercent:        warnAt,
		MaxRequestsPerMinute: rpmLimit,
		Status:               StatusActive,
		ExpiresAt:            now.Add(expiresIn),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	session.BuildAllowedTypesSet()

	// Policy check before holding funds (e.g. time_window enforcement).
	if s.policyEvaluator != nil {
		decision, err := s.policyEvaluator.EvaluateProxy(ctx, session, "")
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPolicyDenied, err)
		}
		if decision != nil && !decision.Allowed {
			return nil, fmt.Errorf("%w: %s", ErrPolicyDenied, decision.Reason)
		}
	}

	// Hold the full budget from the buyer
	if err := s.ledger.Hold(ctx, session.AgentAddr, session.MaxTotal, session.ID); err != nil {
		return nil, &MoneyError{
			Err:         fmt.Errorf("failed to hold gateway funds: %w", err),
			FundsStatus: "no_change",
			Recovery:    "No funds were moved. Check your available balance and try again.",
			Amount:      session.MaxTotal,
			Reference:   session.ID,
		}
	}

	s.rateLimit.setLimit(session.ID, rpmLimit)

	if err := s.store.CreateSession(ctx, session); err != nil {
		if relErr := s.ledger.ReleaseHold(ctx, session.AgentAddr, session.MaxTotal, session.ID); relErr != nil {
			s.logger.Error("ReleaseHold failed after store error: funds stuck in pending",
				"session", session.ID, "amount", session.MaxTotal, "error", relErr)
			return nil, &MoneyError{
				Err:         fmt.Errorf("failed to create gateway session: %w", err),
				FundsStatus: "held_pending",
				Recovery:    "Session creation failed and hold release also failed. Contact support with the reference to release your funds.",
				Amount:      session.MaxTotal,
				Reference:   session.ID,
			}
		}
		return nil, &MoneyError{
			Err:         fmt.Errorf("failed to create gateway session: %w", err),
			FundsStatus: "no_change",
			Recovery:    "Session creation failed but your funds were returned. Safe to retry.",
			Amount:      session.MaxTotal,
			Reference:   session.ID,
		}
	}

	gwSessionsCreated.Inc()
	gwActiveSessions.Inc()

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitSessionCreated(session.AgentAddr, session.ID, session.MaxTotal)
	}

	return session, nil
}

// Proxy handles a single proxy request within a session.
//
// Lock strategy (3-phase with budget reservation):
//
//	Phase 1: Lock → validate session, resolve candidates → unlock
//	Phase 2: For each candidate:
//	  2a: Lock → check budget (accounting for pendingSpend) → reserve → unlock
//	  2b: Forward HTTP request (unlocked, concurrent-safe)
//	  2c: Lock → settle payment + update session (or unreserve on failure) → unlock
//
// The pendingSpend reservation in 2a prevents concurrent requests from
// over-allocating budget. Each request's reservation is visible to others.
func (s *Service) Proxy(ctx context.Context, sessionID string, req ProxyRequest) (*ProxyResult, error) {
	if !validServiceType.MatchString(req.ServiceType) {
		return nil, fmt.Errorf("%w: invalid service type %q (must match [a-zA-Z0-9_-]{1,100})", ErrNoServiceAvailable, req.ServiceType)
	}

	proxyStart := time.Now()

	// Rate limit check — before any lock acquisition or DB access.
	if !s.rateLimit.allow(sessionID) {
		gwProxyRequests.WithLabelValues("rate_limited").Inc()
		return nil, ErrRateLimited
	}

	// Idempotency: if the client provides a key, check cache or reserve for processing.
	idemReserved := false
	if req.IdempotencyKey != "" {
		result, err, found := s.idemCache.getOrReserve(ctx, sessionID, req.IdempotencyKey)
		if found {
			return result, err
		}
		// We reserved the key — must call complete() or cancel() before returning.
		idemReserved = true
	}

	// Wrapper to handle idempotency cleanup on all exit paths.
	cancelIdem := func() {
		if idemReserved {
			s.idemCache.cancel(sessionID, req.IdempotencyKey)
		}
	}

	// --- Phase 1: Lock, validate session, resolve candidates ---
	unlock := s.locks.Lock(sessionID)

	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		unlock()
		cancelIdem()
		return nil, err
	}

	if session.Status != StatusActive {
		unlock()
		cancelIdem()
		return nil, ErrSessionClosed
	}
	if session.IsExpired() {
		unlock()
		cancelIdem()
		return nil, ErrSessionExpired
	}

	// Check tenant status (suspended tenants cannot proxy).
	if session.TenantID != "" && s.tenantSettings != nil {
		if status, err := s.tenantSettings.GetTenantStatus(ctx, session.TenantID); err == nil {
			if status == "suspended" || status == "cancelled" {
				gwProxyRequests.WithLabelValues("tenant_suspended").Inc()
				unlock()
				cancelIdem()
				return nil, ErrTenantSuspended
			}
		}
	}

	// Check allowed types (O(1) map lookup).
	if !session.IsTypeAllowed(req.ServiceType) {
		unlock()
		cancelIdem()
		return nil, fmt.Errorf("%w: service type %q not in allowed types", ErrNoServiceAvailable, req.ServiceType)
	}

	// Resolve candidates
	candidates, err := s.resolver.Resolve(ctx, req, session.Strategy, session.MaxPerRequest)
	if err != nil {
		s.logRequest(ctx, session.ID, session.TenantID, req.ServiceType, "", "0", "no_service", 0, err.Error())
		unlock()
		cancelIdem()
		return nil, err
	}

	// Snapshot immutable session properties.
	holdBig, _ := usdc.Parse(session.MaxTotal)
	maxPerBig, _ := usdc.Parse(session.MaxPerRequest)
	agentAddr := session.AgentAddr
	sessionIDCopy := session.ID
	tenantIDCopy := session.TenantID

	unlock() // Release lock — candidates resolved, ready to iterate

	// --- Phase 2: Try candidates ---
	var lastErr error
	retries := 0

	for _, candidate := range candidates {
		// Skip endpoints with tripped circuit breakers.
		if s.circuitBreaker != nil && !s.circuitBreaker.Allow(candidate.Endpoint) {
			continue
		}

		priceBig, ok := usdc.Parse(candidate.Price)
		if !ok || priceBig.Sign() <= 0 {
			continue
		}

		// Check per-request limit (static, doesn't change)
		if priceBig.Cmp(maxPerBig) > 0 {
			continue
		}

		// --- Phase 2a: Lock, reserve budget for this candidate ---
		unlock = s.locks.Lock(sessionID)

		session, err = s.store.GetSession(ctx, sessionIDCopy)
		if err != nil {
			unlock()
			cancelIdem()
			return nil, err
		}
		if session.Status != StatusActive {
			unlock()
			cancelIdem()
			return nil, ErrSessionClosed
		}

		// Policy check (under lock, before budget reservation).
		// Project pendingSpend into session so the evaluator sees in-flight
		// reservations from concurrent requests, preventing spend-limit bypass.
		var policyDecision *PolicyDecision
		if s.policyEvaluator != nil {
			projected := *session
			if pending := s.getPendingSpend(sessionIDCopy); pending.Sign() > 0 {
				spentBig, _ := usdc.Parse(projected.TotalSpent)
				projected.TotalSpent = usdc.Format(new(big.Int).Add(spentBig, pending))
			}
			policyDecision, err = s.policyEvaluator.EvaluateProxy(ctx, &projected, req.ServiceType)
			if err != nil {
				if policyDecision != nil {
					// Genuine policy denial — decision carries rule details.
					s.logger.Info("policy denied proxy request",
						"session", sessionID,
						"agent", session.AgentAddr,
						"policy", policyDecision.GetDeniedBy(),
						"rule", policyDecision.GetDeniedRule(),
						"reason", policyDecision.GetReason())
					s.logRequestWithPolicy(ctx, sessionIDCopy, tenantIDCopy, req.ServiceType, "", "0", "policy_denied", 0, err.Error(), policyDecision)
					gwProxyRequests.WithLabelValues("policy_denied").Inc()
					gwPolicyDenials.WithLabelValues(policyDecision.GetDeniedRule()).Inc()
				} else {
					// Store/evaluator failure — fail closed but label correctly.
					s.logger.Error("policy evaluation failed",
						"session", sessionID, "error", err)
					s.logRequest(ctx, sessionIDCopy, tenantIDCopy, req.ServiceType, "", "0", "policy_error", 0, err.Error())
					gwProxyRequests.WithLabelValues("policy_error").Inc()
					gwPolicyDenials.WithLabelValues("evaluation_error").Inc()
				}
				unlock()
				cancelIdem()
				return nil, fmt.Errorf("%w: %v", ErrPolicyDenied, err)
			}
			// Shadow mode: policy would deny but enforcement is shadow-only.
			// Log the shadow denial and let the request proceed.
			if policyDecision != nil && !policyDecision.Allowed && policyDecision.Shadow {
				s.logger.Info("policy shadow denied proxy request",
					"session", sessionID,
					"agent", session.AgentAddr,
					"policy", policyDecision.GetDeniedBy(),
					"rule", policyDecision.GetDeniedRule(),
					"reason", policyDecision.GetReason())
				s.logRequestWithPolicy(ctx, sessionIDCopy, tenantIDCopy, req.ServiceType, "", "0", "shadow_denied", 0, policyDecision.GetReason(), policyDecision)
				gwPolicyShadowDenials.WithLabelValues(policyDecision.GetDeniedRule()).Inc()
				// Don't block — continue to budget check and proxy.
			}
		}

		// Budget check: account for both settled spend AND in-flight reservations.
		spentBig, _ := usdc.Parse(session.TotalSpent)
		pending := s.getPendingSpend(sessionIDCopy)
		committed := new(big.Int).Add(spentBig, pending)
		remaining := new(big.Int).Sub(holdBig, committed)

		if priceBig.Cmp(remaining) > 0 {
			unlock()
			lastErr = ErrBudgetExceeded
			continue
		}

		// Reserve: mark this amount as committed so concurrent requests see it.
		s.addPendingSpend(sessionIDCopy, priceBig)

		unlock() // Budget reserved — safe to forward without lock

		// Build reference for this request.
		ref := fmt.Sprintf("%s:req:%d:%s", sessionIDCopy, retries, candidate.ServiceID)
		priceStr := usdc.Format(priceBig)

		// --- Phase 2b: Forward HTTP request (unlocked) ---
		fwdResp, fwdErr := s.forwarder.Forward(ctx, ForwardRequest{
			Endpoint:  candidate.Endpoint,
			Params:    req.Params,
			FromAddr:  agentAddr,
			Amount:    candidate.Price,
			Reference: ref,
		})

		if fwdErr != nil {
			// Forward failed — unreserve budget, no payment.
			if s.circuitBreaker != nil {
				s.circuitBreaker.RecordFailure(candidate.Endpoint)
			}
			unlock = s.locks.Lock(sessionID)
			s.removePendingSpend(sessionIDCopy, priceBig)
			unlock()

			s.logger.Warn("forward failed, trying next candidate",
				"session", sessionID, "seller", candidate.AgentAddress, "error", fwdErr)
			s.logRequest(ctx, sessionIDCopy, tenantIDCopy, req.ServiceType, candidate.AgentAddress,
				"0", "forward_failed", 0, fwdErr.Error())

			if s.recorder != nil {
				_ = s.recorder.RecordTransaction(ctx, ref, agentAddr,
					candidate.AgentAddress, "0", candidate.ServiceID, "failed")
			}

			lastErr = fwdErr
			retries++
			continue
		}

		// Record successful forward for circuit breaker.
		if s.circuitBreaker != nil {
			s.circuitBreaker.RecordSuccess(candidate.Endpoint)
		}

		// --- Phase 2c: Lock, settle payment, update session ---
		unlock = s.locks.Lock(sessionID)

		// Re-read session for authoritative state.
		session, err = s.store.GetSession(ctx, sessionIDCopy)
		if err != nil {
			s.removePendingSpend(sessionIDCopy, priceBig)
			unlock()
			cancelIdem()
			return nil, err
		}

		// Allow settlement if session was closed/expired during forward.
		// The hold has enough because Close/Expire account for pendingSpend.
		if session.Status != StatusActive &&
			session.Status != StatusClosed &&
			session.Status != StatusExpired {
			s.removePendingSpend(sessionIDCopy, priceBig)
			unlock()
			cancelIdem()
			return nil, ErrSessionClosed
		}

		// Update reference with authoritative request count.
		ref = fmt.Sprintf("%s:req:%d:%s", session.ID, session.RequestCount+1, candidate.ServiceID)

		// Compute platform fee based on tenant's take rate.
		sellerAmountStr, feeAmountStr := s.computeFee(ctx, session.TenantID, priceBig)

		// Settle payment with retry (handles transient DB errors).
		// Unlock during sleep to avoid blocking other sessions on the same shard.
		settleAttempt := 0
		settleErr := retry.DoWithUnlock(ctx, 3, 50*time.Millisecond,
			func() { unlock() },
			func() { unlock = s.locks.Lock(sessionID) },
			func() error {
				settleAttempt++
				if settleAttempt > 1 {
					gwSettlementRetries.Inc()
				}
				if feeAmountStr != "0.000000" && s.platformAddr != "" {
					return s.ledger.SettleHoldWithFee(ctx, agentAddr, candidate.AgentAddress, sellerAmountStr, s.platformAddr, feeAmountStr, ref)
				}
				return s.ledger.SettleHold(ctx, agentAddr, candidate.AgentAddress, candidate.Price, ref)
			})
		if settleErr != nil {
			// Service was delivered but payment to seller failed.
			// Return the response to the buyer — they already got value.
			// Do NOT try the next candidate (that would double-deliver).
			s.removePendingSpend(sessionIDCopy, priceBig)
			unlock()

			s.logger.Error("CRITICAL: settlement failed after retries — service delivered but seller not paid, returning response to buyer",
				"session", sessionID, "seller", candidate.AgentAddress, "amount", priceStr, "attempts", 3, "error", settleErr)
			s.logRequest(ctx, sessionIDCopy, tenantIDCopy, req.ServiceType, candidate.AgentAddress, "0", "settlement_failed", fwdResp.LatencyMs, settleErr.Error())

			if s.recorder != nil {
				_ = s.recorder.RecordTransaction(ctx, ref, agentAddr,
					candidate.AgentAddress, candidate.Price, candidate.ServiceID, "settlement_failed")
			}

			if s.webhookEmitter != nil {
				go s.webhookEmitter.EmitSettlementFailed(agentAddr, sessionIDCopy, candidate.AgentAddress, priceStr)
			}

			// Return success to buyer (they got the service) with settlement warning.
			// TotalSpent is NOT updated because the ledger didn't move funds.
			// Manual reconciliation must settle the debt to the seller.
			result := &ProxyResult{
				Response:    fwdResp.Body,
				ServiceUsed: candidate.AgentAddress,
				ServiceName: candidate.ServiceName,
				AmountPaid:  "0.000000", // Settlement failed — buyer was not charged
				TotalSpent:  session.TotalSpent,
				Remaining:   session.Remaining(),
				LatencyMs:   fwdResp.LatencyMs,
				Retries:     retries,
			}

			if idemReserved {
				s.idemCache.complete(sessionID, req.IdempotencyKey, result)
			}
			return result, nil
		}

		// Settlement succeeded — move reservation from pendingSpend to TotalSpent.
		spentBig, _ = usdc.Parse(session.TotalSpent)
		newSpent := new(big.Int).Add(spentBig, priceBig)
		session.TotalSpent = usdc.Format(newSpent)
		session.RequestCount++
		session.UpdatedAt = time.Now()
		s.removePendingSpend(sessionIDCopy, priceBig)

		// Update session with retry (money already moved, must persist).
		// Unlock during sleep to avoid blocking other sessions on the same shard.
		updateErr := retry.DoWithUnlock(ctx, 3, 50*time.Millisecond,
			func() { unlock() },
			func() { unlock = s.locks.Lock(sessionID) },
			func() error { return s.store.UpdateSession(ctx, session) })
		if updateErr != nil {
			s.logger.Error("CRITICAL: settlement succeeded but session update failed after retries",
				"session", sessionID, "seller", candidate.AgentAddress, "amount", priceStr, "error", updateErr)
		}

		unlock() // Done with state — release lock

		// Fire-and-forget: logging, reputation, receipts (no lock needed)
		s.logRequestFull(ctx, sessionIDCopy, tenantIDCopy, req.ServiceType, candidate.AgentAddress, priceStr, feeAmountStr, "success", fwdResp.LatencyMs, "", nil)

		if s.recorder != nil {
			_ = s.recorder.RecordTransaction(ctx, ref, agentAddr,
				candidate.AgentAddress, candidate.Price, candidate.ServiceID, "confirmed")
		}

		if s.receiptIssuer != nil {
			_ = s.receiptIssuer.IssueReceipt(ctx, "gateway", ref, agentAddr,
				candidate.AgentAddress, candidate.Price, candidate.ServiceID, "confirmed", "")
		}

		if s.webhookEmitter != nil {
			go s.webhookEmitter.EmitProxySuccess(agentAddr, sessionIDCopy, candidate.AgentAddress, priceStr)
		}

		// Compute budget state for the response.
		remainingBig := new(big.Int).Sub(holdBig, newSpent)
		if remainingBig.Sign() < 0 {
			remainingBig.SetInt64(0)
		}
		budgetLow := false
		if session.WarnAtPercent > 0 && holdBig.Sign() > 0 {
			threshold := new(big.Int).Mul(holdBig, big.NewInt(int64(100-session.WarnAtPercent)))
			threshold.Div(threshold, big.NewInt(100))
			// budgetLow if spent exceeds (100-warn)% of budget
			budgetLow = newSpent.Cmp(threshold) >= 0
		}

		result := &ProxyResult{
			Response:    fwdResp.Body,
			ServiceUsed: candidate.AgentAddress,
			ServiceName: candidate.ServiceName,
			AmountPaid:  priceStr,
			TotalSpent:  usdc.Format(newSpent),
			Remaining:   usdc.Format(remainingBig),
			BudgetLow:   budgetLow,
			LatencyMs:   fwdResp.LatencyMs,
			Retries:     retries,
		}

		// Cache successful result for idempotency.
		if idemReserved {
			s.idemCache.complete(sessionID, req.IdempotencyKey, result)
		}

		gwProxyRequests.WithLabelValues("success").Inc()
		gwProxyLatency.Observe(time.Since(proxyStart).Seconds())
		if amt := parseDecimal(priceStr); amt > 0 {
			gwSettlementAmount.Observe(amt)
		}

		return result, nil
	}

	// All candidates failed.
	cancelIdem()
	gwProxyRequests.WithLabelValues("all_failed").Inc()

	if lastErr != nil {
		if me, ok := lastErr.(*MoneyError); ok {
			return nil, &MoneyError{
				Err:         fmt.Errorf("%w: %v", ErrProxyFailed, me.Err),
				FundsStatus: me.FundsStatus,
				Recovery:    me.Recovery,
				Amount:      me.Amount,
				Reference:   me.Reference,
			}
		}
		return nil, fmt.Errorf("%w: %v", ErrProxyFailed, lastErr)
	}
	return nil, ErrProxyFailed
}

// DryRun checks whether a proxy request would succeed without moving money
// or incrementing counters.
func (s *Service) DryRun(ctx context.Context, sessionID string, req ProxyRequest) (*DryRunResult, error) {
	if !validServiceType.MatchString(req.ServiceType) {
		return nil, fmt.Errorf("%w: invalid service type %q", ErrNoServiceAvailable, req.ServiceType)
	}

	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	result := &DryRunResult{}

	// Check session status.
	if session.Status != StatusActive {
		result.DenyReason = "session is not active"
		return result, nil
	}
	if session.IsExpired() {
		result.DenyReason = "session has expired"
		return result, nil
	}

	// Policy check.
	if s.policyEvaluator != nil {
		projected := *session
		decision, pErr := s.policyEvaluator.EvaluateProxy(ctx, &projected, req.ServiceType)
		if pErr != nil {
			if decision != nil {
				result.PolicyResult = decision
				result.DenyReason = decision.Reason
			} else {
				result.DenyReason = "policy evaluation failed: " + pErr.Error()
			}
			return result, nil
		}
		result.PolicyResult = decision
		if decision != nil && !decision.Allowed && !decision.Shadow {
			result.DenyReason = decision.Reason
			return result, nil
		}
	}
	result.Allowed = true

	// Budget check.
	holdBig, _ := usdc.Parse(session.MaxTotal)
	spentBig, _ := usdc.Parse(session.TotalSpent)
	remaining := new(big.Int).Sub(holdBig, spentBig)
	if remaining.Sign() < 0 {
		remaining.SetInt64(0)
	}
	result.Remaining = usdc.Format(remaining)
	result.BudgetOK = remaining.Sign() > 0

	// Resolver check.
	candidates, rErr := s.resolver.Resolve(ctx, req, session.Strategy, session.MaxPerRequest)
	if rErr != nil {
		result.ServiceFound = false
		if result.BudgetOK {
			result.Allowed = false
			result.DenyReason = "no service available"
		}
		return result, nil
	}

	result.ServiceFound = true
	if len(candidates) > 0 {
		result.BestPrice = candidates[0].Price
		result.BestService = candidates[0].ServiceName
		// Check if budget covers best price.
		bestBig, ok := usdc.Parse(candidates[0].Price)
		if ok && bestBig.Cmp(remaining) > 0 {
			result.BudgetOK = false
			result.Allowed = false
			result.DenyReason = "budget insufficient for cheapest service"
		}
	}

	return result, nil
}

// CloseSession settles a session, releasing unspent funds.
func (s *Service) CloseSession(ctx context.Context, sessionID, callerAddr string) (*Session, error) {
	unlock := s.locks.Lock(sessionID)
	defer unlock()

	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	if !strings.EqualFold(callerAddr, session.AgentAddr) {
		return nil, ErrUnauthorized
	}

	// Idempotent: if already closed/expired, return current state without error.
	if session.Status != StatusActive {
		return session, nil
	}

	// Release only the unused portion, accounting for in-flight reservations.
	// Per-request SettleHold already settled the spent portion.
	// PendingSpend covers in-flight requests that haven't settled yet.
	spentBig, _ := usdc.Parse(session.TotalSpent)
	holdBig, _ := usdc.Parse(session.MaxTotal)
	pending := s.getPendingSpend(sessionID)
	committed := new(big.Int).Add(spentBig, pending)
	unused := new(big.Int).Sub(holdBig, committed)

	if unused.Sign() > 0 {
		unusedStr := usdc.Format(unused)
		if err := s.ledger.ReleaseHold(ctx, session.AgentAddr, unusedStr, session.ID); err != nil {
			return nil, &MoneyError{
				Err:         fmt.Errorf("failed to release unused hold: %w", err),
				FundsStatus: "held_pending",
				Recovery:    "Session close failed because the unused hold could not be released. Contact support with the reference to release your funds.",
				Amount:      unusedStr,
				Reference:   session.ID,
			}
		}
	}

	// Clean up pendingSpend entry if zero (no in-flight requests).
	if pending.Sign() == 0 {
		s.pendingSpend.Delete(sessionID)
	}

	s.rateLimit.remove(sessionID)

	session.Status = StatusClosed
	session.UpdatedAt = time.Now()

	// CRITICAL: Funds already moved. Retry the status update because if this fails
	// and the session stays "active", the auto-close timer could release holds that no longer exist.
	// Unlock during sleep to avoid blocking other sessions on the same shard.
	updateErr := retry.DoWithUnlock(ctx, 3, 50*time.Millisecond,
		func() { unlock() },
		func() { unlock = s.locks.Lock(sessionID) },
		func() error {
			if err := s.store.UpdateSession(ctx, session); err != nil {
				s.logger.Warn("gateway session status update retry", "session", sessionID, "error", err)
				return err
			}
			return nil
		})
	if updateErr != nil {
		// All retries exhausted. Mark as settlement_failed so auto-close won't re-process.
		session.Status = StatusSettlementFailed
		if retryErr := s.store.UpdateSession(ctx, session); retryErr != nil {
			s.logger.Error("CRITICAL: gateway session ALL status updates failed including sentinel",
				"session", sessionID, "error", retryErr)
		} else {
			s.logger.Error("CRITICAL: gateway session marked as settlement_failed — funds moved but closed status could not be set",
				"session", sessionID)
		}
		return nil, &MoneyError{
			Err:         fmt.Errorf("failed to update session after close: %w", updateErr),
			FundsStatus: "settled_safe",
			Recovery:    "All funds were correctly settled but the session status could not be updated. No action needed regarding your funds.",
			Amount:      session.TotalSpent,
			Reference:   session.ID,
		}
	}

	gwSessionsClosed.WithLabelValues("client").Inc()
	gwActiveSessions.Dec()

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitSessionClosed(session.AgentAddr, session.ID, session.TotalSpent, string(session.Status))
	}

	return session, nil
}

// SingleCall creates an ephemeral session, proxies one request, and closes.
// This is the simplest path: one HTTP call does everything.
func (s *Service) SingleCall(ctx context.Context, agentAddr, tenantID string, req SingleCallRequest) (*SingleCallResult, error) {
	// Create ephemeral session sized to maxPrice.
	session, err := s.CreateSession(ctx, agentAddr, tenantID, CreateSessionRequest{
		MaxTotal:      req.MaxPrice,
		MaxPerRequest: req.MaxPrice,
		ExpiresInSec:  300, // 5 minute safety net
	})
	if err != nil {
		return nil, err
	}

	// Proxy the request.
	proxyResult, proxyErr := s.Proxy(ctx, session.ID, ProxyRequest{
		ServiceType: req.ServiceType,
		Params:      req.Params,
	})

	// Always close the session to release any unspent hold.
	// Retry because a stuck hold locks buyer funds until the 5-min expiry timer.
	closeErr := retry.Do(ctx, 3, 50*time.Millisecond, func() error {
		_, err := s.CloseSession(ctx, session.ID, agentAddr)
		return err
	})
	if closeErr != nil {
		s.logger.Error("CRITICAL: failed to close ephemeral gateway session after retries — hold locked until expiry",
			"session", session.ID, "amount", req.MaxPrice, "error", closeErr)
	}

	if proxyErr != nil {
		return nil, proxyErr
	}

	return &SingleCallResult{
		Response:    proxyResult.Response,
		ServiceUsed: proxyResult.ServiceUsed,
		ServiceName: proxyResult.ServiceName,
		AmountPaid:  proxyResult.AmountPaid,
		LatencyMs:   proxyResult.LatencyMs,
	}, nil
}

// GetSession returns a session by ID.
func (s *Service) GetSession(ctx context.Context, id string) (*Session, error) {
	return s.store.GetSession(ctx, id)
}

// ListSessions returns sessions for an agent.
func (s *Service) ListSessions(ctx context.Context, agentAddr string, limit int) ([]*Session, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListSessions(ctx, strings.ToLower(agentAddr), limit)
}

// ListByStatus returns sessions in a given status.
func (s *Service) ListByStatus(ctx context.Context, status Status, limit int) ([]*Session, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.ListByStatus(ctx, status, limit)
}

// ListLogs returns request logs for a session.
func (s *Service) ListLogs(ctx context.Context, sessionID string, limit int) ([]*RequestLog, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.ListLogs(ctx, sessionID, limit)
}

// computeFee calculates the platform fee for a given price and tenant.
// Returns the fee amount as a USDC string and the seller amount (price - fee).
// If there's no tenant, no settings provider, or bps is 0, fee is "0.000000".
func (s *Service) computeFee(ctx context.Context, tenantID string, priceBig *big.Int) (sellerAmount, feeAmount string) {
	zero := "0.000000"
	priceStr := usdc.Format(priceBig)

	if tenantID == "" || s.tenantSettings == nil || s.platformAddr == "" {
		return priceStr, zero
	}

	bps, err := s.tenantSettings.GetTakeRateBPS(ctx, tenantID)
	if err != nil || bps <= 0 {
		return priceStr, zero
	}

	// fee = price * bps / 10000
	feeBig := new(big.Int).Mul(priceBig, big.NewInt(int64(bps)))
	feeBig.Div(feeBig, big.NewInt(10000))

	if feeBig.Sign() <= 0 {
		return priceStr, zero
	}

	sellerBig := new(big.Int).Sub(priceBig, feeBig)
	return usdc.Format(sellerBig), usdc.Format(feeBig)
}

// logRequest creates a request log entry.
func (s *Service) logRequest(ctx context.Context, sessionID, tenantID, serviceType, agentCalled, amount, status string, latencyMs int64, errMsg string) {
	s.logRequestFull(ctx, sessionID, tenantID, serviceType, agentCalled, amount, "", status, latencyMs, errMsg, nil)
}

// logRequestWithPolicy creates a request log entry with an optional policy decision.
func (s *Service) logRequestWithPolicy(ctx context.Context, sessionID, tenantID, serviceType, agentCalled, amount, status string, latencyMs int64, errMsg string, policy *PolicyDecision) {
	s.logRequestFull(ctx, sessionID, tenantID, serviceType, agentCalled, amount, "", status, latencyMs, errMsg, policy)
}

// logRequestFull creates a request log entry with all fields.
func (s *Service) logRequestFull(ctx context.Context, sessionID, tenantID, serviceType, agentCalled, amount, feeAmount, status string, latencyMs int64, errMsg string, policy *PolicyDecision) {
	log := &RequestLog{
		ID:           idgen.WithPrefix("gwlog_"),
		SessionID:    sessionID,
		TenantID:     tenantID,
		ServiceType:  serviceType,
		AgentCalled:  agentCalled,
		Amount:       amount,
		FeeAmount:    feeAmount,
		Status:       status,
		LatencyMs:    latencyMs,
		Error:        errMsg,
		PolicyResult: policy,
		CreatedAt:    time.Now(),
	}
	if err := s.store.CreateLog(ctx, log); err != nil {
		s.logger.Warn("failed to create gateway request log", "error", err)
	}
}

// AutoCloseExpired closes an expired session without caller authorization.
// Called by the Timer goroutine. Sets status to StatusExpired and releases unspent funds.
func (s *Service) AutoCloseExpired(ctx context.Context, session *Session) error {
	unlock := s.locks.Lock(session.ID)
	defer unlock()

	// Re-read under lock
	fresh, err := s.store.GetSession(ctx, session.ID)
	if err != nil {
		return err
	}
	if fresh.Status != StatusActive {
		return ErrSessionClosed
	}

	// Release only the unused portion, accounting for in-flight reservations.
	spentBig, _ := usdc.Parse(fresh.TotalSpent)
	holdBig, _ := usdc.Parse(fresh.MaxTotal)
	pending := s.getPendingSpend(fresh.ID)
	committed := new(big.Int).Add(spentBig, pending)
	unused := new(big.Int).Sub(holdBig, committed)

	if unused.Sign() > 0 {
		unusedStr := usdc.Format(unused)
		if err := s.ledger.ReleaseHold(ctx, fresh.AgentAddr, unusedStr, fresh.ID); err != nil {
			return fmt.Errorf("failed to release unused hold: %w", err)
		}
	}

	// Clean up pendingSpend entry if zero.
	if pending.Sign() == 0 {
		s.pendingSpend.Delete(fresh.ID)
	}

	s.rateLimit.remove(fresh.ID)

	fresh.Status = StatusExpired
	fresh.UpdatedAt = time.Now()

	// Funds already released. Retry status update to prevent re-processing.
	// Unlock during sleep to avoid blocking other sessions on the same shard.
	updateErr := retry.DoWithUnlock(ctx, 3, 50*time.Millisecond,
		func() { unlock() },
		func() { unlock = s.locks.Lock(session.ID) },
		func() error {
			if err := s.store.UpdateSession(ctx, fresh); err != nil {
				s.logger.Warn("auto-close status update retry", "session", fresh.ID, "error", err)
				return err
			}
			return nil
		})
	if updateErr == nil {
		gwExpiredSessionsClosed.Inc()
		gwSessionsClosed.WithLabelValues("expired").Inc()
		gwActiveSessions.Dec()
		if s.webhookEmitter != nil {
			go s.webhookEmitter.EmitSessionClosed(fresh.AgentAddr, fresh.ID, fresh.TotalSpent, string(fresh.Status))
		}
		return nil
	}
	// Mark as settlement_failed so next auto-close cycle skips it.
	fresh.Status = StatusSettlementFailed
	if retryErr := s.store.UpdateSession(ctx, fresh); retryErr != nil {
		s.logger.Error("CRITICAL: auto-close ALL status updates failed including sentinel",
			"session", fresh.ID, "error", retryErr)
	}
	return fmt.Errorf("failed to update expired session: %w", updateErr)
}
