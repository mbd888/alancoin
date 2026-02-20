package streams

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/logging"
	"github.com/mbd888/alancoin/internal/retry"
	"github.com/mbd888/alancoin/internal/syncutil"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// WebhookEmitter emits lifecycle events to webhook subscribers.
type WebhookEmitter interface {
	EmitStreamOpened(sellerAddr, streamID, buyerAddr, holdAmount string)
	EmitStreamClosed(buyerAddr, streamID, sellerAddr, spentAmount, status string)
}

// Service implements streaming micropayment business logic.
type Service struct {
	store          Store
	ledger         LedgerService
	recorder       TransactionRecorder
	revenue        RevenueAccumulator
	receiptIssuer  ReceiptIssuer
	webhookEmitter WebhookEmitter
	locks          syncutil.ShardedMutex
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

// WithReceiptIssuer adds a receipt issuer for cryptographic payment proofs.
func (s *Service) WithReceiptIssuer(r ReceiptIssuer) *Service {
	s.receiptIssuer = r
	return s
}

// WithWebhookEmitter adds a webhook emitter for lifecycle event notifications.
func (s *Service) WithWebhookEmitter(e WebhookEmitter) *Service {
	s.webhookEmitter = e
	return s
}

// Open creates a new payment stream and holds funds from the buyer.
func (s *Service) Open(ctx context.Context, req OpenRequest) (_ *Stream, retErr error) {
	ctx, span := traces.StartSpan(ctx, "streams.Open",
		attribute.String("buyer", req.BuyerAddr),
		attribute.String("seller", req.SellerAddr),
		attribute.String("hold_amount", req.HoldAmount),
	)
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

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

	streamsOpened.Inc()

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitStreamOpened(stream.SellerAddr, stream.ID, stream.BuyerAddr, stream.HoldAmount)
	}

	return stream, nil
}

// RecordTick records a micropayment tick on an open stream.
func (s *Service) RecordTick(ctx context.Context, streamID string, req TickRequest) (_ *Tick, _ *Stream, retErr error) {
	ctx, span := traces.StartSpan(ctx, "streams.RecordTick",
		attribute.String("stream_id", streamID),
	)
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	unlock := s.locks.Lock(streamID)
	defer unlock()

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

	// Determine next sequence number.
	// If caller supplies a seq, validate for idempotency: reject duplicates and out-of-order.
	nextSeq := stream.TickCount + 1
	if req.Seq > 0 {
		if req.Seq <= stream.TickCount {
			return nil, nil, ErrDuplicateTickSeq
		}
		if req.Seq != nextSeq {
			return nil, nil, fmt.Errorf("%w: expected %d, got %d", ErrInvalidTickSeq, nextSeq, req.Seq)
		}
	}

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

	streamTicksTotal.Inc()
	return tick, stream, nil
}

// Close settles a stream: pays seller for spent amount, refunds unused hold to buyer.
func (s *Service) Close(ctx context.Context, streamID, callerAddr, reason string) (_ *Stream, retErr error) {
	ctx, span := traces.StartSpan(ctx, "streams.Close",
		attribute.String("stream_id", streamID),
		attribute.String("caller", callerAddr),
	)
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	unlock := s.locks.Lock(streamID)
	defer unlock()

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

	return s.settle(ctx, stream, StatusClosed, reason, unlock)
}

// AutoClose settles a stale stream (no tick within timeout).
func (s *Service) AutoClose(ctx context.Context, stream *Stream) error {
	unlock := s.locks.Lock(stream.ID)
	defer unlock()

	// Re-read under lock to prevent stale-state races
	fresh, err := s.store.Get(ctx, stream.ID)
	if err != nil {
		return err
	}
	stream = fresh

	if stream.IsTerminal() {
		return ErrAlreadyClosed
	}

	_, err = s.settle(ctx, stream, StatusStaleClosed, "stale_timeout", unlock)
	return err
}

