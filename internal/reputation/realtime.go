package reputation

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/traces"
	"go.opentelemetry.io/otel/attribute"
)

// RealtimeUpdater provides immediate reputation score adjustments after
// transactions, without waiting for the periodic snapshot worker.
// It maintains a cache of score deltas that are applied on top of the
// latest snapshot. The periodic worker remains the source of truth.
type RealtimeUpdater struct {
	calculator *Calculator
	provider   MetricsProvider
	store      SnapshotStore
	logger     *slog.Logger

	// deltaCache maps agent address to accumulated score delta since last snapshot.
	mu         sync.RWMutex
	deltaCache map[string]*scoreDelta

	// Shutdown coordination for background persist goroutines.
	wg             sync.WaitGroup
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

type scoreDelta struct {
	txnsDelta     int     // change in transaction count since last snapshot
	volumeDelta   float64 // change in volume since last snapshot
	successDelta  int     // successful txns since last snapshot
	failedDelta   int     // failed txns since last snapshot
	disputedDelta int     // disputed txns since last snapshot
}

// NewRealtimeUpdater creates a new real-time reputation updater.
func NewRealtimeUpdater(provider MetricsProvider, store SnapshotStore, logger *slog.Logger) *RealtimeUpdater {
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel stored in struct, called in Shutdown()
	return &RealtimeUpdater{
		calculator:     NewCalculator(),
		provider:       provider,
		store:          store,
		logger:         logger,
		deltaCache:     make(map[string]*scoreDelta),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
	}
}

// Shutdown cancels background persist goroutines and waits for them to complete.
// Should be called during graceful server shutdown.
func (u *RealtimeUpdater) Shutdown(timeout time.Duration) {
	u.shutdownCancel()
	done := make(chan struct{})
	go func() {
		u.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		u.logger.Warn("realtime updater: shutdown timed out waiting for persist goroutines")
	}
}

// OnTransactionConfirmed is called after a successful payment settlement.
// It immediately adjusts the seller's and buyer's reputation scores.
func (u *RealtimeUpdater) OnTransactionConfirmed(ctx context.Context, buyerAddr, sellerAddr, amountUSD string) {
	_, span := traces.StartSpan(ctx, "reputation.OnTransactionConfirmed",
		attribute.String("buyer", buyerAddr),
		attribute.String("seller", sellerAddr),
		traces.Amount(amountUSD),
	)
	defer span.End()

	vol := parseFloat(amountUSD)

	// Update seller: +1 successful txn, +volume
	u.applyDelta(sellerAddr, 1, vol, 1, 0, 0)

	// Update buyer: +1 successful txn, +volume
	u.applyDelta(buyerAddr, 1, vol, 1, 0, 0)

	// Persist updated scores asynchronously
	u.wg.Add(1)
	go u.persistScoreTracked(sellerAddr)
	u.wg.Add(1)
	go u.persistScoreTracked(buyerAddr)
}

// OnTransactionFailed is called when a service delivery fails.
func (u *RealtimeUpdater) OnTransactionFailed(ctx context.Context, sellerAddr, amountUSD string) {
	_, span := traces.StartSpan(ctx, "reputation.OnTransactionFailed",
		attribute.String("seller", sellerAddr),
		traces.Amount(amountUSD),
	)
	defer span.End()

	vol := parseFloat(amountUSD)
	u.applyDelta(sellerAddr, 1, vol, 0, 1, 0)

	u.wg.Add(1)
	go u.persistScoreTracked(sellerAddr)
}

// OnDisputeResolved is called when an escrow dispute is resolved.
func (u *RealtimeUpdater) OnDisputeResolved(ctx context.Context, sellerAddr, buyerAddr string, buyerWon bool) {
	_, span := traces.StartSpan(ctx, "reputation.OnDisputeResolved",
		attribute.String("seller", sellerAddr),
		attribute.String("buyer", buyerAddr),
		attribute.Bool("buyer_won", buyerWon),
	)
	defer span.End()

	if buyerWon {
		// Seller loses reputation for dispute loss
		u.applyDelta(sellerAddr, 0, 0, 0, 0, 1)
		u.wg.Add(1)
		go u.persistScoreTracked(sellerAddr)
	} else {
		// Buyer loses reputation for frivolous dispute
		u.applyDelta(buyerAddr, 0, 0, 0, 0, 1)
		u.wg.Add(1)
		go u.persistScoreTracked(buyerAddr)
	}
}

// ClearDeltas resets the delta cache. Called by the periodic snapshot worker
// after a full recalculation to prevent delta drift.
func (u *RealtimeUpdater) ClearDeltas() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.deltaCache = make(map[string]*scoreDelta)
}

