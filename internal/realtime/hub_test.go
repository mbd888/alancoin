package realtime

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func testHub() *Hub {
	return NewHub(slog.Default())
}

// ---------------------------------------------------------------------------
// shouldSend tests
// ---------------------------------------------------------------------------

func TestShouldSend_AllEvents(t *testing.T) {
	h := testHub()
	client := &Client{sub: Subscription{AllEvents: true}}

	event := &Event{Type: EventTransaction, Timestamp: time.Now()}
	if !h.shouldSend(client, event) {
		t.Error("AllEvents client should receive all events")
	}
}

func TestShouldSend_EventTypeFilter(t *testing.T) {
	h := testHub()

	client := &Client{sub: Subscription{
		EventTypes: []EventType{EventTransaction, EventAgentJoined},
	}}

	txEvent := &Event{Type: EventTransaction}
	joinedEvent := &Event{Type: EventAgentJoined}
	milestoneEvent := &Event{Type: EventMilestone}

	if !h.shouldSend(client, txEvent) {
		t.Error("Should receive transaction events")
	}
	if !h.shouldSend(client, joinedEvent) {
		t.Error("Should receive agent_joined events")
	}
	if h.shouldSend(client, milestoneEvent) {
		t.Error("Should NOT receive milestone events")
	}
}

func TestShouldSend_AgentFilter(t *testing.T) {
	h := testHub()

	client := &Client{sub: Subscription{
		AgentAddrs: []string{"0xagent1"},
	}}

	matching := &Event{
		Type: EventTransaction,
		Data: map[string]interface{}{"from": "0xagent1", "to": "0xother"},
	}
	notMatching := &Event{
		Type: EventTransaction,
		Data: map[string]interface{}{"from": "0xother", "to": "0xanother"},
	}
	matchingTo := &Event{
		Type: EventTransaction,
		Data: map[string]interface{}{"from": "0xsender", "to": "0xagent1"},
	}
	matchingAuthor := &Event{
		Type: EventAgentJoined,
		Data: map[string]interface{}{"authorAddr": "0xagent1"},
	}

	if !h.shouldSend(client, matching) {
		t.Error("Should match on from address")
	}
	if h.shouldSend(client, notMatching) {
		t.Error("Should NOT match unrelated agents")
	}
	if !h.shouldSend(client, matchingTo) {
		t.Error("Should match on to address")
	}
	if !h.shouldSend(client, matchingAuthor) {
		t.Error("Should match on authorAddr")
	}
}

func TestShouldSend_MinAmountFilter(t *testing.T) {
	h := testHub()

	client := &Client{sub: Subscription{
		MinAmount: 10.0,
	}}

	large := &Event{
		Type: EventTransaction,
		Data: map[string]interface{}{"amount": 15.0},
	}
	small := &Event{
		Type: EventTransaction,
		Data: map[string]interface{}{"amount": 5.0},
	}
	milestone := &Event{
		Type: EventMilestone,
		Data: map[string]interface{}{"content": "test"},
	}

	if !h.shouldSend(client, large) {
		t.Error("Should receive large transaction")
	}
	if h.shouldSend(client, small) {
		t.Error("Should NOT receive small transaction")
	}
	if !h.shouldSend(client, milestone) {
		t.Error("MinAmount filter should only apply to transactions")
	}
}

func TestShouldSend_EmptySubscription(t *testing.T) {
	h := testHub()

	// No filters, not AllEvents
	client := &Client{sub: Subscription{}}

	event := &Event{Type: EventTransaction}
	if !h.shouldSend(client, event) {
		t.Error("Empty subscription (no filters) should receive events")
	}
}

func TestShouldSend_NonMapData(t *testing.T) {
	h := testHub()

	client := &Client{sub: Subscription{
		AgentAddrs: []string{"0xagent1"},
	}}

	// Event with non-map data should not crash
	event := &Event{
		Type: EventMilestone,
		Data: "string data not a map",
	}

	// Agent filter skips non-map data (can't extract addresses), so event passes through
	if !h.shouldSend(client, event) {
		t.Error("Non-map data should pass through when agent filter can't extract addresses")
	}
}