// settle performs the actual settlement: pay seller, refund unused to buyer.
//
// Order matters for crash safety: we release the unused portion first, then
// settle the spent portion. If we crash between the two operations:
//   - Release succeeded, settle failed → buyer got unused funds back, seller
//     is owed money (recoverable via reconciliation — no funds are stuck).
//   - The reverse order (old code) would leave unused funds permanently stuck
//     in pending if the process crashes after settle but before release.
func (s *Service) settle(ctx context.Context, stream *Stream, status Status, reason string, unlockFn func()) (_ *Stream, retErr error) {
	ctx, span := traces.StartSpan(ctx, "streams.settle",
		attribute.String("stream_id", stream.ID),
		attribute.String("target_status", string(status)),
	)
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	spentBig, _ := usdc.Parse(stream.SpentAmount)
	holdBig, _ := usdc.Parse(stream.HoldAmount)
	unused := new(big.Int).Sub(holdBig, spentBig)

	// 1. Release unused hold back to buyer first (fail-safe order).
	if unused.Sign() > 0 {
		unusedStr := usdc.Format(unused)
		if err := s.ledger.ReleaseHold(ctx, stream.BuyerAddr, unusedStr, stream.ID); err != nil {
			return nil, fmt.Errorf("failed to release unused hold: %w", err)
		}
	}

	// 2. Settle spent portion: buyer pending → seller available.
	if spentBig.Sign() > 0 {
		if err := s.ledger.SettleHold(ctx, stream.BuyerAddr, stream.SellerAddr, stream.SpentAmount, stream.ID); err != nil {
			// CRITICAL: Unused funds released but spent portion not settled.
			// The seller is owed money. Mark as settlement_failed so the stale
			// timer won't keep retrying (which would fail identically each time).
			logging.L(ctx).Error("CRITICAL: stream release succeeded but settle failed — seller owed money",
				"stream", stream.ID, "seller", stream.SellerAddr, "amount", stream.SpentAmount, "error", err)
			stream.Status = StatusSettlementFailed
			stream.UpdatedAt = time.Now()
			if storeErr := s.store.Update(ctx, stream); storeErr != nil {
				logging.L(ctx).Error("CRITICAL: could not mark stream as settlement_failed",
					"stream", stream.ID, "error", storeErr)
			}
			return nil, fmt.Errorf("failed to settle hold (unused funds released, seller owed %s — requires reconciliation): %w",
				stream.SpentAmount, err)
		}
	}

	now := time.Now()
	stream.Status = status
	stream.CloseReason = reason
	stream.ClosedAt = &now
	stream.UpdatedAt = now

	// CRITICAL: Funds already moved. Retry the status update because if this fails
	// and the stream stays "open", the auto-close timer could settle again (double payment).
	// We use DoWithUnlock to release the shard lock during backoff sleep so other
	// streams on the same shard are not blocked. The settlement itself (above) is
	// already complete, so releasing the lock during retry is safe — any concurrent
	// caller would fail at the ledger level (hold already released/settled).
	relockFn := func() { _ = s.locks.Lock(stream.ID) }
	updateErr := retry.DoWithUnlock(ctx, 3, 50*time.Millisecond, unlockFn, relockFn, func() error {
		if err := s.store.Update(ctx, stream); err != nil {
			logging.L(ctx).Warn("stream status update failed, retrying",
				"stream", stream.ID, "error", err)
			return err
		}
		return nil
	})
	if updateErr != nil {
		// All retries exhausted. Mark as settlement_failed so auto-close won't re-settle.
		stream.Status = StatusSettlementFailed
		if retryErr := s.store.Update(ctx, stream); retryErr != nil {
			logging.L(ctx).Error("CRITICAL: stream funds settled but ALL status updates failed including sentinel",
				"stream", stream.ID, "error", retryErr)
		} else {
			logging.L(ctx).Error("CRITICAL: stream marked as settlement_failed — funds moved but target status could not be set",
				"stream", stream.ID, "target_status", string(status))
		}
		return nil, fmt.Errorf("failed to update stream after settlement (requires manual resolution): %w", updateErr)
	}

	streamsClosed.WithLabelValues(string(stream.Status)).Inc()
	streamDuration.Observe(time.Since(stream.CreatedAt).Seconds())
	if amt, err := strconv.ParseFloat(stream.SpentAmount, 64); err == nil && amt > 0 {
		streamSettlementAmount.Observe(amt)
	}

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitStreamClosed(stream.BuyerAddr, stream.ID, stream.SellerAddr, stream.SpentAmount, string(stream.Status))
	}

	// Record transaction for reputation
	if s.recorder != nil && spentBig.Sign() > 0 {
		txStatus := "confirmed"
		if status == StatusDisputed {
			txStatus = "failed"
		}
		if err := s.recorder.RecordTransaction(ctx, stream.ID, stream.BuyerAddr, stream.SellerAddr, stream.SpentAmount, stream.ServiceID, txStatus); err != nil {
			slog.Error("stream settle: failed to record transaction", "stream_id", stream.ID, "error", err)
		}
	}

	// Intercept revenue for stakes (seller earned money)
	if s.revenue != nil && spentBig.Sign() > 0 && status != StatusDisputed {
		if err := s.revenue.AccumulateRevenue(ctx, stream.SellerAddr, stream.SpentAmount, "stream_settle:"+stream.ID); err != nil {
			slog.Error("stream settle: failed to accumulate revenue", "stream_id", stream.ID, "seller", stream.SellerAddr, "error", err)
		}
	}

	// Issue receipt for stream settlement
	if s.receiptIssuer != nil && spentBig.Sign() > 0 {
		rcptStatus := "confirmed"
		if status == StatusDisputed {
			rcptStatus = "failed"
		}
		if err := s.receiptIssuer.IssueReceipt(ctx, "stream", stream.ID, stream.BuyerAddr,
			stream.SellerAddr, stream.SpentAmount, stream.ServiceID, rcptStatus, string(status)); err != nil {
			slog.Error("stream settle: failed to issue receipt", "stream_id", stream.ID, "error", err)
		}
	}

	return stream, nil
}

// ForceCloseStale auto-closes all stale streams. Returns the number closed.
func (s *Service) ForceCloseStale(ctx context.Context) (int, error) {
	stale, err := s.store.ListStale(ctx, time.Now(), 100)
	if err != nil {
		return 0, err
	}

	closed := 0
	for _, stream := range stale {
		if stream.IsTerminal() {
			continue
		}
		if err := s.AutoClose(ctx, stream); err != nil {
			continue
		}
		closed++
	}
	return closed, nil
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
