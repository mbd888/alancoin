// Package realtime provides WebSocket streaming for live network activity.
//
// This is what makes the network feel ALIVE. Instead of polling,
// agents subscribe to real-time events:
// - Transactions as they happen
// - Agent milestones
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mbd888/alancoin/internal/metrics"
)

// normalCloseCodes are WebSocket close codes that indicate an expected disconnect.
var normalCloseCodes = []int{
	websocket.CloseNormalClosure,
	websocket.CloseGoingAway,
	websocket.CloseNoStatusReceived,
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // Allow non-browser clients
		}
		// Allow same-host connections
		host := r.Host
		return origin == "http://"+host || origin == "https://"+host
	},
}

// EventType for real-time events
type EventType string

const (
	EventTransaction EventType = "transaction"
	EventAgentJoined EventType = "agent_joined"
	EventMilestone   EventType = "milestone"
	EventPriceAlert  EventType = "price_alert"
	EventCoalition   EventType = "coalition"

	// Gateway session lifecycle events
	EventSessionCreated  EventType = "session_created"
	EventSessionClosed   EventType = "session_closed"
	EventProxySettlement EventType = "proxy_settlement"

	// Escrow lifecycle events
	EventEscrowCreated   EventType = "escrow_created"
	EventEscrowDelivered EventType = "escrow_delivered"
	EventEscrowConfirmed EventType = "escrow_confirmed"
	EventEscrowDisputed  EventType = "escrow_disputed"

	// Stream lifecycle events
	EventStreamOpened EventType = "stream_opened"
	EventStreamClosed EventType = "stream_closed"
)

