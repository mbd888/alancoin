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
	"github.com/mbd888/alancoin/internal/usdc"
)

// Service implements gateway business logic.
type Service struct {
	store     Store
	resolver  *Resolver
	forwarder *Forwarder
	ledger    LedgerService
	logger    *slog.Logger
	locks     sync.Map // per-session mutex
}

// NewService creates a new gateway service.
func NewService(store Store, resolver *Resolver, forwarder *Forwarder, ledger LedgerService, logger *slog.Logger) *Service {
	return &Service{
		store:     store,
		resolver:  resolver,
		forwarder: forwarder,
		ledger:    ledger,
		logger:    logger,
	}
}

// sessionLock returns a mutex for the given session ID.
func (s *Service) sessionLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
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
		return nil, fmt.Errorf("failed to hold gateway funds: %w", err)
	}

	if err := s.store.CreateSession(ctx, session); err != nil {
		// Best-effort release if store fails
		_ = s.ledger.ReleaseHold(ctx, session.AgentAddr, session.MaxTotal, session.ID)
		return nil, fmt.Errorf("failed to create gateway session: %w", err)
	}

	return session, nil
}

// Proxy handles a single proxy request within a session.
func (s *Service) Proxy(ctx context.Context, sessionID string, req ProxyRequest) (*ProxyResult, error) {
	mu := s.sessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	if session.Status != StatusActive {
		return nil, ErrSessionClosed
	}
	if session.IsExpired() {
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
			return nil, fmt.Errorf("%w: service type %q not in allowed types", ErrNoServiceAvailable, req.ServiceType)
		}
	}

	// Resolve candidates
	candidates, err := s.resolver.Resolve(ctx, req, session.Strategy, session.MaxPerRequest)
	if err != nil {
		s.logRequest(ctx, session.ID, req.ServiceType, "", "0", "no_service", 0, err.Error())
		return nil, err
	}

	spentBig, _ := usdc.Parse(session.TotalSpent)
	holdBig, _ := usdc.Parse(session.MaxTotal)
	remaining := new(big.Int).Sub(holdBig, spentBig)
	maxPerBig, _ := usdc.Parse(session.MaxPerRequest)

	var lastErr error
	retries := 0

	for _, candidate := range candidates {
		priceBig, ok := usdc.Parse(candidate.Price)
		if !ok || priceBig.Sign() <= 0 {
			continue
		}

		// Check per-request limit
		if priceBig.Cmp(maxPerBig) > 0 {
			continue
		}

		// Check remaining budget
		if priceBig.Cmp(remaining) > 0 {
			lastErr = ErrBudgetExceeded
			continue
		}

		// Payment: confirm hold from buyer + deposit to seller
		ref := fmt.Sprintf("%s:req:%d:%s", session.ID, session.RequestCount+1, candidate.ServiceID)

		if err := s.ledger.ConfirmHold(ctx, session.AgentAddr, candidate.Price, ref); err != nil {
			s.logger.Warn("confirm hold failed", "session", sessionID, "error", err)
			lastErr = err
			continue
		}

		if err := s.ledger.Deposit(ctx, candidate.AgentAddress, candidate.Price, ref); err != nil {
			s.logger.Error("deposit failed after confirm hold", "session", sessionID, "seller", candidate.AgentAddress, "error", err)
			// Payment confirmed but deposit failed — log for manual resolution
			lastErr = err
			retries++
			continue
		}

		// Forward HTTP request
		fwdResp, err := s.forwarder.Forward(ctx, ForwardRequest{
			Endpoint:  candidate.Endpoint,
			Params:    req.Params,
			FromAddr:  session.AgentAddr,
			Amount:    candidate.Price,
			Reference: ref,
		})

		if err != nil {
			// Payment already happened. Service failed — reputation issue.
			s.logger.Warn("forward failed after payment", "session", sessionID, "seller", candidate.AgentAddress, "error", err)
			s.logRequest(ctx, session.ID, req.ServiceType, candidate.AgentAddress, candidate.Price, "forward_failed", 0, err.Error())

			// Update session spend even though forward failed (payment was made)
			newSpent := new(big.Int).Add(spentBig, priceBig)
			session.TotalSpent = usdc.Format(newSpent)
			session.RequestCount++
			session.UpdatedAt = time.Now()
			_ = s.store.UpdateSession(ctx, session)

			remaining = new(big.Int).Sub(holdBig, newSpent)
			spentBig = newSpent
			lastErr = err
			retries++
			continue
		}

		// Success — update session
		newSpent := new(big.Int).Add(spentBig, priceBig)
		session.TotalSpent = usdc.Format(newSpent)
		session.RequestCount++
		session.UpdatedAt = time.Now()

		if err := s.store.UpdateSession(ctx, session); err != nil {
			s.logger.Error("failed to update session after successful proxy", "session", sessionID, "error", err)
		}

		s.logRequest(ctx, session.ID, req.ServiceType, candidate.AgentAddress, candidate.Price, "success", fwdResp.LatencyMs, "")

		return &ProxyResult{
			Response:    fwdResp.Body,
			ServiceUsed: candidate.AgentAddress,
			ServiceName: candidate.ServiceName,
			AmountPaid:  candidate.Price,
			LatencyMs:   fwdResp.LatencyMs,
			Retries:     retries,
		}, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrProxyFailed, lastErr)
	}
	return nil, ErrProxyFailed
}

// CloseSession settles a session, releasing unspent funds.
func (s *Service) CloseSession(ctx context.Context, sessionID, callerAddr string) (*Session, error) {
	mu := s.sessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

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

	// Release unspent hold
	spentBig, _ := usdc.Parse(session.TotalSpent)
	holdBig, _ := usdc.Parse(session.MaxTotal)
	unused := new(big.Int).Sub(holdBig, spentBig)

	if unused.Sign() > 0 {
		unusedStr := usdc.Format(unused)
		if err := s.ledger.ReleaseHold(ctx, session.AgentAddr, unusedStr, session.ID); err != nil {
			return nil, fmt.Errorf("failed to release unused hold: %w", err)
		}
	}

	session.Status = StatusClosed
	session.UpdatedAt = time.Now()

	if err := s.store.UpdateSession(ctx, session); err != nil {
		s.logger.Error("CRITICAL: gateway session funds released but status update failed",
			"session", sessionID, "error", err)
		return nil, fmt.Errorf("failed to update session after close: %w", err)
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
