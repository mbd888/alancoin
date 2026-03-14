package alancoin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Sentinel errors for realtime operations.
var (
	ErrConnClosed    = errors.New("alancoin: connection closed")
	ErrAlreadyClosed = errors.New("alancoin: already closed")
)

// EventHandler is called for each real-time event received from the server.
// It runs in a dedicated goroutine — blocking it delays subsequent events.
type EventHandler func(event RealtimeEvent)

// ErrorHandler is called when a WebSocket read or reconnect error occurs.
// Returning false stops automatic reconnection.
type ErrorHandler func(err error) (shouldReconnect bool)

// RealtimeConfig configures a [RealtimeClient].
type RealtimeConfig struct {
	// Subscription filters which events are delivered.
	// A zero-value Subscription (AllEvents = false, no filters) receives nothing.
	// Set AllEvents to true or populate EventTypes/AgentAddrs to receive events.
	Subscription RealtimeSubscription

	// OnEvent is called for each incoming event.
	OnEvent EventHandler

	// OnError is called on connection errors. Return true to reconnect.
	// If nil, the client reconnects automatically on transient errors.
	OnError ErrorHandler

	// ReconnectBackoff is the initial backoff delay between reconnect attempts.
	// Defaults to 1 second. Doubles on each attempt up to ReconnectMax.
	ReconnectBackoff time.Duration

	// ReconnectMax is the maximum backoff between reconnect attempts.
	// Defaults to 30 seconds.
	ReconnectMax time.Duration

	// PingInterval is how often a keepalive ping is sent to the server.
	// Defaults to 30 seconds. The server expects pings within 60 seconds.
	PingInterval time.Duration

	// ReadTimeout is how long to wait for a message before assuming the
	// connection is dead. Defaults to 90 seconds (3 missed pings).
	ReadTimeout time.Duration
}

func (cfg *RealtimeConfig) defaults() {
	if cfg.ReconnectBackoff == 0 {
		cfg.ReconnectBackoff = 1 * time.Second
	}
	if cfg.ReconnectMax == 0 {
		cfg.ReconnectMax = 30 * time.Second
	}
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 90 * time.Second
	}
}

// RealtimeClient manages a persistent WebSocket connection to the Alancoin
// real-time event stream. It handles automatic reconnection, subscription
// management, and heartbeat pings.
//
// Usage:
//
//	rt, err := client.Realtime(ctx, alancoin.RealtimeConfig{
//	    Subscription: alancoin.RealtimeSubscription{AllEvents: true},
//	    OnEvent: func(e alancoin.RealtimeEvent) {
//	        fmt.Printf("%s: %v\n", e.Type, e.Data)
//	    },
//	})
//	if err != nil { ... }
//	defer rt.Close()
//
//	// Update subscription at any time:
//	rt.Subscribe(alancoin.RealtimeSubscription{
//	    EventTypes: []alancoin.EventType{alancoin.EventTransaction},
//	    MinAmount:  10.0,
//	})
type RealtimeClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
	cfg     RealtimeConfig
	dialer  wsDialer

	conn   wsConn
	connMu sync.Mutex

	cancel context.CancelFunc
	done   chan struct{}

	closed atomic.Bool

	// Stats
	eventsReceived atomic.Int64
	reconnects     atomic.Int64
}

// wsConn abstracts a WebSocket connection for testing.
type wsConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	SetPongHandler(h func(appData string) error)
	Close() error
}

// wsDialer abstracts WebSocket dialing for testing.
type wsDialer interface {
	dial(ctx context.Context, url string, header http.Header) (wsConn, *http.Response, error)
}

// defaultDialer uses gorilla/websocket (or falls back to a lightweight impl).
// Since the SDK has zero external dependencies (no gorilla/websocket), we use
// a pure-stdlib approach: we do not import gorilla/websocket. Instead, we
// provide a thin WebSocket layer using net/http + hijack, or the caller can
// inject a custom dialer. For production use, we recommend the gorilla-based
// dialer via WithWSDialer.
//
// The default implementation uses a raw HTTP-based approach that works for
// simple text-frame WebSocket communication.
type stdlibDialer struct {
	httpClient *http.Client
}