// Event represents a real-time event
type Event struct {
	Type      EventType   `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// Subscription filters for a client
type Subscription struct {
	AllEvents    bool        `json:"allEvents"`
	EventTypes   []EventType `json:"eventTypes"`
	AgentAddrs   []string    `json:"agentAddrs"`   // Watch specific agents
	ServiceTypes []string    `json:"serviceTypes"` // Watch specific services
	MinAmount    float64     `json:"minAmount"`    // Only txs above this
}

// Client represents a WebSocket connection
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	mu   sync.RWMutex
	sub  Subscription
	ip   string // remote IP for per-IP tracking
}

// MaxClients is the maximum number of concurrent WebSocket connections.
const MaxClients = 10000

// Hub manages all WebSocket connections
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan *Event
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
	logger     *slog.Logger
	done       chan struct{} // closed when Run exits; prevents upgrade race
	maxClients int
	connSem    chan struct{} // buffered semaphore; capacity = maxClients

	// Per-IP connection tracking to prevent single-source DoS
	ipConns  map[string]int
	maxPerIP int

	// Stats
	totalEvents  atomic.Int64
	totalClients atomic.Int64
	peakClients  atomic.Int64
}

// NewHub creates a new WebSocket hub
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan *Event, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		ipConns:    make(map[string]int),
		maxPerIP:   100, // Max 100 WS connections from a single IP
		logger:     logger,
		done:       make(chan struct{}),
		maxClients: MaxClients,
		connSem:    make(chan struct{}, MaxClients),
	}
}

// Run starts the hub's main loop
func (h *Hub) Run(ctx context.Context) {
	h.logger.Info("realtime hub started")
	defer close(h.done)

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("realtime hub shutting down, closing client connections")
			h.mu.Lock()
			for client := range h.clients {
				close(client.send) // writePump sends CloseMessage on closed channel
				delete(h.clients, client)
			}
			h.mu.Unlock()
			metrics.ActiveWebSocketClients.Set(0)
			h.logger.Info("realtime hub stopped")
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.totalClients.Add(1)
			if current := int64(len(h.clients)); current > h.peakClients.Load() {
				h.peakClients.Store(current)
			}
			n := len(h.clients)
			h.mu.Unlock()
			metrics.ActiveWebSocketClients.Set(float64(n))
			h.logger.Info("client connected", "total", n)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				if client.ip != "" {
					h.ipConns[client.ip]--
					if h.ipConns[client.ip] <= 0 {
						delete(h.ipConns, client.ip)
					}
				}
				select {
				case <-h.connSem: // release semaphore slot
				default:
				}
			}
			n := len(h.clients)
			h.mu.Unlock()
			metrics.ActiveWebSocketClients.Set(float64(n))
			h.logger.Info("client disconnected", "total", n)

		case event := <-h.broadcast:
			h.totalEvents.Add(1)
			h.mu.RLock()
			var slow []*Client
			for client := range h.clients {
				if h.shouldSend(client, event) {
					select {
					case client.send <- h.serialize(event):
					default:
						slow = append(slow, client)
					}
				}
			}
			h.mu.RUnlock()
			// Remove slow clients under write lock
			if len(slow) > 0 {
				h.mu.Lock()
				for _, client := range slow {
					if _, ok := h.clients[client]; ok {
						close(client.send)
						delete(h.clients, client)
					}
				}
				h.mu.Unlock()
			}
		}
	}
}

// shouldSend checks if event matches client's subscription
func (h *Hub) shouldSend(client *Client, event *Event) bool {
	client.mu.RLock()
	sub := client.sub
	client.mu.RUnlock()

	// All events subscribed
	if sub.AllEvents {
		return true
	}

	// Check event type filter
	if len(sub.EventTypes) > 0 {
		matched := false
		for _, t := range sub.EventTypes {
			if t == event.Type {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check agent filter
	if len(sub.AgentAddrs) > 0 {
		// Extract agent addresses from event data
		data, ok := event.Data.(map[string]interface{})
		if ok {
			from, _ := data["from"].(string)
			to, _ := data["to"].(string)
			author, _ := data["authorAddr"].(string)

			matched := false
			for _, addr := range sub.AgentAddrs {
				if addr == from || addr == to || addr == author {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}

	// Check minimum amount
	if sub.MinAmount > 0 && event.Type == EventTransaction {
		data, ok := event.Data.(map[string]interface{})
		if ok {
			if amount, ok := data["amount"].(float64); ok {
				if amount < sub.MinAmount {
					return false
				}
			}
		}
	}

	return true
}

func (h *Hub) serialize(event *Event) []byte {
	data, _ := json.Marshal(event)
	return data
}

// Broadcast sends an event to all matching clients
func (h *Hub) Broadcast(event *Event) {
	select {
	case h.broadcast <- event:
	default:
		h.logger.Warn("broadcast channel full, dropping event")
	}
}

// BroadcastTransaction sends a transaction event
func (h *Hub) BroadcastTransaction(tx map[string]interface{}) {
	h.Broadcast(&Event{
		Type:      EventTransaction,
		Timestamp: time.Now(),
		Data:      tx,
	})
}

// BroadcastCoalition sends a coalition lifecycle event.
func (h *Hub) BroadcastCoalition(data map[string]interface{}) {
	h.Broadcast(&Event{
		Type:      EventCoalition,
		Timestamp: time.Now(),
		Data:      data,
	})
}

// BroadcastSessionEvent sends a gateway session lifecycle event.
func (h *Hub) BroadcastSessionEvent(eventType EventType, data map[string]interface{}) {
	h.Broadcast(&Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	})
}

// BroadcastEscrowEvent sends an escrow lifecycle event.
func (h *Hub) BroadcastEscrowEvent(eventType EventType, data map[string]interface{}) {
	h.Broadcast(&Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	})
}

// BroadcastStreamEvent sends a stream lifecycle event.
func (h *Hub) BroadcastStreamEvent(eventType EventType, data map[string]interface{}) {
	h.Broadcast(&Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	})
}

// Stats returns hub statistics
func (h *Hub) Stats() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return map[string]interface{}{
		"connectedClients": len(h.clients),
		"totalEvents":      h.totalEvents.Load(),
		"totalClients":     h.totalClients.Load(),
		"peakClients":      h.peakClients.Load(),
	}
}

// HandleWebSocket upgrades HTTP to WebSocket
func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Reject upgrades after the hub has stopped to prevent orphaned connections.
	select {
	case <-h.done:
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	default:
	}

	// Enforce connection limit: reserve a slot via semaphore before upgrading.
	// This is atomic — no TOCTOU between the check and the actual upgrade.
	select {
	case h.connSem <- struct{}{}:
		// slot reserved
	default:
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	// Per-IP connection limit
	ip := r.RemoteAddr
	if idx := len(ip) - 1; idx >= 0 {
		// Strip port
		for i := idx; i >= 0; i-- {
			if ip[i] == ':' {
				ip = ip[:i]
				break
			}
		}
	}
	h.mu.Lock()
	if h.ipConns[ip] >= h.maxPerIP {
		h.mu.Unlock()
		<-h.connSem
		http.Error(w, "too many connections from this IP", http.StatusTooManyRequests)
		return
	}
	h.ipConns[ip]++
	h.mu.Unlock()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		<-h.connSem // release reserved slot
		h.mu.Lock()
		h.ipConns[ip]--
		h.mu.Unlock()
		h.logger.Error("websocket upgrade failed", "error", err)
		return
	}

	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, 256),
		sub:  Subscription{AllEvents: true}, // Default: all events
		ip:   ip,
	}

	h.register <- client

	// Start goroutines for reading and writing
	go client.writePump()
	go client.readPump()
}

// readPump reads messages from WebSocket (subscriptions, pings)
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(512 * 1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, normalCloseCodes...) {
				c.hub.logger.Warn("websocket read error", "error", err)
			}
			break
		}

		// Parse subscription update — cap filter array sizes to prevent DoS.
		var sub Subscription
		if err := json.Unmarshal(message, &sub); err == nil {
			const maxFilterEntries = 100
			if len(sub.EventTypes) > maxFilterEntries {
				sub.EventTypes = sub.EventTypes[:maxFilterEntries]
			}
			if len(sub.AgentAddrs) > maxFilterEntries {
				sub.AgentAddrs = sub.AgentAddrs[:maxFilterEntries]
			}
			if len(sub.ServiceTypes) > maxFilterEntries {
				sub.ServiceTypes = sub.ServiceTypes[:maxFilterEntries]
			}
			c.mu.Lock()
			c.sub = sub
			c.mu.Unlock()
		}
	}
}

// writePump writes messages to WebSocket
func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.hub.logger.Warn("websocket write error", "error", err)
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.hub.logger.Debug("websocket ping failed", "error", err)
				return
			}
		}
	}
}