// ---------------------------------------------------------------------------
// Hub lifecycle tests
// ---------------------------------------------------------------------------

func TestHub_Stats_Initial(t *testing.T) {
	h := testHub()

	stats := h.Stats()
	if stats["connectedClients"].(int) != 0 {
		t.Errorf("Expected 0 connected clients, got %v", stats["connectedClients"])
	}
	if stats["totalEvents"].(int64) != 0 {
		t.Errorf("Expected 0 total events, got %v", stats["totalEvents"])
	}
}

func TestHub_BroadcastAndStats(t *testing.T) {
	h := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	// Broadcast an event
	h.Broadcast(&Event{Type: EventTransaction, Timestamp: time.Now()})
	time.Sleep(50 * time.Millisecond)

	stats := h.Stats()
	if stats["totalEvents"].(int64) != 1 {
		t.Errorf("Expected 1 total event, got %v", stats["totalEvents"])
	}
}

func TestHub_RegisterUnregister(t *testing.T) {
	h := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	client := &Client{
		hub:  h,
		send: make(chan []byte, 256),
		sub:  Subscription{AllEvents: true},
	}

	h.register <- client
	time.Sleep(50 * time.Millisecond)

	stats := h.Stats()
	if stats["connectedClients"].(int) != 1 {
		t.Errorf("Expected 1 connected client, got %v", stats["connectedClients"])
	}
	if stats["peakClients"].(int64) != 1 {
		t.Errorf("Expected peak 1, got %v", stats["peakClients"])
	}

	h.unregister <- client
	time.Sleep(50 * time.Millisecond)

	stats = h.Stats()
	if stats["connectedClients"].(int) != 0 {
		t.Errorf("Expected 0 connected clients after unregister, got %v", stats["connectedClients"])
	}
	// Peak should still be 1
	if stats["peakClients"].(int64) != 1 {
		t.Errorf("Expected peak still 1, got %v", stats["peakClients"])
	}
}

func TestHub_BroadcastToClient(t *testing.T) {
	h := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	client := &Client{
		hub:  h,
		send: make(chan []byte, 256),
		sub:  Subscription{AllEvents: true},
	}

	h.register <- client
	time.Sleep(50 * time.Millisecond)

	h.Broadcast(&Event{
		Type:      EventTransaction,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"amount": "5.00"},
	})

	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("Expected non-empty message")
		}
	case <-time.After(time.Second):
		t.Error("Timeout waiting for broadcast")
	}
}

func TestHub_BroadcastTransaction(t *testing.T) {
	h := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	// Should not panic
	h.BroadcastTransaction(map[string]interface{}{
		"from": "0xa", "to": "0xb", "amount": "1.00",
	})
}

func TestHub_ContextCancellation(t *testing.T) {
	h := testHub()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Hub stopped
	case <-time.After(2 * time.Second):
		t.Error("Hub did not stop after context cancellation")
	}
}

func TestHub_FilteredBroadcast(t *testing.T) {
	h := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	// Client only wants milestones
	client := &Client{
		hub:  h,
		send: make(chan []byte, 256),
		sub:  Subscription{EventTypes: []EventType{EventMilestone}},
	}

	h.register <- client
	time.Sleep(50 * time.Millisecond)

	// Send a transaction event (should be filtered out)
	h.Broadcast(&Event{Type: EventTransaction, Timestamp: time.Now()})
	time.Sleep(100 * time.Millisecond)

	select {
	case <-client.send:
		t.Error("Client should NOT receive transaction event")
	default:
		// Good - filtered out
	}

	// Send a milestone event (should be received)
	h.Broadcast(&Event{Type: EventMilestone, Timestamp: time.Now()})

	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("Expected non-empty message")
		}
	case <-time.After(time.Second):
		t.Error("Client should receive milestone event")
	}
}
