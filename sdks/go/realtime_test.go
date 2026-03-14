package alancoin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockWSConn is a test double for wsConn.
type mockWSConn struct {
	mu       sync.Mutex
	messages [][]byte
	readIdx  int
	closed   atomic.Bool
	written  [][]byte
	writeMu  sync.Mutex

	// Controls
	readDelay time.Duration // artificial delay per read
	readErr   error         // error to return after messages exhausted
	writeErr  error
}

func (m *mockWSConn) ReadMessage() (int, []byte, error) {
	if m.closed.Load() {
		return 0, nil, errors.New("connection closed")
	}
	if m.readDelay > 0 {
		time.Sleep(m.readDelay)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readIdx >= len(m.messages) {
		if m.readErr != nil {
			return 0, nil, m.readErr
		}
		// Block until closed.
		m.mu.Unlock()
		for !m.closed.Load() {
			time.Sleep(10 * time.Millisecond)
		}
		m.mu.Lock()
		return 0, nil, errors.New("connection closed")
	}
	msg := m.messages[m.readIdx]
	m.readIdx++
	return 1, msg, nil
}

func (m *mockWSConn) WriteMessage(messageType int, data []byte) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.writeMu.Lock()
	m.written = append(m.written, data)
	m.writeMu.Unlock()
	return nil
}

func (m *mockWSConn) WriteControl(messageType int, data []byte, deadline time.Time) error {
	return nil
}

func (m *mockWSConn) SetReadDeadline(t time.Time) error   { return nil }
func (m *mockWSConn) SetWriteDeadline(t time.Time) error  { return nil }
func (m *mockWSConn) SetPongHandler(h func(string) error) {}

func (m *mockWSConn) Close() error {
	m.closed.Store(true)
	return nil
}

func (m *mockWSConn) getWritten() [][]byte {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	out := make([][]byte, len(m.written))
	copy(out, m.written)
	return out
}

// mockDialer is a test double for wsDialer.
type mockDialer struct {
	conn    *mockWSConn
	err     error
	calls   atomic.Int32
	lastURL string
	mu      sync.Mutex
}

func (d *mockDialer) dial(ctx context.Context, url string, header http.Header) (wsConn, *http.Response, error) {
	d.calls.Add(1)
	d.mu.Lock()
	d.lastURL = url
	d.mu.Unlock()
	if d.err != nil {
		return nil, nil, d.err
	}
	return d.conn, nil, nil
}

func makeEvent(t EventType, data map[string]any) []byte {
	e := RealtimeEvent{Type: t, Timestamp: time.Now(), Data: data}
	b, _ := json.Marshal(e)
	return b
}

func TestRealtimeClient_ReceivesEvents(t *testing.T) {
	conn := &mockWSConn{
		messages: [][]byte{
			makeEvent(EventTransaction, map[string]any{"from": "0xA", "to": "0xB", "amount": 5.0}),
			makeEvent(EventAgentJoined, map[string]any{"address": "0xC"}),
			makeEvent(EventMilestone, map[string]any{"name": "1000 transactions"}),
		},
		readErr: errors.New("EOF"),
	}
	dialer := &mockDialer{conn: conn}

	var received []RealtimeEvent
	var mu sync.Mutex

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := &RealtimeClient{
		baseURL: "http://localhost:8080",
		dialer:  dialer,
		cfg: RealtimeConfig{
			Subscription: RealtimeSubscription{AllEvents: true},
			OnEvent: func(e RealtimeEvent) {
				mu.Lock()
				received = append(received, e)
				mu.Unlock()
			},
			OnError: func(err error) bool {
				return false // don't reconnect in test
			},
			ReconnectBackoff: time.Millisecond,
			ReconnectMax:     10 * time.Millisecond,
			PingInterval:     time.Hour, // disable pings
			ReadTimeout:      5 * time.Second,
		},
		cancel: cancel,
		done:   make(chan struct{}),
	}

	// Initial connect.
	c, err := rt.dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rt.connMu.Lock()
	rt.conn = c
	rt.connMu.Unlock()

	_ = rt.sendSubscription(c, rt.cfg.Subscription)

	go rt.runLoop(ctx)

	// Wait for events to be processed.
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for events, got %d", n)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if received[0].Type != EventTransaction {
		t.Errorf("event[0].Type = %q, want %q", received[0].Type, EventTransaction)
	}
	if received[1].Type != EventAgentJoined {
		t.Errorf("event[1].Type = %q, want %q", received[1].Type, EventAgentJoined)
	}
	if received[2].Type != EventMilestone {
		t.Errorf("event[2].Type = %q, want %q", received[2].Type, EventMilestone)
	}

	if rt.eventsReceived.Load() != 3 {
		t.Errorf("eventsReceived = %d, want 3", rt.eventsReceived.Load())
	}
}

