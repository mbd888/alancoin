package contracts

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"
)

// Propose creates a new contract proposal. No funds are locked yet.
func (s *Service) Propose(ctx context.Context, req ProposeRequest) (*Contract, error) {
	if strings.EqualFold(req.BuyerAddr, req.SellerAddr) {
		return nil, errors.New("buyer and seller cannot be the same address")
	}

	if _, err := parseDuration(req.Duration); err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}

	// Defaults
	if req.MinVolume <= 0 {
		req.MinVolume = 1
	}
	if req.SellerPenalty == "" {
		req.SellerPenalty = "0"
	}
	if req.MaxLatencyMs <= 0 {
		req.MaxLatencyMs = 10000
	}
	if req.MinSuccessRate <= 0 {
		req.MinSuccessRate = 95.00
	}
	if req.SLAWindowSize <= 0 {
		req.SLAWindowSize = 20
	}

	now := time.Now()
	contract := &Contract{
		ID:             generateContractID(),
		BuyerAddr:      strings.ToLower(req.BuyerAddr),
		SellerAddr:     strings.ToLower(req.SellerAddr),
		ServiceType:    req.ServiceType,
		PricePerCall:   req.PricePerCall,
		MinVolume:      req.MinVolume,
		BuyerBudget:    req.BuyerBudget,
		SellerPenalty:  req.SellerPenalty,
		MaxLatencyMs:   req.MaxLatencyMs,
		MinSuccessRate: req.MinSuccessRate,
		SLAWindowSize:  req.SLAWindowSize,
		Status:         StatusProposed,
		Duration:       req.Duration,
		BudgetSpent:    "0",
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.store.Create(ctx, contract); err != nil {
		return nil, fmt.Errorf("failed to create contract: %w", err)
	}

	return contract, nil
}

// Accept accepts a proposed contract. Locks buyer budget and seller penalty.
func (s *Service) Accept(ctx context.Context, id, callerAddr string) (*Contract, error) {
	mu := s.contractLock(id)
	mu.Lock()
	defer mu.Unlock()

	contract, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if strings.ToLower(callerAddr) != contract.SellerAddr {
		return nil, ErrUnauthorized
	}

	if contract.IsTerminal() {
		return nil, ErrAlreadyResolved
	}

	if contract.Status != StatusProposed {
		return nil, ErrInvalidStatus
	}

	// Lock buyer budget
	if err := s.ledger.EscrowLock(ctx, contract.BuyerAddr, contract.BuyerBudget, contract.ID); err != nil {
		return nil, fmt.Errorf("failed to lock buyer budget: %w", err)
	}

	// Lock seller penalty (if any)
	penaltyRef := contract.ID + "_pen"
	sellerPenalty := parseBigFloat(contract.SellerPenalty)
	if sellerPenalty.Sign() > 0 {
		if err := s.ledger.EscrowLock(ctx, contract.SellerAddr, contract.SellerPenalty, penaltyRef); err != nil {
			// Compensate: refund buyer budget
			if refErr := s.ledger.RefundEscrow(ctx, contract.BuyerAddr, contract.BuyerBudget, contract.ID); refErr != nil {
				log.Printf("CRITICAL: contract %s failed to refund buyer budget on seller penalty lock failure: %v", contract.ID, refErr)
			}
			return nil, fmt.Errorf("failed to lock seller penalty: %w", err)
		}
	}

	dur, _ := parseDuration(contract.Duration) // already validated in Propose
	now := time.Now()
	expiresAt := now.Add(dur)
	contract.StartsAt = &now
	contract.ExpiresAt = &expiresAt
	contract.Status = StatusActive
	contract.UpdatedAt = now

	if err := s.store.Update(ctx, contract); err != nil {
		// Compensate: refund both locks
		if refErr := s.ledger.RefundEscrow(ctx, contract.BuyerAddr, contract.BuyerBudget, contract.ID); refErr != nil {
			log.Printf("CRITICAL: contract %s failed to refund buyer budget on store update failure: %v", contract.ID, refErr)
		}
		if sellerPenalty.Sign() > 0 {
			if refErr := s.ledger.RefundEscrow(ctx, contract.SellerAddr, contract.SellerPenalty, penaltyRef); refErr != nil {
				log.Printf("CRITICAL: contract %s failed to refund seller penalty on store update failure: %v", contract.ID, refErr)
			}
		}
		return nil, fmt.Errorf("failed to update contract: %w", err)
	}

	return contract, nil
}

