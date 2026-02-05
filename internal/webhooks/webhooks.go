// Package webhooks provides event notifications to external services.
//
// Agents can register webhook URLs to receive notifications about:
// - Incoming payments
// - Session key transactions
// - Balance changes
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// EventType represents the type of webhook event
type EventType string

const (
	EventPaymentReceived   EventType = "payment.received"
	EventPaymentSent       EventType = "payment.sent"
	EventSessionKeyUsed    EventType = "session_key.used"
	EventSessionKeyCreated EventType = "session_key.created"
	EventSessionKeyRevoked EventType = "session_key.revoked"
	EventBalanceDeposit    EventType = "balance.deposit"
	EventBalanceWithdraw   EventType = "balance.withdraw"
)

// Event represents a webhook event
type Event struct {
	ID        string                 `json:"id"`
	Type      EventType              `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
}

// Subscription represents a webhook subscription
type Subscription struct {
	ID          string      `json:"id"`
	AgentAddr   string      `json:"agentAddr"`
	URL         string      `json:"url"`
	Secret      string      `json:"-"` // Used for HMAC signing
	Events      []EventType `json:"events"`
	Active      bool        `json:"active"`
	CreatedAt   time.Time   `json:"createdAt"`
	LastSuccess *time.Time  `json:"lastSuccess,omitempty"`
	LastError   string      `json:"lastError,omitempty"`
}

// Store persists webhook subscriptions
type Store interface {
	Create(ctx context.Context, sub *Subscription) error
	Get(ctx context.Context, id string) (*Subscription, error)
	GetByAgent(ctx context.Context, agentAddr string) ([]*Subscription, error)
	GetByEvent(ctx context.Context, eventType EventType) ([]*Subscription, error)
	Update(ctx context.Context, sub *Subscription) error
	Delete(ctx context.Context, id string) error
}

// Dispatcher sends webhook events
type Dispatcher struct {
	store  Store
	client *http.Client
	mu     sync.RWMutex
}

// NewDispatcher creates a new webhook dispatcher
func NewDispatcher(store Store) *Dispatcher {
	return &Dispatcher{
		store: store,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Dispatch sends an event to all relevant subscribers
func (d *Dispatcher) Dispatch(ctx context.Context, event *Event) error {
	subs, err := d.store.GetByEvent(ctx, event.Type)
	if err != nil {
		return fmt.Errorf("failed to get subscribers: %w", err)
	}

	for _, sub := range subs {
		if !sub.Active {
			continue
		}

		// Send async to avoid blocking
		go d.send(ctx, sub, event)
	}

	return nil
}

// DispatchToAgent sends an event to a specific agent's webhooks
func (d *Dispatcher) DispatchToAgent(ctx context.Context, agentAddr string, event *Event) error {
	subs, err := d.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		return fmt.Errorf("failed to get subscriptions: %w", err)
	}

	for _, sub := range subs {
		if !sub.Active {
			continue
		}

		// Check if subscribed to this event type
		for _, et := range sub.Events {
			if et == event.Type {
				go d.send(ctx, sub, event)
				break
			}
		}
	}

	return nil
}

func (d *Dispatcher) send(ctx context.Context, sub *Subscription, event *Event) {
	payload, err := json.Marshal(event)
	if err != nil {
		d.updateError(ctx, sub, "failed to marshal event")
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", sub.URL, bytes.NewReader(payload))
	if err != nil {
		d.updateError(ctx, sub, "failed to create request")
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Alancoin-Event", string(event.Type))
	req.Header.Set("X-Alancoin-Timestamp", fmt.Sprintf("%d", event.Timestamp.Unix()))

	// Sign the payload if secret is set
	if sub.Secret != "" {
		signature := d.sign(payload, sub.Secret)
		req.Header.Set("X-Alancoin-Signature", signature)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		d.updateError(ctx, sub, fmt.Sprintf("request failed: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		d.updateSuccess(ctx, sub)
	} else {
		d.updateError(ctx, sub, fmt.Sprintf("status %d", resp.StatusCode))
	}
}

func (d *Dispatcher) sign(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

func (d *Dispatcher) updateSuccess(ctx context.Context, sub *Subscription) {
	now := time.Now()
	sub.LastSuccess = &now
	sub.LastError = ""
	d.store.Update(ctx, sub)
}

func (d *Dispatcher) updateError(ctx context.Context, sub *Subscription, errMsg string) {
	sub.LastError = errMsg
	d.store.Update(ctx, sub)
}

// MemoryStore is an in-memory implementation for testing
type MemoryStore struct {
	subs map[string]*Subscription
	mu   sync.RWMutex
}

// NewMemoryStore creates a new in-memory store
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		subs: make(map[string]*Subscription),
	}
}

func (m *MemoryStore) Create(ctx context.Context, sub *Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[sub.ID] = sub
	return nil
}

func (m *MemoryStore) Get(ctx context.Context, id string) (*Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if sub, ok := m.subs[id]; ok {
		return sub, nil
	}
	return nil, fmt.Errorf("subscription not found")
}

func (m *MemoryStore) GetByAgent(ctx context.Context, agentAddr string) ([]*Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Subscription
	for _, sub := range m.subs {
		if sub.AgentAddr == agentAddr {
			result = append(result, sub)
		}
	}
	return result, nil
}

func (m *MemoryStore) GetByEvent(ctx context.Context, eventType EventType) ([]*Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Subscription
	for _, sub := range m.subs {
		for _, et := range sub.Events {
			if et == eventType {
				result = append(result, sub)
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) Update(ctx context.Context, sub *Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[sub.ID] = sub
	return nil
}

func (m *MemoryStore) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, id)
	return nil
}
