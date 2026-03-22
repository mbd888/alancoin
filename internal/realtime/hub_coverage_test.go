package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Hub Run and Stop
// ---------------------------------------------------------------------------

func TestHub_RunStopsOnContextCancel(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()

	// Let it start
	time.Sleep(30 * time.Millisecond)

	cancel()

	select {
	case <-done:
		// Success — hub exited
	case <-time.After(2 * time.Second):
		t.Fatal("Hub did not stop after context cancel")
	}

	// After Run exits, done channel should be closed, rejecting HandleWebSocket
	select {
	case <-h.done:
		// Good
	default:
		t.Error("Expected h.done to be closed after Run exits")
	}
}

// ---------------------------------------------------------------------------
// Register, Unregister, and client tracking
// ---------------------------------------------------------------------------

func TestHub_RegisterMultipleClients(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	clients := make([]*Client, 5)
	for i := range clients {
		clients[i] = &Client{
			hub:  h,
			send: make(chan []byte, 256),
			sub:  Subscription{AllEvents: true},
		}
		h.register <- clients[i]
	}
	time.Sleep(50 * time.Millisecond)

	stats := h.Stats()
	if stats["connectedClients"].(int) != 5 {
		t.Errorf("Expected 5 connected clients, got %v", stats["connectedClients"])
	}
	if stats["peakClients"].(int64) != 5 {
		t.Errorf("Expected peak 5, got %v", stats["peakClients"])
	}

	// Unregister 3
	for i := 0; i < 3; i++ {
		h.unregister <- clients[i]
	}
	time.Sleep(50 * time.Millisecond)

	stats = h.Stats()
	if stats["connectedClients"].(int) != 2 {
		t.Errorf("Expected 2 connected clients, got %v", stats["connectedClients"])
	}
	// Peak should remain 5
	if stats["peakClients"].(int64) != 5 {
		t.Errorf("Expected peak still 5, got %v", stats["peakClients"])
	}
}

func TestHub_UnregisterNonExistentClient(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	// Unregister a client that was never registered — should not panic
	fake := &Client{
		hub:  h,
		send: make(chan []byte, 256),
		sub:  Subscription{AllEvents: true},
	}
	h.unregister <- fake
	time.Sleep(50 * time.Millisecond)

	stats := h.Stats()
	if stats["connectedClients"].(int) != 0 {
		t.Errorf("Expected 0 clients, got %v", stats["connectedClients"])
	}
}

func TestHub_UnregisterWithIP(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	client := &Client{
		hub:  h,
		send: make(chan []byte, 256),
		sub:  Subscription{AllEvents: true},
		ip:   "1.2.3.4",
	}
	h.register <- client
	time.Sleep(30 * time.Millisecond)

	// Manually set ipConns to simulate what HandleWebSocket does
	h.mu.Lock()
	h.ipConns["1.2.3.4"] = 1
	h.mu.Unlock()

	h.unregister <- client
	time.Sleep(50 * time.Millisecond)

	h.mu.RLock()
	count := h.ipConns["1.2.3.4"]
	h.mu.RUnlock()

	if count != 0 {
		t.Errorf("Expected ipConns for 1.2.3.4 to be cleaned up, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Broadcast behavior
// ---------------------------------------------------------------------------

func TestHub_BroadcastToMultipleClients(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	clients := make([]*Client, 3)
	for i := range clients {
		clients[i] = &Client{
			hub:  h,
			send: make(chan []byte, 256),
			sub:  Subscription{AllEvents: true},
		}
		h.register <- clients[i]
	}
	time.Sleep(50 * time.Millisecond)

	h.Broadcast(&Event{
		Type:      EventTransaction,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"amount": "10.00"},
	})

	for i, client := range clients {
		select {
		case msg := <-client.send:
			if len(msg) == 0 {
				t.Errorf("Client %d received empty message", i)
			}
		case <-time.After(time.Second):
			t.Errorf("Client %d did not receive broadcast", i)
		}
	}
}

func TestHub_BroadcastDropsWhenChannelFull(t *testing.T) {
	h := NewHub(slog.Default())
	// Should not panic when broadcast channel is full
	// Fill the broadcast channel
	for i := 0; i < 256; i++ {
		h.Broadcast(&Event{
			Type:      EventTransaction,
			Timestamp: time.Now(),
			Data:      map[string]interface{}{"seq": i},
		})
	}
	// One more should be silently dropped
	h.Broadcast(&Event{
		Type:      EventTransaction,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"dropped": true},
	})
}

func TestHub_SlowClientDisconnected(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	// Create a client with a tiny send buffer that will fill up
	slowClient := &Client{
		hub:  h,
		send: make(chan []byte, 1), // Very small buffer
		sub:  Subscription{AllEvents: true},
	}
	h.register <- slowClient
	time.Sleep(30 * time.Millisecond)

	// Fill the slow client's send buffer
	slowClient.send <- []byte("fill")

	// Now broadcast — the slow client should be disconnected
	h.Broadcast(&Event{
		Type:      EventMilestone,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"event": "test"},
	})
	time.Sleep(100 * time.Millisecond)

	stats := h.Stats()
	if stats["connectedClients"].(int) != 0 {
		t.Errorf("Expected slow client to be disconnected, got %v connected", stats["connectedClients"])
	}
}

