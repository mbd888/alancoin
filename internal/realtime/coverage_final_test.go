package realtime

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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