// GetAdjustedScore returns the latest snapshot score plus real-time deltas.
func (u *RealtimeUpdater) GetAdjustedScore(ctx context.Context, addr string) (*Score, error) {
	// Get latest snapshot
	snap, err := u.store.Latest(ctx, addr)
	if err != nil {
		return nil, err
	}

	if snap == nil {
		// No snapshot exists, compute from scratch
		metrics, err := u.provider.GetAgentMetrics(ctx, addr)
		if err != nil {
			return nil, err
		}
		return u.calculator.Calculate(addr, *metrics), nil
	}

	// Apply delta adjustments
	u.mu.RLock()
	delta, hasDelta := u.deltaCache[addr]
	u.mu.RUnlock()

	if !hasDelta {
		return &Score{
			Address: snap.Address,
			Score:   snap.Score,
			Tier:    snap.Tier,
			Components: Components{
				VolumeScore:    snap.VolumeScore,
				ActivityScore:  snap.ActivityScore,
				SuccessScore:   snap.SuccessScore,
				AgeScore:       snap.AgeScore,
				DiversityScore: snap.DiversityScore,
			},
			Metrics: Metrics{
				TotalTransactions: snap.TotalTxns,
				TotalVolumeUSD:    snap.TotalVolume,
			},
			CalculatedAt: snap.CreatedAt,
		}, nil
	}

	// Apply deltas to snapshot metrics and recalculate
	totalTxns := snap.TotalTxns + delta.txnsDelta
	successTxns := int(snap.SuccessRate*float64(snap.TotalTxns)) + delta.successDelta
	totalVolume := snap.TotalVolume + delta.volumeDelta

	metrics := Metrics{
		TotalTransactions:    totalTxns,
		TotalVolumeUSD:       totalVolume,
		SuccessfulTxns:       successTxns,
		FailedTxns:           (totalTxns - successTxns),
		UniqueCounterparties: snap.UniquePeers,                          // can't increment from delta alone
		DaysOnNetwork:        int(math.Max(1, float64(snap.TotalTxns))), // approximate
	}

	return u.calculator.Calculate(addr, metrics), nil
}

func (u *RealtimeUpdater) applyDelta(addr string, txns int, volume float64, success, failed, disputed int) {
	u.mu.Lock()
	defer u.mu.Unlock()

	d, ok := u.deltaCache[addr]
	if !ok {
		d = &scoreDelta{}
		u.deltaCache[addr] = d
	}
	d.txnsDelta += txns
	d.volumeDelta += volume
	d.successDelta += success
	d.failedDelta += failed
	d.disputedDelta += disputed
}

func (u *RealtimeUpdater) persistScoreTracked(addr string) {
	defer u.wg.Done()
	ctx, cancel := context.WithTimeout(u.shutdownCtx, 5*time.Second)
	defer cancel()
	u.persistScore(ctx, addr)
}

func (u *RealtimeUpdater) persistScore(ctx context.Context, addr string) {
	score, err := u.GetAdjustedScore(ctx, addr)
	if err != nil {
		u.logger.Warn("realtime: failed to compute adjusted score", "address", addr, "error", err)
		return
	}

	snap := SnapshotFromScore(score)
	if err := u.store.Save(ctx, snap); err != nil {
		u.logger.Warn("realtime: failed to persist score", "address", addr, "error", err)
	}
}

func parseFloat(s string) float64 {
	var f float64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			var dec float64
			var pow float64 = 10
			for j := i + 1; j < len(s); j++ {
				dec += float64(s[j]-'0') / pow
				pow *= 10
			}
			f += dec
			break
		}
		f = f*10 + float64(c-'0')
	}
	return f
}
