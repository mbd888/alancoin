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

	"github.com/mbd888/alancoin/internal/idgen"
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
	store         Store
	resolver      *Resolver
	forwarder     *Forwarder
	ledger        LedgerService
	recorder      TransactionRecorder
	receiptIssuer ReceiptIssuer
	logger        *slog.Logger
	locks         syncutil.ShardedMutex
	idemCache     *idempotencyCache

	// pendingSpend tracks in-flight budget reservations per session.
	// Key: sessionID, Value: *big.Int (sum of reserved amounts).
	// Accessed under per-session lock (s.locks) for correctness.
	pendingSpend sync.Map
}

// SweepIdempotencyCache removes expired entries. Called by the Timer.
func (s *Service) SweepIdempotencyCache() int {
	return s.idemCache.sweep()
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
func (s *Service) CreateSession(ctx context.Context, agentAddr string, req CreateSessionRequest) (*Session, error) {
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

	now := time.Now()
	session := &Session{
		ID:            idgen.WithPrefix("gw_"),
		AgentAddr:     strings.ToLower(agentAddr),
		MaxTotal:      req.MaxTotal,
		MaxPerRequest: req.MaxPerRequest,
		TotalSpent:    "0.000000",
		RequestCount:  0,
		Strategy:      strategy,
		AllowedTypes:  req.AllowedTypes,
		WarnAtPercent: warnAt,
		Status:        StatusActive,
		ExpiresAt:     now.Add(expiresIn),
		CreatedAt:     now,
		UpdatedAt:     now,
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

	// Check allowed types
	if len(session.AllowedTypes) > 0 {
		allowed := false
		for _, t := range session.AllowedTypes {
			if t == req.ServiceType {
				allowed = true
				break
			}
		}
		if !allowed {
			unlock()
			cancelIdem()
			return nil, fmt.Errorf("%w: service type %q not in allowed types", ErrNoServiceAvailable, req.ServiceType)
		}
	}

	// Resolve candidates
	candidates, err := s.resolver.Resolve(ctx, req, session.Strategy, session.MaxPerRequest)
	if err != nil {
		s.logRequest(ctx, session.ID, req.ServiceType, "", "0", "no_service", 0, err.Error())
		unlock()
		cancelIdem()
		return nil, err
	}

	// Snapshot immutable session properties.
	holdBig, _ := usdc.Parse(session.MaxTotal)
	maxPerBig, _ := usdc.Parse(session.MaxPerRequest)
	agentAddr := session.AgentAddr
	sessionIDCopy := session.ID

	unlock() // Release lock — candidates resolved, ready to iterate

	// --- Phase 2: Try candidates ---
	var lastErr error
	retries := 0

	for _, candidate := range candidates {
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
			unlock = s.locks.Lock(sessionID)
			s.removePendingSpend(sessionIDCopy, priceBig)
			unlock()

			s.logger.Warn("forward failed, trying next candidate",
				"session", sessionID, "seller", candidate.AgentAddress, "error", fwdErr)
			s.logRequest(ctx, sessionIDCopy, req.ServiceType, candidate.AgentAddress,
				"0", "forward_failed", 0, fwdErr.Error())

			if s.recorder != nil {
				_ = s.recorder.RecordTransaction(ctx, ref, agentAddr,
					candidate.AgentAddress, "0", candidate.ServiceID, "failed")
			}

			lastErr = fwdErr
			retries++
			continue
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

		// Settle payment with retry (handles transient DB errors).
		var settleErr error
		for attempt := 0; attempt < 3; attempt++ {
			settleErr = s.ledger.SettleHold(ctx, agentAddr, candidate.AgentAddress, candidate.Price, ref)
			if settleErr == nil {
				break
			}
			if attempt < 2 {
				time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
			}
		}
		if settleErr != nil {
			s.removePendingSpend(sessionIDCopy, priceBig)
			s.logger.Error("settlement failed after retries — service delivered but seller not paid",
				"session", sessionID, "seller", candidate.AgentAddress, "attempts", 3, "error", settleErr)
			unlock()
			lastErr = &MoneyError{
				Err:         settleErr,
				FundsStatus: "held_safe",
				Recovery:    "Service was delivered but payment settlement failed after 3 attempts. Your funds are safely held. Contact support with the reference.",
				Amount:      candidate.Price,
				Reference:   ref,
			}
			retries++
			continue
		}

		// Settlement succeeded — move reservation from pendingSpend to TotalSpent.
		spentBig, _ = usdc.Parse(session.TotalSpent)
		newSpent := new(big.Int).Add(spentBig, priceBig)
		session.TotalSpent = usdc.Format(newSpent)
		session.RequestCount++
		session.UpdatedAt = time.Now()
		s.removePendingSpend(sessionIDCopy, priceBig)

		// Update session with retry (money already moved, must persist).
		var updateErr error
		for attempt := 0; attempt < 3; attempt++ {
			if updateErr = s.store.UpdateSession(ctx, session); updateErr == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
		}
		if updateErr != nil {
			s.logger.Error("CRITICAL: settlement succeeded but session update failed after retries",
				"session", sessionID, "seller", candidate.AgentAddress, "amount", priceStr, "error", updateErr)
		}

		unlock() // Done with state — release lock

		// Fire-and-forget: logging, reputation, receipts (no lock needed)
		s.logRequest(ctx, sessionIDCopy, req.ServiceType, candidate.AgentAddress, priceStr, "success", fwdResp.LatencyMs, "")

		if s.recorder != nil {
			_ = s.recorder.RecordTransaction(ctx, ref, agentAddr,
				candidate.AgentAddress, candidate.Price, candidate.ServiceID, "confirmed")
		}

		if s.receiptIssuer != nil {
			_ = s.receiptIssuer.IssueReceipt(ctx, "gateway", ref, agentAddr,
				candidate.AgentAddress, candidate.Price, candidate.ServiceID, "confirmed", "")
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

		return result, nil
	}

	// All candidates failed.
	cancelIdem()

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

	session.Status = StatusClosed
	session.UpdatedAt = time.Now()

	// CRITICAL: Funds already moved. Retry the status update because if this fails
	// and the session stays "active", the auto-close timer could release holds that no longer exist.
	var updateErr error
	for attempt := 0; attempt < 3; attempt++ {
		if updateErr = s.store.UpdateSession(ctx, session); updateErr == nil {
			break
		}
		s.logger.Warn("gateway session status update retry", "session", sessionID, "attempt", attempt+1, "error", updateErr)
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
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

	return session, nil
}

// SingleCall creates an ephemeral session, proxies one request, and closes.
// This is the simplest path: one HTTP call does everything.
func (s *Service) SingleCall(ctx context.Context, agentAddr string, req SingleCallRequest) (*SingleCallResult, error) {
	// Create ephemeral session sized to maxPrice.
	session, err := s.CreateSession(ctx, agentAddr, CreateSessionRequest{
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
	if _, closeErr := s.CloseSession(ctx, session.ID, agentAddr); closeErr != nil {
		s.logger.Warn("failed to close ephemeral gateway session",
			"session", session.ID, "error", closeErr)
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

// ListLogs returns request logs for a session.
func (s *Service) ListLogs(ctx context.Context, sessionID string, limit int) ([]*RequestLog, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.ListLogs(ctx, sessionID, limit)
}

// logRequest creates a request log entry.
func (s *Service) logRequest(ctx context.Context, sessionID, serviceType, agentCalled, amount, status string, latencyMs int64, errMsg string) {
	log := &RequestLog{
		ID:          idgen.WithPrefix("gwlog_"),
		SessionID:   sessionID,
		ServiceType: serviceType,
		AgentCalled: agentCalled,
		Amount:      amount,
		Status:      status,
		LatencyMs:   latencyMs,
		Error:       errMsg,
		CreatedAt:   time.Now(),
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

	fresh.Status = StatusExpired
	fresh.UpdatedAt = time.Now()

	// Funds already released. Retry status update to prevent re-processing.
	var updateErr error
	for attempt := 0; attempt < 3; attempt++ {
		if updateErr = s.store.UpdateSession(ctx, fresh); updateErr == nil {
			return nil
		}
		s.logger.Warn("auto-close status update retry", "session", fresh.ID, "attempt", attempt+1, "error", updateErr)
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	// Mark as settlement_failed so next auto-close cycle skips it.
	fresh.Status = StatusSettlementFailed
	if retryErr := s.store.UpdateSession(ctx, fresh); retryErr != nil {
		s.logger.Error("CRITICAL: auto-close ALL status updates failed including sentinel",
			"session", fresh.ID, "error", retryErr)
	}
	return fmt.Errorf("failed to update expired session: %w", updateErr)
}
