package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

// --- merged from coverage_final_test.go ---

// ---------------------------------------------------------------------------
// HandleWebSocket — full WebSocket upgrade path
// ---------------------------------------------------------------------------

func TestHandleWebSocket_FullLifecycle(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	// Create test HTTP server that hands off to HandleWebSocket
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleWebSocket(w, r)
	}))
	defer server.Close()

	// Dial a WebSocket connection
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close() //nolint:errcheck
	}
	if err != nil {
		t.Fatalf("Failed to dial websocket: %v", err)
	}

	// Wait for registration
	time.Sleep(100 * time.Millisecond)

	stats := h.Stats()
	if stats["connectedClients"].(int) != 1 {
		t.Errorf("Expected 1 connected client, got %v", stats["connectedClients"])
	}

	// Send a subscription update
	sub := `{"allEvents":false,"eventTypes":["transaction"]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(sub)); err != nil {
		t.Fatalf("Failed to write subscription: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Broadcast a transaction event
	h.Broadcast(&Event{
		Type:      EventTransaction,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"amount": "5.00", "from": "0xa"},
	})

	// Read the event from the WebSocket
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read message: %v", err)
	}
	if len(msg) == 0 {
		t.Error("Expected non-empty message")
	}

	// Broadcast a milestone event — should be filtered out
	h.Broadcast(&Event{
		Type:      EventMilestone,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"event": "filtered"},
	})

	// Close the connection
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	conn.Close()

	// Wait for unregister
	time.Sleep(200 * time.Millisecond)

	stats = h.Stats()
	if stats["connectedClients"].(int) != 0 {
		t.Errorf("Expected 0 connected clients after close, got %v", stats["connectedClients"])
	}
}

func TestHandleWebSocket_RejectsAfterHubStopped(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)

	cancel()
	<-done

	// After hub stops, HandleWebSocket should return 503
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ws", nil)
	h.HandleWebSocket(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 after hub stopped, got %d", w.Code)
	}
}

func TestHandleWebSocket_ConnectionLimitEnforced(t *testing.T) {
	h := NewHub(slog.Default())
	// Set a very small maxClients for testing
	h.maxClients = 1
	h.connSem = make(chan struct{}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleWebSocket(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// First connection should succeed
	conn1, resp1, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp1 != nil && resp1.Body != nil {
		defer resp1.Body.Close() //nolint:errcheck
	}
	if err != nil {
		t.Fatalf("First connection should succeed: %v", err)
	}
	defer conn1.Close()
	time.Sleep(100 * time.Millisecond)

	// Second connection should be rejected (max 1)
	_, resp2, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp2 != nil && resp2.Body != nil {
		defer resp2.Body.Close() //nolint:errcheck
	}
	if err == nil {
		t.Error("Second connection should have been rejected")
	}
	if resp2 != nil && resp2.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 for connection limit, got %d", resp2.StatusCode)
	}
}

func TestHandleWebSocket_PerIPLimit(t *testing.T) {
	h := NewHub(slog.Default())
	h.maxPerIP = 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleWebSocket(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// First connection should succeed
	conn1, r1, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if r1 != nil && r1.Body != nil {
		defer r1.Body.Close() //nolint:errcheck
	}
	if err != nil {
		t.Fatalf("First connection should succeed: %v", err)
	}
	defer conn1.Close()
	time.Sleep(100 * time.Millisecond)

	// Second connection from same IP should be rejected
	_, r2, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if r2 != nil && r2.Body != nil {
		defer r2.Body.Close() //nolint:errcheck
	}
	if err == nil {
		t.Error("Second connection from same IP should have been rejected")
	}
	if r2 != nil && r2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected 429 for per-IP limit, got %d", r2.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// writePump — pong/close handling
// ---------------------------------------------------------------------------

func TestWritePump_SendsClose(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleWebSocket(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, dialResp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if dialResp != nil && dialResp.Body != nil {
		defer dialResp.Body.Close() //nolint:errcheck
	}
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Send multiple messages then close
	h.Broadcast(&Event{Type: EventTransaction, Timestamp: time.Now(), Data: map[string]interface{}{"a": 1}})
	h.Broadcast(&Event{Type: EventTransaction, Timestamp: time.Now(), Data: map[string]interface{}{"a": 2}})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read first message: %v", err)
	}
	_, _, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read second message: %v", err)
	}

	conn.Close()
	time.Sleep(100 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// readPump — subscription parsing with oversized filter arrays
// ---------------------------------------------------------------------------

func TestReadPump_OversizedFilterArrays(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleWebSocket(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, dr, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if dr != nil && dr.Body != nil {
		defer dr.Body.Close() //nolint:errcheck
	}
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	// Send subscription with oversized arrays (>100 entries)
	// Build a JSON array with 150 event types
	var types []string
	for i := 0; i < 150; i++ {
		types = append(types, `"transaction"`)
	}
	sub := `{"eventTypes":[` + strings.Join(types, ",") + `]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(sub)); err != nil {
		t.Fatalf("Failed to write subscription: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Should not crash; connection should still be alive
	h.Broadcast(&Event{Type: EventTransaction, Timestamp: time.Now(), Data: map[string]interface{}{"test": true}})
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Connection should still be alive after oversized sub: %v", err)
	}
	if len(msg) == 0 {
		t.Error("Expected non-empty message")
	}
}

// ---------------------------------------------------------------------------
// readPump — invalid JSON subscription
// ---------------------------------------------------------------------------

func TestReadPump_InvalidJSON(t *testing.T) {
	h := NewHub(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleWebSocket(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, dr, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if dr != nil && dr.Body != nil {
		defer dr.Body.Close() //nolint:errcheck
	}
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	// Send invalid JSON — should not crash, subscription unchanged
	if err := conn.WriteMessage(websocket.TextMessage, []byte("not-json")); err != nil {
		t.Fatalf("Failed to write: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Client should still receive events (default AllEvents=true)
	h.Broadcast(&Event{Type: EventMilestone, Timestamp: time.Now(), Data: map[string]interface{}{"ok": true}})
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Expected message after invalid JSON sub: %v", err)
	}
	if len(msg) == 0 {
		t.Error("Expected non-empty message")
	}
}

// ---------------------------------------------------------------------------
// upgrader origin check
// ---------------------------------------------------------------------------

func TestUpgrader_CheckOrigin_ValidSameHost(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "https://example.com")

	allowed := upgrader.CheckOrigin(req)
	if !allowed {
		t.Error("Same-host origin should be allowed")
	}
}

func TestUpgrader_CheckOrigin_NoOrigin(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Host = "example.com"
	// No Origin header — non-browser client

	allowed := upgrader.CheckOrigin(req)
	if !allowed {
		t.Error("No origin (non-browser) should be allowed")
	}
}

func TestUpgrader_CheckOrigin_MismatchedHost(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "https://evil.com")

	allowed := upgrader.CheckOrigin(req)
	if allowed {
		t.Error("Mismatched origin should be rejected")
	}
}

// --- merged from hub_coverage_test.go ---

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