// Reject rejects a proposed contract. No funds to move.
func (s *Service) Reject(ctx context.Context, id, callerAddr string) (*Contract, error) {
	mu := s.contractLock(id)
	mu.Lock()
	defer mu.Unlock()

	contract, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if strings.ToLower(callerAddr) != contract.SellerAddr {
		return nil, ErrUnauthorized
	}

	if contract.IsTerminal() {
		return nil, ErrAlreadyResolved
	}

	if contract.Status != StatusProposed {
		return nil, ErrInvalidStatus
	}

	now := time.Now()
	contract.Status = StatusRejected
	contract.ResolvedAt = &now
	contract.UpdatedAt = now

	if err := s.store.Update(ctx, contract); err != nil {
		return nil, fmt.Errorf("failed to update contract: %w", err)
	}

	return contract, nil
}

// RecordCall records a service call result within an active contract.
func (s *Service) RecordCall(ctx context.Context, id string, req RecordCallRequest, callerAddr string) (*Contract, error) {
	mu := s.contractLock(id)
	mu.Lock()
	defer mu.Unlock()

	contract, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	caller := strings.ToLower(callerAddr)
	if caller != contract.BuyerAddr && caller != contract.SellerAddr {
		return nil, ErrUnauthorized
	}

	if contract.IsTerminal() {
		return nil, ErrAlreadyResolved
	}

	if contract.Status != StatusActive {
		return nil, ErrInvalidStatus
	}

	// Check budget remaining
	budgetRemaining := subtractBigFloat(contract.BuyerBudget, contract.BudgetSpent)
	pricePerCall := parseBigFloat(contract.PricePerCall)
	if budgetRemaining.Cmp(pricePerCall) < 0 {
		return nil, ErrBudgetExhausted
	}

	// Record the call
	call := &ContractCall{
		ID:         generateCallID(),
		ContractID: contract.ID,
		Status:     req.Status,
		LatencyMs:  req.LatencyMs,
		ErrorMsg:   req.ErrorMessage,
		Amount:     contract.PricePerCall,
		CreatedAt:  time.Now(),
	}

	if err := s.store.RecordCall(ctx, call); err != nil {
		return nil, fmt.Errorf("failed to record call: %w", err)
	}

	// Update tracking
	contract.TotalCalls++
	contract.TotalLatencyMs += int64(req.LatencyMs)
	if req.Status == "success" {
		contract.SuccessfulCalls++
	} else {
		contract.FailedCalls++
	}

	// On success, micro-release payment
	if req.Status == "success" {
		if err := s.ledger.ReleaseEscrow(ctx, contract.BuyerAddr, contract.SellerAddr, contract.PricePerCall, call.ID); err != nil {
			return nil, fmt.Errorf("failed to release payment: %w", err)
		}
		contract.BudgetSpent = addBigFloat(contract.BudgetSpent, contract.PricePerCall)
	}

	contract.UpdatedAt = time.Now()

	// Check SLA via rolling window
	recentCalls, err := s.store.GetRecentCalls(ctx, contract.ID, contract.SLAWindowSize)
	if err == nil && len(recentCalls) >= contract.SLAWindowSize {
		successCount := 0
		for _, c := range recentCalls {
			if c.Status == "success" {
				successCount++
			}
		}
		windowRate := float64(successCount) / float64(len(recentCalls)) * 100.0
		if windowRate < contract.MinSuccessRate {
			details := fmt.Sprintf("SLA window success rate %.1f%% < threshold %.1f%% (window: %d calls)",
				windowRate, contract.MinSuccessRate, len(recentCalls))
			if err := s.store.Update(ctx, contract); err != nil {
				return nil, fmt.Errorf("failed to update contract: %w", err)
			}
			return s.violate(ctx, contract, details)
		}
	}

	// Check if budget exhausted
	newRemaining := subtractBigFloat(contract.BuyerBudget, contract.BudgetSpent)
	if newRemaining.Cmp(pricePerCall) < 0 && contract.TotalCalls >= contract.MinVolume {
		if err := s.store.Update(ctx, contract); err != nil {
			return nil, fmt.Errorf("failed to update contract: %w", err)
		}
		return s.complete(ctx, contract)
	}

	if err := s.store.Update(ctx, contract); err != nil {
		return nil, fmt.Errorf("failed to update contract: %w", err)
	}

	return contract, nil
}