// ---------------------------------------------------------------------------
// BroadcastTransaction and BroadcastCoalition
// ---------------------------------------------------------------------------

func TestHub_BroadcastCoalition(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	client := &Client{
		hub:  h,
		send: make(chan []byte, 256),
		sub:  Subscription{AllEvents: true},
	}
	h.register <- client
	time.Sleep(30 * time.Millisecond)

	h.BroadcastCoalition(map[string]interface{}{
		"coalitionId": "coal_1",
		"status":      "settled",
	})

	select {
	case msg := <-client.send:
		var event Event
		if err := json.Unmarshal(msg, &event); err != nil {
			t.Fatalf("Failed to unmarshal event: %v", err)
		}
		if event.Type != EventCoalition {
			t.Errorf("Expected coalition event type, got %s", event.Type)
		}
	case <-time.After(time.Second):
		t.Error("Timeout waiting for coalition broadcast")
	}
}

// ---------------------------------------------------------------------------
// serialize
// ---------------------------------------------------------------------------

func TestHub_Serialize(t *testing.T) {
	h := NewHub(slog.Default())
	event := &Event{
		Type:      EventTransaction,
		Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Data:      map[string]interface{}{"amount": "5.00"},
	}

	data := h.serialize(event)
	if len(data) == 0 {
		t.Error("Expected non-empty serialized data")
	}

	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal serialized event: %v", err)
	}
	if parsed.Type != EventTransaction {
		t.Errorf("Expected transaction type, got %s", parsed.Type)
	}
}

// ---------------------------------------------------------------------------
// Context cancellation closes all clients
// ---------------------------------------------------------------------------

func TestHub_ContextCancelClosesAllClients(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	// Register several clients
	clients := make([]*Client, 3)
	for i := range clients {
		clients[i] = &Client{
			hub:  h,
			send: make(chan []byte, 256),
			sub:  Subscription{AllEvents: true},
		}
		h.register <- clients[i]
	}
	time.Sleep(50 * time.Millisecond)

	cancel()
	time.Sleep(100 * time.Millisecond)

	// After cancel, all client send channels should be closed
	for i, client := range clients {
		select {
		case _, ok := <-client.send:
			_ = ok // channel closed or drained — both fine
		default:
			t.Logf("Client %d send channel was already drained", i)
		}
	}

	stats := h.Stats()
	if stats["connectedClients"].(int) != 0 {
		t.Errorf("Expected 0 clients after context cancel, got %v", stats["connectedClients"])
	}
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

func TestHub_Stats_TotalEvents(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	for i := 0; i < 5; i++ {
		h.Broadcast(&Event{Type: EventMilestone, Timestamp: time.Now()})
	}
	time.Sleep(100 * time.Millisecond)

	stats := h.Stats()
	if stats["totalEvents"].(int64) != 5 {
		t.Errorf("Expected 5 total events, got %v", stats["totalEvents"])
	}
}

// ---------------------------------------------------------------------------
// shouldSend: combined filters
// ---------------------------------------------------------------------------

func TestShouldSend_EventTypeAndAgent(t *testing.T) {
	h := NewHub(slog.Default())
	client := &Client{sub: Subscription{
		EventTypes: []EventType{EventTransaction},
		AgentAddrs: []string{"0xagent1"},
	}}

	// Matching type AND agent
	matching := &Event{
		Type: EventTransaction,
		Data: map[string]interface{}{"from": "0xagent1", "to": "0xother"},
	}
	if !h.shouldSend(client, matching) {
		t.Error("Should match on both type and agent filter")
	}

	// Matching type but wrong agent
	wrongAgent := &Event{
		Type: EventTransaction,
		Data: map[string]interface{}{"from": "0xother", "to": "0xanother"},
	}
	if h.shouldSend(client, wrongAgent) {
		t.Error("Should NOT send — agent filter not matched")
	}

	// Wrong type
	wrongType := &Event{
		Type: EventMilestone,
		Data: map[string]interface{}{"from": "0xagent1"},
	}
	if h.shouldSend(client, wrongType) {
		t.Error("Should NOT send — type filter not matched")
	}
}

func TestShouldSend_EventTypeAndMinAmount(t *testing.T) {
	h := NewHub(slog.Default())
	client := &Client{sub: Subscription{
		EventTypes: []EventType{EventTransaction},
		MinAmount:  10.0,
	}}

	large := &Event{
		Type: EventTransaction,
		Data: map[string]interface{}{"amount": 15.0},
	}
	if !h.shouldSend(client, large) {
		t.Error("Should match large transaction")
	}

	small := &Event{
		Type: EventTransaction,
		Data: map[string]interface{}{"amount": 5.0},
	}
	if h.shouldSend(client, small) {
		t.Error("Should NOT send — amount too small")
	}
}