// Realtime opens a persistent WebSocket connection to the real-time event
// stream and returns a [RealtimeClient] that delivers events to the configured
// handler. The connection auto-reconnects on transient failures.
//
// The returned client must be closed when no longer needed.
func (c *Client) Realtime(ctx context.Context, cfg RealtimeConfig) (*RealtimeClient, error) {
	cfg.defaults()

	connCtx, cancel := context.WithCancel(ctx)

	var d wsDialer
	if c.wsDialer != nil {
		d = &wsDialerAdapter{d: c.wsDialer}
	}

	rt := &RealtimeClient{
		baseURL: c.baseURL,
		apiKey:  c.apiKey,
		http:    c.httpClient,
		cfg:     cfg,
		dialer:  d,
		cancel:  cancel,
		done:    make(chan struct{}),
	}

	// Attempt the initial connection.
	conn, err := rt.dial(connCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("alancoin: realtime connect: %w", err)
	}
	rt.connMu.Lock()
	rt.conn = conn
	rt.connMu.Unlock()

	// Send initial subscription.
	if err := rt.sendSubscription(conn, cfg.Subscription); err != nil {
		conn.Close()
		cancel()
		return nil, fmt.Errorf("alancoin: send subscription: %w", err)
	}

	// Start the read loop and keepalive pinger.
	go rt.runLoop(connCtx)

	return rt, nil
}

// Subscribe updates the event filter on a live connection. This takes effect
// immediately — the server will start applying the new filters to subsequent events.
func (rt *RealtimeClient) Subscribe(sub RealtimeSubscription) error {
	if rt.closed.Load() {
		return ErrAlreadyClosed
	}
	rt.cfg.Subscription = sub
	rt.connMu.Lock()
	conn := rt.conn
	rt.connMu.Unlock()
	if conn == nil {
		return ErrConnClosed
	}
	return rt.sendSubscription(conn, sub)
}

// Close terminates the WebSocket connection and stops all background goroutines.
func (rt *RealtimeClient) Close() error {
	if !rt.closed.CompareAndSwap(false, true) {
		return ErrAlreadyClosed
	}
	rt.cancel()
	<-rt.done // wait for runLoop to exit
	rt.connMu.Lock()
	conn := rt.conn
	rt.conn = nil
	rt.connMu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// Stats returns cumulative statistics for this connection.
func (rt *RealtimeClient) Stats() RealtimeStats {
	return RealtimeStats{
		EventsReceived: rt.eventsReceived.Load(),
		Reconnects:     rt.reconnects.Load(),
	}
}

// RealtimeStats holds connection-level statistics.
type RealtimeStats struct {
	EventsReceived int64
	Reconnects     int64
}

// runLoop is the main background loop that reads events and handles reconnection.
func (rt *RealtimeClient) runLoop(ctx context.Context) {
	defer close(rt.done)

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		rt.connMu.Lock()
		conn := rt.conn
		rt.connMu.Unlock()
		if conn == nil {
			return
		}

		// Start a keepalive pinger in parallel.
		pingDone := make(chan struct{})
		go rt.pingLoop(ctx, conn, pingDone)

		// Read loop — blocks until error.
		readErr := rt.readLoop(ctx, conn)

		// Stop the pinger.
		close(pingDone)

		if ctx.Err() != nil {
			return // shutting down
		}

		// Connection lost — decide whether to reconnect.
		shouldReconnect := true
		if rt.cfg.OnError != nil {
			shouldReconnect = rt.cfg.OnError(readErr)
		}
		if !shouldReconnect || rt.closed.Load() {
			return
		}

		// Reconnect with exponential backoff.
		conn.Close()
		newConn, err := rt.reconnect(ctx)
		if err != nil {
			return // context cancelled or permanent failure
		}
		rt.connMu.Lock()
		rt.conn = newConn
		rt.connMu.Unlock()
		rt.reconnects.Add(1)
	}
}

// readLoop reads messages from the WebSocket until an error occurs.
func (rt *RealtimeClient) readLoop(ctx context.Context, conn wsConn) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_ = conn.SetReadDeadline(time.Now().Add(rt.cfg.ReadTimeout))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var event RealtimeEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			continue // skip malformed events
		}

		rt.eventsReceived.Add(1)
		if rt.cfg.OnEvent != nil {
			rt.cfg.OnEvent(event)
		}
	}
}

