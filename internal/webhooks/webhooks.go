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
	ID                  string      `json:"id"`
	AgentAddr           string      `json:"agentAddr"`
	URL                 string      `json:"url"`
	Secret              string      `json:"-"` // Used for HMAC signing
	Events              []EventType `json:"events"`
	Active              bool        `json:"active"`
	CreatedAt           time.Time   `json:"createdAt"`
	LastSuccess         *time.Time  `json:"lastSuccess,omitempty"`
	LastError           string      `json:"lastError,omitempty"`
	ConsecutiveFailures int         `json:"consecutiveFailures"`
}

// RetryConfig controls exponential backoff for webhook delivery
type RetryConfig struct {
	MaxAttempts int           // Total attempts including initial (default: 5)
	BaseDelay   time.Duration // Initial retry delay (default: 1s)
	MaxDelay    time.Duration // Cap on backoff delay (default: 60s)
	MaxFailures int           // Consecutive failures before deactivation (default: 50)
}

// DefaultRetryConfig returns sensible retry defaults
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Second,
		MaxDelay:    60 * time.Second,
		MaxFailures: 50,
	}
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
	retry  RetryConfig
	sem    chan struct{} // concurrency limiter
}

const maxConcurrentWebhooks = 50

// NewDispatcher creates a new webhook dispatcher
func NewDispatcher(store Store) *Dispatcher {
	return &Dispatcher{
		store: store,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		retry: DefaultRetryConfig(),
		sem:   make(chan struct{}, maxConcurrentWebhooks),
	}
}

// NewDispatcherWithRetry creates a dispatcher with custom retry config
func NewDispatcherWithRetry(store Store, retryCfg RetryConfig) *Dispatcher {
	return &Dispatcher{
		store: store,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		retry: retryCfg,
		sem:   make(chan struct{}, maxConcurrentWebhooks),
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

		// Send async with concurrency limit
		d.sem <- struct{}{}
		go func(s *Subscription) {
			defer func() { <-d.sem }()
			d.send(ctx, s, event)
		}(sub)
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
				d.sem <- struct{}{}
				go func(s *Subscription) {
					defer func() { <-d.sem }()
					d.send(ctx, s, event)
				}(sub)
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

	var lastErr string
	for attempt := 0; attempt < d.retry.MaxAttempts; attempt++ {
		// Wait before retry (no wait on first attempt)
		if attempt > 0 {
			delay := d.retry.BaseDelay * (1 << (attempt - 1)) // exponential: 1s, 2s, 4s, 8s...
			if delay > d.retry.MaxDelay {
				delay = d.retry.MaxDelay
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				d.updateError(ctx, sub, "context cancelled during retry")
				return
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", sub.URL, bytes.NewReader(payload))
		if err != nil {
			lastErr = "failed to create request"
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Alancoin-Event", string(event.Type))
		req.Header.Set("X-Alancoin-Timestamp", fmt.Sprintf("%d", event.Timestamp.Unix()))
		req.Header.Set("X-Alancoin-Delivery-Attempt", fmt.Sprintf("%d", attempt+1))

		if sub.Secret != "" {
			signature := d.sign(payload, sub.Secret)
			req.Header.Set("X-Alancoin-Signature", signature)
		}

		resp, err := d.client.Do(req)
		if err != nil {
			lastErr = fmt.Sprintf("request failed: %v", err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			d.updateSuccess(ctx, sub)
			return
		}

		lastErr = fmt.Sprintf("status %d", resp.StatusCode)

		// Don't retry on 4xx (client error) — only retry on 5xx / network failures
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			break
		}
	}

	// All attempts exhausted
	d.updateError(ctx, sub, lastErr)
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
	sub.ConsecutiveFailures = 0
	_ = d.store.Update(ctx, sub)
}

func (d *Dispatcher) updateError(ctx context.Context, sub *Subscription, errMsg string) {
	sub.LastError = errMsg
	sub.ConsecutiveFailures++

	// Auto-deactivate after too many consecutive failures
	if d.retry.MaxFailures > 0 && sub.ConsecutiveFailures >= d.retry.MaxFailures {
		sub.Active = false
	}

	_ = d.store.Update(ctx, sub)
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