// Terminate terminates a contract early by either party.
func (s *Service) Terminate(ctx context.Context, id, callerAddr, reason string) (*Contract, error) {
	mu := s.contractLock(id)
	mu.Lock()
	defer mu.Unlock()

	contract, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	caller := strings.ToLower(callerAddr)
	if caller != contract.BuyerAddr && caller != contract.SellerAddr {
		return nil, ErrUnauthorized
	}

	if contract.IsTerminal() {
		return nil, ErrAlreadyResolved
	}

	if contract.Status != StatusActive {
		return nil, ErrInvalidStatus
	}

	now := time.Now()
	contract.Status = StatusTerminated
	contract.TerminatedBy = caller
	contract.TerminatedReason = reason
	contract.ResolvedAt = &now
	contract.UpdatedAt = now

	sellerPenalty := parseBigFloat(contract.SellerPenalty)
	penaltyRef := contract.ID + "_pen"
	budgetRemaining := subtractBigFloat(contract.BuyerBudget, contract.BudgetSpent)

	if caller == contract.BuyerAddr {
		// Buyer terminates: remaining budget goes to seller as compensation, seller penalty returned
		if budgetRemaining.Sign() > 0 {
			if err := s.ledger.ReleaseEscrow(ctx, contract.BuyerAddr, contract.SellerAddr, bigFloatString(budgetRemaining), contract.ID+"_term"); err != nil {
				log.Printf("CRITICAL: contract %s terminate failed to release buyer budget to seller: %v", contract.ID, err)
			}
		}
		if sellerPenalty.Sign() > 0 {
			if err := s.ledger.RefundEscrow(ctx, contract.SellerAddr, contract.SellerPenalty, penaltyRef); err != nil {
				log.Printf("CRITICAL: contract %s terminate failed to refund seller penalty: %v", contract.ID, err)
			}
		}
	} else {
		// Seller terminates: seller penalty forfeited to buyer, remaining buyer budget refunded
		if sellerPenalty.Sign() > 0 {
			if err := s.ledger.ReleaseEscrow(ctx, contract.SellerAddr, contract.BuyerAddr, contract.SellerPenalty, penaltyRef); err != nil {
				log.Printf("CRITICAL: contract %s terminate failed to release seller penalty to buyer: %v", contract.ID, err)
			}
		}
		if budgetRemaining.Sign() > 0 {
			if err := s.ledger.RefundEscrow(ctx, contract.BuyerAddr, bigFloatString(budgetRemaining), contract.ID+"_refund"); err != nil {
				log.Printf("CRITICAL: contract %s terminate failed to refund buyer budget: %v", contract.ID, err)
			}
		}
	}

	if err := s.store.Update(ctx, contract); err != nil {
		return nil, fmt.Errorf("failed to update contract: %w", err)
	}

	return contract, nil
}

// complete handles natural contract completion.
func (s *Service) complete(ctx context.Context, contract *Contract) (*Contract, error) {
	now := time.Now()
	contract.Status = StatusCompleted
	contract.ResolvedAt = &now
	contract.UpdatedAt = now

	// Refund remaining buyer budget
	budgetRemaining := subtractBigFloat(contract.BuyerBudget, contract.BudgetSpent)
	if budgetRemaining.Sign() > 0 {
		if err := s.ledger.RefundEscrow(ctx, contract.BuyerAddr, bigFloatString(budgetRemaining), contract.ID+"_refund"); err != nil {
			log.Printf("CRITICAL: contract %s complete failed to refund buyer budget: %v", contract.ID, err)
		}
	}

	// Return seller penalty
	sellerPenalty := parseBigFloat(contract.SellerPenalty)
	if sellerPenalty.Sign() > 0 {
		if err := s.ledger.RefundEscrow(ctx, contract.SellerAddr, contract.SellerPenalty, contract.ID+"_pen"); err != nil {
			log.Printf("CRITICAL: contract %s complete failed to refund seller penalty: %v", contract.ID, err)
		}
	}

	if err := s.store.Update(ctx, contract); err != nil {
		return nil, fmt.Errorf("failed to update contract: %w", err)
	}

	return contract, nil
}

