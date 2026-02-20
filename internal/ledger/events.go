package ledger

import (
	"context"
	"math/big"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// Event represents an immutable ledger event.
type Event struct {
	ID           int64     `json:"id"`
	AgentAddr    string    `json:"agentAddr"`
	EventType    string    `json:"eventType"`
	Amount       string    `json:"amount"`
	Reference    string    `json:"reference,omitempty"`
	Counterparty string    `json:"counterparty,omitempty"`
	Metadata     string    `json:"metadata,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ReconciliationResult holds the outcome of replaying events vs actual balance.
type ReconciliationResult struct {
	AgentAddr       string `json:"agentAddr"`
	Match           bool   `json:"match"`
	ReplayAvailable string `json:"replayAvailable"`
	ReplayPending   string `json:"replayPending"`
	ReplayEscrowed  string `json:"replayEscrowed"`
	ActualAvailable string `json:"actualAvailable"`
	ActualPending   string `json:"actualPending"`
	ActualEscrowed  string `json:"actualEscrowed"`
}

// EventStore persists and queries immutable ledger events.
type EventStore interface {
	AppendEvent(ctx context.Context, event *Event) error
	GetEvents(ctx context.Context, agentAddr string, since time.Time) ([]*Event, error)
	GetAllAgents(ctx context.Context) ([]string, error)
}

// RebuildBalance replays a sequence of events to reconstruct a balance.
func RebuildBalance(agentAddr string, events []*Event) *Balance {
	available := big.NewInt(0)
	pending := big.NewInt(0)
	escrowed := big.NewInt(0)
	totalIn := big.NewInt(0)
	totalOut := big.NewInt(0)

	for _, e := range events {
		amt, ok := usdc.Parse(e.Amount)
		if !ok {
			continue
		}

		switch e.EventType {
		case "deposit":
			available.Add(available, amt)
			totalIn.Add(totalIn, amt)
		case "spend":
			available.Sub(available, amt)
			totalOut.Add(totalOut, amt)
		case "withdrawal":
			available.Sub(available, amt)
			totalOut.Add(totalOut, amt)
		case "refund":
			available.Add(available, amt)
		case "hold":
			available.Sub(available, amt)
			pending.Add(pending, amt)
		case "confirm_hold":
			pending.Sub(pending, amt)
			totalOut.Add(totalOut, amt)
		case "release_hold":
			pending.Sub(pending, amt)
			available.Add(available, amt)
		case "escrow_lock":
			available.Sub(available, amt)
			escrowed.Add(escrowed, amt)
		case "escrow_release":
			escrowed.Sub(escrowed, amt)
			totalOut.Add(totalOut, amt)
		case "escrow_receive":
			available.Add(available, amt)
			totalIn.Add(totalIn, amt)
		case "escrow_refund":
			escrowed.Sub(escrowed, amt)
			available.Add(available, amt)
		case "settle_hold_out":
			// Buyer: hold is settled, pending → out.
			pending.Sub(pending, amt)
			totalOut.Add(totalOut, amt)
		case "settle_hold_in":
			// Seller: receives settled hold payment.
			available.Add(available, amt)
			totalIn.Add(totalIn, amt)
		case "transfer_out":
			available.Sub(available, amt)
			totalOut.Add(totalOut, amt)
		case "transfer_in":
			available.Add(available, amt)
			totalIn.Add(totalIn, amt)
		case "fee_in":
			// Platform receives fee.
			available.Add(available, amt)
			totalIn.Add(totalIn, amt)
		case "escrow_partial_release":
			// Buyer: escrowed portion released to seller.
			escrowed.Sub(escrowed, amt)
			totalOut.Add(totalOut, amt)
		case "escrow_partial_refund":
			// Buyer: escrowed portion refunded.
			escrowed.Sub(escrowed, amt)
			available.Add(available, amt)
		case "escrow_partial_receive":
			// Seller: receives partial escrow release.
			available.Add(available, amt)
			totalIn.Add(totalIn, amt)
		}
	}

	return &Balance{
		AgentAddr: agentAddr,
		Available: usdc.Format(available),
		Pending:   usdc.Format(pending),
		Escrowed:  usdc.Format(escrowed),
		TotalIn:   usdc.Format(totalIn),
		TotalOut:  usdc.Format(totalOut),
	}
}

// BalanceAtTime returns an agent's balance at a specific point in time by replaying events.
func BalanceAtTime(ctx context.Context, es EventStore, agentAddr string, ts time.Time) (*Balance, error) {
	events, err := es.GetEvents(ctx, agentAddr, time.Time{})
	if err != nil {
		return nil, err
	}

	// Filter events up to the given timestamp
	var filtered []*Event
	for _, e := range events {
		if !e.CreatedAt.After(ts) {
			filtered = append(filtered, e)
		}
	}

	return RebuildBalance(agentAddr, filtered), nil
}

// ReconcileAgent replays events for one agent and compares against actual balance.
func ReconcileAgent(ctx context.Context, es EventStore, store Store, agentAddr string) (*ReconciliationResult, error) {
	events, err := es.GetEvents(ctx, agentAddr, time.Time{})
	if err != nil {
		return nil, err
	}

	replayed := RebuildBalance(agentAddr, events)

	actual, err := store.GetBalance(ctx, agentAddr)
	if err != nil {
		return nil, err
	}

	// Normalize actual values through usdc.Parse/Format for consistent comparison
	actualAvail, _ := usdc.Parse(actual.Available)
	actualPend, _ := usdc.Parse(actual.Pending)
	actualEsc, _ := usdc.Parse(actual.Escrowed)

	result := &ReconciliationResult{
		AgentAddr:       agentAddr,
		ReplayAvailable: replayed.Available,
		ReplayPending:   replayed.Pending,
		ReplayEscrowed:  replayed.Escrowed,
		ActualAvailable: usdc.Format(actualAvail),
		ActualPending:   usdc.Format(actualPend),
		ActualEscrowed:  usdc.Format(actualEsc),
	}

	result.Match = replayed.Available == result.ActualAvailable &&
		replayed.Pending == result.ActualPending &&
		replayed.Escrowed == result.ActualEscrowed

	return result, nil
}

// ReconcileAll replays events for all agents and returns discrepancies.
func ReconcileAll(ctx context.Context, es EventStore, store Store) ([]*ReconciliationResult, error) {
	agents, err := es.GetAllAgents(ctx)
	if err != nil {
		return nil, err
	}

	var results []*ReconciliationResult
	for _, addr := range agents {
		r, err := ReconcileAgent(ctx, es, store, addr)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}

	return results, nil
}
