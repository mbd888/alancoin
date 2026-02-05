// Package realtime provides WebSocket streaming for live network activity.
//
// This is what makes the network feel ALIVE. Instead of polling,
// agents subscribe to real-time events:
// - Transactions as they happen
// - Commentary as it's posted
// - Predictions being made and resolved
// - Agent milestones
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

// EventType for real-time events
type EventType string

const (
	EventTransaction        EventType = "transaction"
	EventComment            EventType = "comment"
	EventPrediction         EventType = "prediction"
	EventPredictionResolved EventType = "prediction_resolved"
	EventAgentJoined        EventType = "agent_joined"
	EventMilestone          EventType = "milestone"
	EventPriceAlert         EventType = "price_alert"
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
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	sub       Subscription
	agentAddr string // If authenticated
}

// Hub manages all WebSocket connections
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan *Event
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
	logger     *slog.Logger

	// Stats
	totalEvents  int64
	totalClients int64
	peakClients  int64
}

// NewHub creates a new WebSocket hub
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan *Event, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		logger:     logger,
	}
}

// Run starts the hub's main loop
func (h *Hub) Run(ctx context.Context) {
	h.logger.Info("realtime hub started")

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("realtime hub shutting down")
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.totalClients++
			if int64(len(h.clients)) > h.peakClients {
				h.peakClients = int64(len(h.clients))
			}
			h.mu.Unlock()
			h.logger.Info("client connected", "total", len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			h.logger.Info("client disconnected", "total", len(h.clients))

		case event := <-h.broadcast:
			h.totalEvents++
			h.mu.RLock()
			for client := range h.clients {
				if h.shouldSend(client, event) {
					select {
					case client.send <- h.serialize(event):
					default:
						// Client too slow, disconnect
						close(client.send)
						delete(h.clients, client)
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

// shouldSend checks if event matches client's subscription
func (h *Hub) shouldSend(client *Client, event *Event) bool {
	sub := client.sub

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

// BroadcastComment sends a comment event
func (h *Hub) BroadcastComment(comment map[string]interface{}) {
	h.Broadcast(&Event{
		Type:      EventComment,
		Timestamp: time.Now(),
		Data:      comment,
	})
}

// Stats returns hub statistics
func (h *Hub) Stats() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return map[string]interface{}{
		"connectedClients": len(h.clients),
		"totalEvents":      h.totalEvents,
		"totalClients":     h.totalClients,
		"peakClients":      h.peakClients,
	}
}

// HandleWebSocket upgrades HTTP to WebSocket
func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", "error", err)
		return
	}

	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, 256),
		sub:  Subscription{AllEvents: true}, // Default: all events
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
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512 * 1024)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		// Parse subscription update
		var sub Subscription
		if err := json.Unmarshal(message, &sub); err == nil {
			c.sub = sub
		}
	}
}

// writePump writes messages to WebSocket
func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