// pingLoop sends periodic pings to keep the connection alive.
func (rt *RealtimeClient) pingLoop(ctx context.Context, conn wsConn, done <-chan struct{}) {
	ticker := time.NewTicker(rt.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			deadline := time.Now().Add(10 * time.Second)
			// WriteControl with PingMessage (opcode 9).
			if err := conn.WriteControl(9, nil, deadline); err != nil {
				return
			}
		}
	}
}

// reconnect attempts to re-establish the WebSocket connection with backoff.
func (rt *RealtimeClient) reconnect(ctx context.Context) (wsConn, error) {
	delay := rt.cfg.ReconnectBackoff
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}

		conn, err := rt.dial(ctx)
		if err != nil {
			if delay < rt.cfg.ReconnectMax {
				delay *= 2
				if delay > rt.cfg.ReconnectMax {
					delay = rt.cfg.ReconnectMax
				}
			}
			continue
		}

		// Re-send subscription on the new connection.
		if err := rt.sendSubscription(conn, rt.cfg.Subscription); err != nil {
			conn.Close()
			continue
		}

		return conn, nil
	}
}

// dial establishes a new WebSocket connection.
func (rt *RealtimeClient) dial(ctx context.Context) (wsConn, error) {
	if rt.dialer != nil {
		header := http.Header{}
		if rt.apiKey != "" {
			header.Set("X-API-Key", rt.apiKey)
		}
		header.Set("User-Agent", userAgent)
		conn, _, err := rt.dialer.dial(ctx, rt.wsURL(), header)
		return conn, err
	}
	return nil, errors.New("alancoin: no WebSocket dialer configured; use WithWSDialer")
}

// wsURL converts the HTTP base URL to a WebSocket URL.
func (rt *RealtimeClient) wsURL() string {
	u := strings.TrimRight(rt.baseURL, "/")
	u = strings.Replace(u, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	return u + "/ws"
}

// sendSubscription sends a subscription message over the WebSocket.
func (rt *RealtimeClient) sendSubscription(conn wsConn, sub RealtimeSubscription) error {
	data, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(1, data) // TextMessage = 1
}

// dialer is the WebSocket dialer. Nil until set via WithWSDialer on the Client.
// We store it on the RealtimeClient to avoid adding a dependency.
var _ wsDialer = (*stdlibDialer)(nil)

// WithWSDialer configures a custom WebSocket dialer on the client. This is
// necessary because the SDK has no external WebSocket dependencies by default.
//
// For production use with gorilla/websocket:
//
//	import "github.com/gorilla/websocket"
//
//	dialer := alancoin.GorillaDialer(websocket.DefaultDialer)
//	client := alancoin.NewClient(url, alancoin.WithWSDialer(dialer))
func WithWSDialer(d WSDialer) Option {
	return func(c *Client) {
		c.wsDialer = d
	}
}

// WSDialer is the public interface for WebSocket dialers.
type WSDialer interface {
	DialContext(ctx context.Context, url string, header http.Header) (WSConn, *http.Response, error)
}

// WSConn is the public interface for WebSocket connections.
type WSConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	SetPongHandler(h func(appData string) error)
	Close() error
}

// wsDialerAdapter wraps a public WSDialer into the internal interface.
type wsDialerAdapter struct {
	d WSDialer
}

func (a *wsDialerAdapter) dial(ctx context.Context, url string, header http.Header) (wsConn, *http.Response, error) {
	conn, resp, err := a.d.DialContext(ctx, url, header)
	if err != nil {
		return nil, resp, err
	}
	return conn, resp, nil
}

// stdlibDialer.dial is a stub — the stdlib approach requires gorilla or nhooyr
// to actually work. We provide the interface so users can inject their own.
func (d *stdlibDialer) dial(ctx context.Context, url string, header http.Header) (wsConn, *http.Response, error) {
	return nil, nil, errors.New("alancoin: stdlib WebSocket dialer not implemented; use WithWSDialer with gorilla/websocket")
}
