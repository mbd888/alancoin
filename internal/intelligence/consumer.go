package intelligence

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/mbd888/alancoin/internal/eventbus"
	"github.com/mbd888/alancoin/internal/idgen"
)

// MakeSettlementConsumer creates an event bus handler that recomputes
// intelligence profiles for both buyer and seller on settlement events.
func MakeSettlementConsumer(engine *Engine, store Store, logger *slog.Logger) eventbus.Handler {
	return func(ctx context.Context, events []eventbus.Event) error {
		// Collect unique addresses from batch
		addresses := make(map[string]bool)
		for _, e := range events {
			var p eventbus.SettlementPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				logger.Warn("intelligence: failed to unmarshal settlement event",
					"eventId", e.ID, "error", err)
				continue
			}
			if p.BuyerAddr != "" {
				addresses[p.BuyerAddr] = true
			}
			if p.SellerAddr != "" {
				addresses[p.SellerAddr] = true
			}
		}

		if len(addresses) == 0 {
			return nil
		}

		runID := idgen.WithPrefix("intel_rt_")

		for addr := range addresses {
			profile, err := engine.ComputeOne(ctx, addr, runID)
			if err != nil {
				logger.Warn("intelligence: real-time recompute failed",
					"address", addr, "error", err)
				continue
			}

			if err := store.SaveProfile(ctx, profile); err != nil {
				logger.Warn("intelligence: failed to save real-time profile",
					"address", addr, "error", err)
			}

			realtimeUpdatesTotal.Inc()
		}

		return nil
	}
}
