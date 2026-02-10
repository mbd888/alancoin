package stakes

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Distributor periodically checks for stakes due for revenue distribution
// and pays out accumulated revenue to shareholders.
type Distributor struct {
	service  *Service
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

// NewDistributor creates a new revenue distribution timer.
func NewDistributor(service *Service, logger *slog.Logger) *Distributor {
	return &Distributor{
		service:  service,
		interval: 60 * time.Second,
		logger:   logger,
		stop:     make(chan struct{}, 1),
	}
}

// Start begins the distribution loop. Call in a goroutine.
func (d *Distributor) Start(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stop:
			return
		case <-ticker.C:
			d.safeCheckAndDistribute(ctx)
		}
	}
}

// Stop signals the distributor to stop.
func (d *Distributor) Stop() {
	select {
	case d.stop <- struct{}{}:
	default:
	}
}

func (d *Distributor) safeCheckAndDistribute(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			d.logger.Error("panic in stakes distributor", "panic", fmt.Sprint(r))
		}
	}()
	d.checkAndDistribute(ctx)
}

func (d *Distributor) checkAndDistribute(ctx context.Context) {
	due, err := d.service.ListDueForDistribution(ctx, 100)
	if err != nil {
		d.logger.Warn("failed to list stakes due for distribution", "error", err)
		return
	}

	for _, stake := range due {
		if err := d.service.Distribute(ctx, stake); err != nil {
			d.logger.Warn("failed to distribute revenue",
				"stakeId", stake.ID,
				"agentAddr", stake.AgentAddr,
				"undistributed", stake.Undistributed,
				"error", err,
			)
			continue
		}
		d.logger.Info("distributed revenue",
			"stakeId", stake.ID,
			"agentAddr", stake.AgentAddr,
			"amount", stake.Undistributed,
		)
	}
}
