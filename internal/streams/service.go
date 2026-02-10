package streams

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// Service implements streaming micropayment business logic.
type Service struct {
	store    Store
	ledger   LedgerService
	recorder TransactionRecorder
	revenue  RevenueAccumulator
	locks    sync.Map // per-stream ID locks to prevent race conditions
}

// streamLock returns a mutex for the given stream ID.
func (s *Service) streamLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// NewService creates a new streaming micropayment service.
func NewService(store Store, ledger LedgerService) *Service {
	return &Service{
		store:  store,
		ledger: ledger,
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

// Open creates a new payment stream and holds funds from the buyer.
func (s *Service) Open(ctx context.Context, req OpenRequest) (*Stream, error) {
	if strings.EqualFold(req.BuyerAddr, req.SellerAddr) {
		return nil, errors.New("buyer and seller cannot be the same address")
	}

	holdBig, ok := usdc.Parse(req.HoldAmount)
	if !ok || holdBig.Sign() <= 0 {
		return nil, ErrInvalidAmount
	}

	priceBig, ok := usdc.Parse(req.PricePerTick)
	if !ok || priceBig.Sign() <= 0 {
		return nil, fmt.Errorf("%w: pricePerTick must be positive", ErrInvalidAmount)
	}

	staleTimeout := req.StaleTimeoutSec
	if staleTimeout <= 0 {
		staleTimeout = int(DefaultStaleTimeout.Seconds())
	}

	now := time.Now()
	stream := &Stream{
		ID:              generateStreamID(),
		BuyerAddr:       strings.ToLower(req.BuyerAddr),
		SellerAddr:      strings.ToLower(req.SellerAddr),
		ServiceID:       req.ServiceID,
		SessionKeyID:    req.SessionKeyID,
		HoldAmount:      req.HoldAmount,
		SpentAmount:     "0.000000",
		PricePerTick:    req.PricePerTick,
		TickCount:       0,
		Status:          StatusOpen,
		StaleTimeoutSec: staleTimeout,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// Hold buyer funds
	if err := s.ledger.Hold(ctx, stream.BuyerAddr, stream.HoldAmount, stream.ID); err != nil {
		return nil, fmt.Errorf("failed to hold stream funds: %w", err)
	}

	if err := s.store.Create(ctx, stream); err != nil {
		// Best-effort release if store fails
		_ = s.ledger.ReleaseHold(ctx, stream.BuyerAddr, stream.HoldAmount, stream.ID)
		return nil, fmt.Errorf("failed to create stream record: %w", err)
	}

	return stream, nil
}

// RecordTick records a micropayment tick on an open stream.
func (s *Service) RecordTick(ctx context.Context, streamID string, req TickRequest) (*Tick, *Stream, error) {
	mu := s.streamLock(streamID)
	mu.Lock()
	defer mu.Unlock()

	stream, err := s.store.Get(ctx, streamID)
	if err != nil {
		return nil, nil, err
	}

	if stream.IsTerminal() {
		return nil, nil, ErrAlreadyClosed
	}

	if stream.Status != StatusOpen {
		return nil, nil, ErrInvalidStatus
	}

	// Determine tick amount: use request amount or pricePerTick
	tickAmount := req.Amount
	if tickAmount == "" {
		tickAmount = stream.PricePerTick
	}

	tickBig, ok := usdc.Parse(tickAmount)
	if !ok || tickBig.Sign() <= 0 {
		return nil, nil, ErrInvalidAmount
	}

	// Check that tick won't exceed hold
	spentBig, _ := usdc.Parse(stream.SpentAmount)
	holdBig, _ := usdc.Parse(stream.HoldAmount)
	newSpent := new(big.Int).Add(spentBig, tickBig)

	if newSpent.Cmp(holdBig) > 0 {
		return nil, nil, ErrHoldExhausted
	}

	// Determine next sequence number
	nextSeq := stream.TickCount + 1

	now := time.Now()
	tick := &Tick{
		ID:         generateTickID(),
		StreamID:   streamID,
		Seq:        nextSeq,
		Amount:     tickAmount,
		Cumulative: usdc.Format(newSpent),
		Metadata:   req.Metadata,
		CreatedAt:  now,
	}

	if err := s.store.CreateTick(ctx, tick); err != nil {
		return nil, nil, fmt.Errorf("failed to record tick: %w", err)
	}

	// Update stream state
	stream.SpentAmount = tick.Cumulative
	stream.TickCount = nextSeq
	stream.LastTickAt = &now
	stream.UpdatedAt = now

	if err := s.store.Update(ctx, stream); err != nil {
		return nil, nil, fmt.Errorf("failed to update stream after tick: %w", err)
	}

	return tick, stream, nil
}

// Close settles a stream: pays seller for spent amount, refunds unused hold to buyer.
func (s *Service) Close(ctx context.Context, streamID, callerAddr, reason string) (*Stream, error) {
	mu := s.streamLock(streamID)
	mu.Lock()
	defer mu.Unlock()

	stream, err := s.store.Get(ctx, streamID)
	if err != nil {
		return nil, err
	}

	caller := strings.ToLower(callerAddr)
	if caller != stream.BuyerAddr && caller != stream.SellerAddr {
		return nil, ErrUnauthorized
	}

	if stream.IsTerminal() {
		return nil, ErrAlreadyClosed
	}

	return s.settle(ctx, stream, StatusClosed, reason)
}

// AutoClose settles a stale stream (no tick within timeout).
func (s *Service) AutoClose(ctx context.Context, stream *Stream) error {
	mu := s.streamLock(stream.ID)
	mu.Lock()
	defer mu.Unlock()

	// Re-read under lock to prevent stale-state races
	fresh, err := s.store.Get(ctx, stream.ID)
	if err != nil {
		return err
	}
	stream = fresh

	if stream.IsTerminal() {
		return ErrAlreadyClosed
	}

	_, err = s.settle(ctx, stream, StatusStaleClosed, "stale_timeout")
	return err
}

// settle performs the actual settlement: pay seller, refund unused to buyer.
func (s *Service) settle(ctx context.Context, stream *Stream, status Status, reason string) (*Stream, error) {
	spentBig, _ := usdc.Parse(stream.SpentAmount)
	holdBig, _ := usdc.Parse(stream.HoldAmount)

	// 1. Confirm hold for the spent portion (pending → total_out for buyer)
	if spentBig.Sign() > 0 {
		if err := s.ledger.ConfirmHold(ctx, stream.BuyerAddr, stream.SpentAmount, stream.ID); err != nil {
			return nil, fmt.Errorf("failed to confirm spent hold: %w", err)
		}

		// Credit seller for the spent amount
		if err := s.ledger.Deposit(ctx, stream.SellerAddr, stream.SpentAmount, stream.ID); err != nil {
			return nil, fmt.Errorf("failed to credit seller: %w", err)
		}
	}

	// 2. Release unused hold back to buyer (pending → available)
	unused := new(big.Int).Sub(holdBig, spentBig)
	if unused.Sign() > 0 {
		unusedStr := usdc.Format(unused)
		if err := s.ledger.ReleaseHold(ctx, stream.BuyerAddr, unusedStr, stream.ID); err != nil {
			return nil, fmt.Errorf("failed to release unused hold: %w", err)
		}
	}

	now := time.Now()
	stream.Status = status
	stream.CloseReason = reason
	stream.ClosedAt = &now
	stream.UpdatedAt = now

	if err := s.store.Update(ctx, stream); err != nil {
		// CRITICAL: Funds already moved. Log for manual resolution.
		log.Printf("CRITICAL: stream %s funds settled but status update failed: %v",
			stream.ID, err)
		return nil, fmt.Errorf("failed to update stream after settlement (requires manual resolution): %w", err)
	}

	// Record transaction for reputation
	if s.recorder != nil && spentBig.Sign() > 0 {
		txStatus := "confirmed"
		if status == StatusDisputed {
			txStatus = "failed"
		}
		_ = s.recorder.RecordTransaction(ctx, stream.ID, stream.BuyerAddr, stream.SellerAddr, stream.SpentAmount, stream.ServiceID, txStatus)
	}

	// Intercept revenue for stakes (seller earned money)
	if s.revenue != nil && spentBig.Sign() > 0 && status != StatusDisputed {
		_ = s.revenue.AccumulateRevenue(ctx, stream.SellerAddr, stream.SpentAmount)
	}

	return stream, nil
}

// Get returns a stream by ID.
func (s *Service) Get(ctx context.Context, id string) (*Stream, error) {
	return s.store.Get(ctx, id)
}

// ListByAgent returns streams involving an agent (as buyer or seller).
func (s *Service) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Stream, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListByAgent(ctx, strings.ToLower(agentAddr), limit)
}

// ListTicks returns ticks for a stream.
func (s *Service) ListTicks(ctx context.Context, streamID string, limit int) ([]*Tick, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.ListTicks(ctx, streamID, limit)
}