// violate handles SLA breach.
func (s *Service) violate(ctx context.Context, contract *Contract, details string) (*Contract, error) {
	now := time.Now()
	contract.Status = StatusViolated
	contract.ViolationDetails = details
	contract.ResolvedAt = &now
	contract.UpdatedAt = now

	// Transfer seller penalty to buyer
	sellerPenalty := parseBigFloat(contract.SellerPenalty)
	penaltyRef := contract.ID + "_pen"
	if sellerPenalty.Sign() > 0 {
		if err := s.ledger.ReleaseEscrow(ctx, contract.SellerAddr, contract.BuyerAddr, contract.SellerPenalty, penaltyRef); err != nil {
			log.Printf("CRITICAL: contract %s violate failed to release seller penalty to buyer: %v", contract.ID, err)
		}
	}

	// Refund remaining buyer budget
	budgetRemaining := subtractBigFloat(contract.BuyerBudget, contract.BudgetSpent)
	if budgetRemaining.Sign() > 0 {
		if err := s.ledger.RefundEscrow(ctx, contract.BuyerAddr, bigFloatString(budgetRemaining), contract.ID+"_refund"); err != nil {
			log.Printf("CRITICAL: contract %s violate failed to refund buyer budget: %v", contract.ID, err)
		}
	}

	if err := s.store.Update(ctx, contract); err != nil {
		return nil, fmt.Errorf("failed to update contract: %w", err)
	}

	return contract, nil
}

// CheckExpired checks for expired active contracts and completes or terminates them.
func (s *Service) CheckExpired(ctx context.Context) {
	expired, err := s.store.ListExpiring(ctx, time.Now(), 100)
	if err != nil {
		return
	}

	for _, contract := range expired {
		mu := s.contractLock(contract.ID)
		mu.Lock()

		// Re-read under lock
		fresh, err := s.store.Get(ctx, contract.ID)
		if err != nil {
			mu.Unlock()
			continue
		}
		if fresh.IsTerminal() {
			mu.Unlock()
			continue
		}

		if fresh.TotalCalls >= fresh.MinVolume {
			if _, err := s.complete(ctx, fresh); err != nil {
				log.Printf("ERROR: contract %s expired completion failed: %v", fresh.ID, err)
			}
		} else {
			now := time.Now()
			fresh.Status = StatusTerminated
			fresh.TerminatedReason = "expired"
			fresh.ResolvedAt = &now
			fresh.UpdatedAt = now

			// Refund remaining buyer budget
			budgetRemaining := subtractBigFloat(fresh.BuyerBudget, fresh.BudgetSpent)
			if budgetRemaining.Sign() > 0 {
				if err := s.ledger.RefundEscrow(ctx, fresh.BuyerAddr, bigFloatString(budgetRemaining), fresh.ID+"_refund"); err != nil {
					log.Printf("CRITICAL: contract %s expired failed to refund buyer budget: %v", fresh.ID, err)
				}
			}

			// Return seller penalty
			sellerPenalty := parseBigFloat(fresh.SellerPenalty)
			if sellerPenalty.Sign() > 0 {
				if err := s.ledger.RefundEscrow(ctx, fresh.SellerAddr, fresh.SellerPenalty, fresh.ID+"_pen"); err != nil {
					log.Printf("CRITICAL: contract %s expired failed to refund seller penalty: %v", fresh.ID, err)
				}
			}

			if err := s.store.Update(ctx, fresh); err != nil {
				log.Printf("ERROR: contract %s expired status update failed: %v", fresh.ID, err)
			}
		}

		mu.Unlock()
	}
}

// Get returns a contract by ID.
func (s *Service) Get(ctx context.Context, id string) (*Contract, error) {
	return s.store.Get(ctx, id)
}

// ListByAgent returns contracts involving an agent.
func (s *Service) ListByAgent(ctx context.Context, agentAddr, status string, limit int) ([]*Contract, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListByAgent(ctx, strings.ToLower(agentAddr), status, limit)
}

// ListActive returns all active contracts.
func (s *Service) ListActive(ctx context.Context, limit int) ([]*Contract, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListActive(ctx, limit)
}

// ListCalls returns calls for a contract.
func (s *Service) ListCalls(ctx context.Context, contractID string, limit int) ([]*ContractCall, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListCalls(ctx, contractID, limit)
}

// --- helpers ---

func parseBigFloat(s string) *big.Float {
	f, _, _ := new(big.Float).Parse(s, 10)
	if f == nil {
		return new(big.Float)
	}
	return f
}

func subtractBigFloat(a, b string) *big.Float {
	fa := parseBigFloat(a)
	fb := parseBigFloat(b)
	return new(big.Float).Sub(fa, fb)
}

func addBigFloat(a, b string) string {
	fa := parseBigFloat(a)
	fb := parseBigFloat(b)
	result := new(big.Float).Add(fa, fb)
	return bigFloatString(result)
}

func bigFloatString(f *big.Float) string {
	return f.Text('f', 6)
}