func TestRealtimeClient_Subscribe(t *testing.T) {
	conn := &mockWSConn{
		messages: nil,
		readErr:  errors.New("EOF"),
	}
	dialer := &mockDialer{conn: conn}

	rt := &RealtimeClient{
		baseURL: "http://localhost:8080",
		dialer:  dialer,
		cfg: RealtimeConfig{
			Subscription: RealtimeSubscription{AllEvents: true},
			PingInterval: time.Hour,
			ReadTimeout:  time.Second,
		},
		cancel: func() {},
		done:   make(chan struct{}),
	}
	rt.connMu.Lock()
	rt.conn = conn
	rt.connMu.Unlock()

	// Update subscription.
	err := rt.Subscribe(RealtimeSubscription{
		EventTypes: []EventType{EventTransaction},
		MinAmount:  10.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify the subscription message was sent.
	written := conn.getWritten()
	if len(written) == 0 {
		t.Fatal("expected subscription message to be written")
	}

	var sub RealtimeSubscription
	if err := json.Unmarshal(written[len(written)-1], &sub); err != nil {
		t.Fatal(err)
	}
	if sub.MinAmount != 10.0 {
		t.Errorf("MinAmount = %f, want 10.0", sub.MinAmount)
	}
	if len(sub.EventTypes) != 1 || sub.EventTypes[0] != EventTransaction {
		t.Errorf("EventTypes = %v", sub.EventTypes)
	}
}

func TestRealtimeClient_Close(t *testing.T) {
	conn := &mockWSConn{}
	rt := &RealtimeClient{
		baseURL: "http://localhost:8080",
		cfg:     RealtimeConfig{PingInterval: time.Hour, ReadTimeout: time.Second},
		cancel:  func() {},
		done:    make(chan struct{}),
	}
	close(rt.done) // simulate already stopped loop

	rt.connMu.Lock()
	rt.conn = conn
	rt.connMu.Unlock()

	err := rt.Close()
	if err != nil {
		t.Fatal(err)
	}

	if !conn.closed.Load() {
		t.Error("expected connection to be closed")
	}

	// Double close should return ErrAlreadyClosed.
	err = rt.Close()
	if !errors.Is(err, ErrAlreadyClosed) {
		t.Errorf("expected ErrAlreadyClosed, got %v", err)
	}
}

func TestRealtimeClient_SubscribeWhenClosed(t *testing.T) {
	rt := &RealtimeClient{
		cancel: func() {},
		done:   make(chan struct{}),
	}
	rt.closed.Store(true)

	err := rt.Subscribe(RealtimeSubscription{AllEvents: true})
	if !errors.Is(err, ErrAlreadyClosed) {
		t.Errorf("expected ErrAlreadyClosed, got %v", err)
	}
}

func TestRealtimeClient_Stats(t *testing.T) {
	rt := &RealtimeClient{}
	rt.eventsReceived.Store(42)
	rt.reconnects.Store(3)

	stats := rt.Stats()
	if stats.EventsReceived != 42 {
		t.Errorf("EventsReceived = %d, want 42", stats.EventsReceived)
	}
	if stats.Reconnects != 3 {
		t.Errorf("Reconnects = %d, want 3", stats.Reconnects)
	}
}

func TestRealtimeClient_wsURL(t *testing.T) {
	tests := []struct {
		baseURL string
		want    string
	}{
		{"http://localhost:8080", "ws://localhost:8080/ws"},
		{"https://api.alancoin.io", "wss://api.alancoin.io/ws"},
		{"https://api.alancoin.io/", "wss://api.alancoin.io/ws"},
		{"http://localhost:8080/", "ws://localhost:8080/ws"},
	}
	for _, tt := range tests {
		rt := &RealtimeClient{baseURL: tt.baseURL}
		got := rt.wsURL()
		if got != tt.want {
			t.Errorf("wsURL(%q) = %q, want %q", tt.baseURL, got, tt.want)
		}
	}
}

func TestRealtimeConfig_Defaults(t *testing.T) {
	cfg := RealtimeConfig{}
	cfg.defaults()

	if cfg.ReconnectBackoff != 1*time.Second {
		t.Errorf("ReconnectBackoff = %v", cfg.ReconnectBackoff)
	}
	if cfg.ReconnectMax != 30*time.Second {
		t.Errorf("ReconnectMax = %v", cfg.ReconnectMax)
	}
	if cfg.PingInterval != 30*time.Second {
		t.Errorf("PingInterval = %v", cfg.PingInterval)
	}
	if cfg.ReadTimeout != 90*time.Second {
		t.Errorf("ReadTimeout = %v", cfg.ReadTimeout)
	}
}

func TestRealtimeClient_MalformedEventSkipped(t *testing.T) {
	conn := &mockWSConn{
		messages: [][]byte{
			[]byte("not valid json{{{"),
			makeEvent(EventTransaction, map[string]any{"from": "0xA", "amount": 1.0}),
		},
		readErr: errors.New("EOF"),
	}
	dialer := &mockDialer{conn: conn}

	var received []RealtimeEvent
	var mu sync.Mutex

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := &RealtimeClient{
		baseURL: "http://localhost:8080",
		dialer:  dialer,
		cfg: RealtimeConfig{
			Subscription: RealtimeSubscription{AllEvents: true},
			OnEvent: func(e RealtimeEvent) {
				mu.Lock()
				received = append(received, e)
				mu.Unlock()
			},
			OnError:          func(err error) bool { return false },
			ReconnectBackoff: time.Millisecond,
			ReconnectMax:     10 * time.Millisecond,
			PingInterval:     time.Hour,
			ReadTimeout:      5 * time.Second,
		},
		cancel: cancel,
		done:   make(chan struct{}),
	}

	rt.connMu.Lock()
	rt.conn = conn
	rt.connMu.Unlock()

	go rt.runLoop(ctx)

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// Only the valid event should be received — malformed one skipped.
	if len(received) != 1 {
		t.Fatalf("received %d events, want 1", len(received))
	}
	if received[0].Type != EventTransaction {
		t.Errorf("type = %q", received[0].Type)
	}
}

func TestRealtimeEventTypes(t *testing.T) {
	// Verify event type constants match the server-side values.
	if EventTransaction != "transaction" {
		t.Errorf("EventTransaction = %q", EventTransaction)
	}
	if EventAgentJoined != "agent_joined" {
		t.Errorf("EventAgentJoined = %q", EventAgentJoined)
	}
	if EventMilestone != "milestone" {
		t.Errorf("EventMilestone = %q", EventMilestone)
	}
	if EventPriceAlert != "price_alert" {
		t.Errorf("EventPriceAlert = %q", EventPriceAlert)
	}
}

func TestRealtimeSubscription_JSON(t *testing.T) {
	sub := RealtimeSubscription{
		EventTypes:   []EventType{EventTransaction, EventMilestone},
		AgentAddrs:   []string{"0xABC"},
		ServiceTypes: []string{"inference"},
		MinAmount:    5.0,
	}

	data, err := json.Marshal(sub)
	if err != nil {
		t.Fatal(err)
	}

	// Verify it round-trips correctly.
	var decoded RealtimeSubscription
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AllEvents {
		t.Error("AllEvents should be false")
	}
	if len(decoded.EventTypes) != 2 {
		t.Errorf("EventTypes len = %d", len(decoded.EventTypes))
	}
	if decoded.MinAmount != 5.0 {
		t.Errorf("MinAmount = %f", decoded.MinAmount)
	}

	// Verify JSON field names match the server protocol.
	s := string(data)
	if !strings.Contains(s, `"eventTypes"`) {
		t.Errorf("missing eventTypes field in %s", s)
	}
	if !strings.Contains(s, `"agentAddrs"`) {
		t.Errorf("missing agentAddrs field in %s", s)
	}
	if !strings.Contains(s, `"minAmount"`) {
		t.Errorf("missing minAmount field in %s", s)
	}
}

func TestRealtimeClient_DialURL(t *testing.T) {
	dialer := &mockDialer{conn: &mockWSConn{}}

	rt := &RealtimeClient{
		baseURL: "https://api.alancoin.io",
		apiKey:  "ak_test",
		dialer:  dialer,
	}

	_, err := rt.dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	dialer.mu.Lock()
	url := dialer.lastURL
	dialer.mu.Unlock()

	if url != "wss://api.alancoin.io/ws" {
		t.Errorf("dial URL = %q, want wss://api.alancoin.io/ws", url)
	}
}

func TestRealtimeClient_NoDialer(t *testing.T) {
	rt := &RealtimeClient{
		baseURL: "http://localhost:8080",
		// no dialer
	}

	_, err := rt.dial(context.Background())
	if err == nil {
		t.Fatal("expected error when no dialer configured")
	}
}
