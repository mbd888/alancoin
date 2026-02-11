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
	store             Store
	resolver          *Resolver
	forwarder         *Forwarder
	ledger            LedgerService
	recorder          TransactionRecorder
	revenue           RevenueAccumulator
	verification      VerificationChecker
	contracts         ContractManager
	guaranteeFundAddr string // platform address receiving guarantee premiums
	logger            *slog.Logger
	locks             sync.Map // per-session mutex
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

// WithRecorder adds a transaction recorder for reputation integration.
func (s *Service) WithRecorder(r TransactionRecorder) *Service {
	s.recorder = r
	return s
}

// WithRevenueAccumulator adds a revenue accumulator for stakes interception.
func (s *Service) WithRevenueAccumulator(r RevenueAccumulator) *Service {
	s.revenue = r
	return s
}

// WithVerification adds a verification checker for guarantee premium handling.
func (s *Service) WithVerification(v VerificationChecker) *Service {
	s.verification = v
	return s
}

// WithContracts adds a contract manager for auto-contract creation.
func (s *Service) WithContracts(c ContractManager) *Service {
	s.contracts = c
	return s
}

// WithGuaranteeFundAddr sets the platform address that receives guarantee premiums.
func (s *Service) WithGuaranteeFundAddr(addr string) *Service {
	s.guaranteeFundAddr = strings.ToLower(addr)
	return s
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

		// Check if candidate is verified → compute premium
		var premiumBig *big.Int
		var totalCostBig *big.Int
		var isVerified bool
		var guaranteedRate float64
		var slaWindow int

		if s.verification != nil {
			if v, vErr := s.verification.IsVerified(ctx, candidate.AgentAddress); vErr == nil && v {
				isVerified = true
				gr, pr, _ := s.verification.GetGuarantee(ctx, candidate.AgentAddress)
				guaranteedRate = gr
				slaWindow = 20 // default SLA window
				if pr > 0 {
					// Premium = price * premiumRate (e.g. 0.05 = 5%)
					premiumF := float64(priceBig.Int64()) * pr
					premiumBig = big.NewInt(int64(premiumF))
					if premiumBig.Sign() <= 0 {
						premiumBig = big.NewInt(1) // minimum 1 micro-unit
					}
				}
			}
		}

		if premiumBig == nil {
			premiumBig = new(big.Int)
		}
		totalCostBig = new(big.Int).Add(priceBig, premiumBig)

		// Check per-request limit (against total cost including premium)
		if totalCostBig.Cmp(maxPerBig) > 0 {
			continue
		}

		// Check remaining budget
		if totalCostBig.Cmp(remaining) > 0 {
			lastErr = ErrBudgetExceeded
			continue
		}

		// Payment: deposit to seller. The buyer's budget is already held (pending).
		// Settlement (ConfirmHold for spent + ReleaseHold for unused) happens on session close,
		// using the original hold reference (session.ID) to avoid cross-hold interference.
		ref := fmt.Sprintf("%s:req:%d:%s", session.ID, session.RequestCount+1, candidate.ServiceID)
		totalCostStr := usdc.Format(totalCostBig)

		// Deposit base price to seller
		if err := s.ledger.Deposit(ctx, candidate.AgentAddress, candidate.Price, ref); err != nil {
			s.logger.Error("deposit failed after confirm hold", "session", sessionID, "seller", candidate.AgentAddress, "error", err)
			lastErr = err
			retries++
			continue
		}

		// Deposit premium to guarantee fund (platform address)
		if premiumBig.Sign() > 0 {
			premiumStr := usdc.Format(premiumBig)
			if err := s.ledger.Deposit(ctx, s.guaranteeFundAddr, premiumStr, "gpremium:"+ref); err != nil {
				// Non-fatal: base payment succeeded, premium deposit is best-effort
				s.logger.Warn("guarantee premium deposit failed", "session", sessionID, "premium", premiumStr, "error", err)
			}
		}

		// Forward HTTP request
		start := time.Now()
		fwdResp, err := s.forwarder.Forward(ctx, ForwardRequest{
			Endpoint:  candidate.Endpoint,
			Params:    req.Params,
			FromAddr:  session.AgentAddr,
			Amount:    candidate.Price,
			Reference: ref,
		})
		latencyMs := time.Since(start).Milliseconds()

		// Determine call status for contract recording
		callStatus := "success"
		if err != nil {
			callStatus = "failed"
		}

		// Auto-record into micro-contract if verified
		if isVerified && s.contracts != nil {
			contractID, cErr := s.contracts.EnsureContract(ctx,
				session.AgentAddr, candidate.AgentAddress, req.ServiceType,
				candidate.Price, guaranteedRate, slaWindow)
			if cErr == nil && contractID != "" {
				_ = s.contracts.RecordCall(ctx, contractID, callStatus, int(latencyMs))
			}
		}

		if err != nil {
			// Payment already happened. Service failed — reputation issue.
			s.logger.Warn("forward failed after payment", "session", sessionID, "seller", candidate.AgentAddress, "error", err)
			s.logRequest(ctx, session.ID, req.ServiceType, candidate.AgentAddress, totalCostStr, "forward_failed", 0, err.Error())

			// Record failed transaction so seller's success rate drops
			if s.recorder != nil {
				_ = s.recorder.RecordTransaction(ctx, ref, session.AgentAddr,
					candidate.AgentAddress, candidate.Price, candidate.ServiceID, "failed")
			}

			// Update session spend even though forward failed (payment was made)
			newSpent := new(big.Int).Add(spentBig, totalCostBig)
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
		newSpent := new(big.Int).Add(spentBig, totalCostBig)
		session.TotalSpent = usdc.Format(newSpent)
		session.RequestCount++
		session.UpdatedAt = time.Now()

		if err := s.store.UpdateSession(ctx, session); err != nil {
			s.logger.Error("failed to update session after successful proxy", "session", sessionID, "error", err)
		}

		s.logRequest(ctx, session.ID, req.ServiceType, candidate.AgentAddress, totalCostStr, "success", fwdResp.LatencyMs, "")

		// Record successful transaction for reputation
		if s.recorder != nil {
			_ = s.recorder.RecordTransaction(ctx, ref, session.AgentAddr,
				candidate.AgentAddress, candidate.Price, candidate.ServiceID, "confirmed")
		}

		// Accumulate revenue for stakes
		if s.revenue != nil {
			_ = s.revenue.AccumulateRevenue(ctx, candidate.AgentAddress, candidate.Price, "gateway_proxy:"+ref)
		}

		return &ProxyResult{
			Response:    fwdResp.Body,
			ServiceUsed: candidate.AgentAddress,
			ServiceName: candidate.ServiceName,
			AmountPaid:  totalCostStr,
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

	// Settle the hold: confirm the spent portion, release the unused portion.
	// Both use the original hold reference (session.ID) to ensure we only affect
	// this session's hold and don't consume pending from other holds/streams.
	spentBig, _ := usdc.Parse(session.TotalSpent)
	holdBig, _ := usdc.Parse(session.MaxTotal)
	unused := new(big.Int).Sub(holdBig, spentBig)

	if spentBig.Sign() > 0 {
		spentStr := usdc.Format(spentBig)
		if err := s.ledger.ConfirmHold(ctx, session.AgentAddr, spentStr, session.ID); err != nil {
			return nil, fmt.Errorf("failed to confirm spent hold: %w", err)
		}
	}

	if unused.Sign() > 0 {
		unusedStr := usdc.Format(unused)
		if err := s.ledger.ReleaseHold(ctx, session.AgentAddr, unusedStr, session.ID); err != nil {
			return nil, fmt.Errorf("failed to release unused hold: %w", err)
		}
	}

	session.Status = StatusClosed
	session.UpdatedAt = time.Now()

	if err := s.store.UpdateSession(ctx, session); err != nil {
		s.logger.Error("CRITICAL: gateway session funds settled but status update failed",
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

// AutoCloseExpired closes an expired session without caller authorization.
// Called by the Timer goroutine. Sets status to StatusExpired and releases unspent funds.
func (s *Service) AutoCloseExpired(ctx context.Context, session *Session) error {
	mu := s.sessionLock(session.ID)
	mu.Lock()
	defer mu.Unlock()

	// Re-read under lock
	fresh, err := s.store.GetSession(ctx, session.ID)
	if err != nil {
		return err
	}
	if fresh.Status != StatusActive {
		return ErrSessionClosed
	}

	// Settle the hold: confirm spent, release unused (same as CloseSession)
	spentBig, _ := usdc.Parse(fresh.TotalSpent)
	holdBig, _ := usdc.Parse(fresh.MaxTotal)
	unused := new(big.Int).Sub(holdBig, spentBig)

	if spentBig.Sign() > 0 {
		spentStr := usdc.Format(spentBig)
		if err := s.ledger.ConfirmHold(ctx, fresh.AgentAddr, spentStr, fresh.ID); err != nil {
			return fmt.Errorf("failed to confirm spent hold: %w", err)
		}
	}

	if unused.Sign() > 0 {
		unusedStr := usdc.Format(unused)
		if err := s.ledger.ReleaseHold(ctx, fresh.AgentAddr, unusedStr, fresh.ID); err != nil {
			return fmt.Errorf("failed to release unused hold: %w", err)
		}
	}

	fresh.Status = StatusExpired
	fresh.UpdatedAt = time.Now()

	if err := s.store.UpdateSession(ctx, fresh); err != nil {
		return fmt.Errorf("failed to update expired session: %w", err)
	}

	return nil
}
