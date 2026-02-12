package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/syncutil"
	"github.com/mbd888/alancoin/internal/usdc"
)

// idempotencyEntry caches a proxy result for deduplication.
type idempotencyEntry struct {
	result    *ProxyResult
	err       error
	expiresAt time.Time
}

// idempotencyCache is a bounded TTL cache for proxy result deduplication.
type idempotencyCache struct {
	entries sync.Map
	ttl     time.Duration
}

func newIdempotencyCache(ttl time.Duration) *idempotencyCache {
	return &idempotencyCache{ttl: ttl}
}

func (c *idempotencyCache) key(sessionID, idempotencyKey string) string {
	return sessionID + ":" + idempotencyKey
}

func (c *idempotencyCache) get(sessionID, idempotencyKey string) (*ProxyResult, error, bool) {
	v, ok := c.entries.Load(c.key(sessionID, idempotencyKey))
	if !ok {
		return nil, nil, false
	}
	entry := v.(*idempotencyEntry)
	if time.Now().After(entry.expiresAt) {
		c.entries.Delete(c.key(sessionID, idempotencyKey))
		return nil, nil, false
	}
	return entry.result, entry.err, true
}

func (c *idempotencyCache) set(sessionID, idempotencyKey string, result *ProxyResult, err error) {
	c.entries.Store(c.key(sessionID, idempotencyKey), &idempotencyEntry{
		result:    result,
		err:       err,
		expiresAt: time.Now().Add(c.ttl),
	})
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

	if len(req.AllowedTypes) > 20 {
		return nil, fmt.Errorf("%w: allowedTypes exceeds maximum of 20 entries", ErrInvalidAmount)
	}
	for _, t := range req.AllowedTypes {
		if len(t) > 100 {
			return nil, fmt.Errorf("%w: service type exceeds maximum length of 100", ErrInvalidAmount)
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
// Lock strategy: hold the per-session lock only for state reads and writes,
// NOT during the HTTP forward (which can take up to 30s). This allows
// concurrent proxy requests within the same session.
func (s *Service) Proxy(ctx context.Context, sessionID string, req ProxyRequest) (*ProxyResult, error) {
	if len(req.ServiceType) > 100 {
		return nil, fmt.Errorf("%w: service type exceeds maximum length of 100", ErrNoServiceAvailable)
	}

	// Idempotency: if the client provides a key and we've seen it before, return cached result.
	if req.IdempotencyKey != "" {
		if cached, cachedErr, ok := s.idemCache.get(sessionID, req.IdempotencyKey); ok {
			return cached, cachedErr
		}
	}

	// --- Phase 1: Lock, validate session, resolve candidates, snapshot state ---
	unlock := s.locks.Lock(sessionID)

	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		unlock()
		return nil, err
	}

	if session.Status != StatusActive {
		unlock()
		return nil, ErrSessionClosed
	}
	if session.IsExpired() {
		unlock()
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
			return nil, fmt.Errorf("%w: service type %q not in allowed types", ErrNoServiceAvailable, req.ServiceType)
		}
	}

	// Resolve candidates
	candidates, err := s.resolver.Resolve(ctx, req, session.Strategy, session.MaxPerRequest)
	if err != nil {
		s.logRequest(ctx, session.ID, req.ServiceType, "", "0", "no_service", 0, err.Error())
		unlock()
		return nil, err
	}

	// Snapshot budget state for candidate filtering.
	holdBig, _ := usdc.Parse(session.MaxTotal)
	maxPerBig, _ := usdc.Parse(session.MaxPerRequest)
	agentAddr := session.AgentAddr
	sessionIDCopy := session.ID

	unlock() // Release lock before HTTP forwards

	// --- Phase 2: Try candidates (UNLOCKED — HTTP forward can take 30s) ---
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

		// Quick budget check against snapshot (may be stale but avoids pointless forwards)
		// The authoritative check happens under lock in Phase 3.
		// We don't hold the lock here, so we do an optimistic read.
		if sess, sErr := s.store.GetSession(ctx, sessionIDCopy); sErr == nil {
			spent, _ := usdc.Parse(sess.TotalSpent)
			rem := new(big.Int).Sub(holdBig, spent)
			if priceBig.Cmp(rem) > 0 {
				lastErr = ErrBudgetExceeded
				continue
			}
		}

		// Build reference for this request.
		ref := fmt.Sprintf("%s:req:%d:%s", sessionIDCopy, retries, candidate.ServiceID)
		priceStr := usdc.Format(priceBig)

		// Forward HTTP request — UNLOCKED, can run concurrently with other proxy calls.
		fwdResp, fwdErr := s.forwarder.Forward(ctx, ForwardRequest{
			Endpoint:  candidate.Endpoint,
			Params:    req.Params,
			FromAddr:  agentAddr,
			Amount:    candidate.Price,
			Reference: ref,
		})

		if fwdErr != nil {
			// Forward failed — no payment was made. Try next candidate.
			s.logger.Warn("forward failed, trying next candidate", "session", sessionID, "seller", candidate.AgentAddress, "error", fwdErr)
			s.logRequest(ctx, sessionIDCopy, req.ServiceType, candidate.AgentAddress, "0", "forward_failed", 0, fwdErr.Error())

			// Record failed forward for reputation (no money moved)
			if s.recorder != nil {
				_ = s.recorder.RecordTransaction(ctx, ref, agentAddr,
					candidate.AgentAddress, "0", candidate.ServiceID, "failed")
			}

			lastErr = fwdErr
			retries++
			continue
		}

		// --- Phase 3: Lock, re-validate budget, settle, update session ---
		unlock = s.locks.Lock(sessionID)

		// Re-read session under lock to get authoritative state.
		session, err = s.store.GetSession(ctx, sessionIDCopy)
		if err != nil {
			unlock()
			return nil, err
		}
		if session.Status != StatusActive {
			unlock()
			return nil, ErrSessionClosed
		}

		// Authoritative budget check — another concurrent request may have spent funds.
		spentBig, _ := usdc.Parse(session.TotalSpent)
		remaining := new(big.Int).Sub(holdBig, spentBig)
		if priceBig.Cmp(remaining) > 0 {
			unlock()
			// Service was delivered but budget consumed by concurrent request.
			// This is a rare race — log it but don't charge the buyer.
			s.logger.Warn("budget consumed by concurrent request after successful forward",
				"session", sessionID, "seller", candidate.AgentAddress, "price", candidate.Price)
			lastErr = ErrBudgetExceeded
			continue
		}

		// Update reference with authoritative request count.
		ref = fmt.Sprintf("%s:req:%d:%s", session.ID, session.RequestCount+1, candidate.ServiceID)

		// Forward succeeded — now settle payment.
		if err := s.ledger.SettleHold(ctx, agentAddr, candidate.AgentAddress, candidate.Price, ref); err != nil {
			s.logger.Error("CRITICAL: settle failed after successful forward — service delivered but seller not paid",
				"session", sessionID, "seller", candidate.AgentAddress, "error", err)
			unlock()
			lastErr = &MoneyError{
				Err:         err,
				FundsStatus: "held_safe",
				Recovery:    "Service was delivered but payment settlement failed. Your funds are safely held. Contact support with the reference.",
				Amount:      candidate.Price,
				Reference:   ref,
			}
			retries++
			continue
		}

		// Success — update session.
		newSpent := new(big.Int).Add(spentBig, priceBig)
		session.TotalSpent = usdc.Format(newSpent)
		session.RequestCount++
		session.UpdatedAt = time.Now()

		if err := s.store.UpdateSession(ctx, session); err != nil {
			s.logger.Error("failed to update session after successful proxy", "session", sessionID, "error", err)
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

		result := &ProxyResult{
			Response:    fwdResp.Body,
			ServiceUsed: candidate.AgentAddress,
			ServiceName: candidate.ServiceName,
			AmountPaid:  priceStr,
			LatencyMs:   fwdResp.LatencyMs,
			Retries:     retries,
		}

		// Cache for idempotency so retries return the same result.
		if req.IdempotencyKey != "" {
			s.idemCache.set(sessionID, req.IdempotencyKey, result, nil)
		}

		return result, nil
	}

	if lastErr != nil {
		// Propagate MoneyError through ErrProxyFailed so handlers can extract funds_status
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

	if session.Status != StatusActive {
		return nil, ErrSessionClosed
	}

	// Release only the unused portion. Per-request SettleHold already settled the spent portion.
	spentBig, _ := usdc.Parse(session.TotalSpent)
	holdBig, _ := usdc.Parse(session.MaxTotal)
	unused := new(big.Int).Sub(holdBig, spentBig)

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

	// Release only the unused portion. Per-request SettleHold already settled the spent portion.
	spentBig, _ := usdc.Parse(fresh.TotalSpent)
	holdBig, _ := usdc.Parse(fresh.MaxTotal)
	unused := new(big.Int).Sub(holdBig, spentBig)

	if unused.Sign() > 0 {
		unusedStr := usdc.Format(unused)
		if err := s.ledger.ReleaseHold(ctx, fresh.AgentAddr, unusedStr, fresh.ID); err != nil {
			return fmt.Errorf("failed to release unused hold: %w", err)
		}
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
