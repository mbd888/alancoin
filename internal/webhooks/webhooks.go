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
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/logging"
	"github.com/mbd888/alancoin/internal/metrics"
	"github.com/mbd888/alancoin/internal/security"
	"github.com/mbd888/alancoin/internal/traces"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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

	// Session key budget/expiration alerts
	EventSessionKeyBudgetWarning EventType = "session_key.budget_warning"
	EventSessionKeyExpiring      EventType = "session_key.expiring"

	// Gateway lifecycle events
	EventGatewaySessionCreated   EventType = "gateway.session.created"
	EventGatewaySessionClosed    EventType = "gateway.session.closed"
	EventGatewayProxySuccess     EventType = "gateway.proxy.success"
	EventGatewaySettlementFailed EventType = "gateway.settlement.failed"

	// Escrow lifecycle events
	EventEscrowCreated   EventType = "escrow.created"
	EventEscrowDelivered EventType = "escrow.delivered"
	EventEscrowReleased  EventType = "escrow.released"
	EventEscrowRefunded  EventType = "escrow.refunded"
	EventEscrowDisputed  EventType = "escrow.disputed"

	// Stream lifecycle events
	EventStreamOpened  EventType = "stream.opened"
	EventStreamClosed  EventType = "stream.closed"
	EventStreamSettled EventType = "stream.settled"
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
	SuspendedUntil      *time.Time  `json:"suspendedUntil,omitempty"`
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

// URLValidator checks if a URL is safe for server-side requests.
type URLValidator func(rawURL string) error

// Dispatcher sends webhook events
type Dispatcher struct {
	store        Store
	client       *http.Client
	retry        RetryConfig
	sem          chan struct{} // concurrency limiter
	urlValidator URLValidator  // nil = use security.ValidateEndpointURL
}

const maxConcurrentWebhooks = 50

// NewDispatcher creates a new webhook dispatcher
func NewDispatcher(store Store) *Dispatcher {
	return &Dispatcher{
		store: store,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
			},
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
	ctx, span := traces.StartSpan(ctx, "webhooks.Dispatch",
		attribute.String("event_type", string(event.Type)),
	)
	defer span.End()

	subs, err := d.store.GetByEvent(ctx, event.Type)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to get subscribers: %w", err)
	}
	span.SetAttributes(attribute.Int("subscriber_count", len(subs)))

	for _, sub := range subs {
		if !sub.Active || sub.isSuspended() {
			continue
		}

		// Send async with concurrency limit; bail if context cancelled (e.g. shutdown).
		select {
		case d.sem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		go func(s *Subscription) {
			defer func() {
				<-d.sem
				if r := recover(); r != nil {
					logging.L(ctx).Error("webhook dispatch panic", "subscription", s.ID, "panic", r)
				}
			}()
			d.send(ctx, s, event)
		}(sub)
	}

	return nil
}

// DispatchToAgent sends an event to a specific agent's webhooks
func (d *Dispatcher) DispatchToAgent(ctx context.Context, agentAddr string, event *Event) error {
	ctx, span := traces.StartSpan(ctx, "webhooks.DispatchToAgent",
		attribute.String("event_type", string(event.Type)),
		attribute.String("agent", agentAddr),
	)
	defer span.End()

	subs, err := d.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to get subscriptions: %w", err)
	}

	for _, sub := range subs {
		if !sub.Active || sub.isSuspended() {
			continue
		}

		// Check if subscribed to this event type
		for _, et := range sub.Events {
			if et == event.Type {
				select {
				case d.sem <- struct{}{}:
				case <-ctx.Done():
					return ctx.Err()
				}
				go func(s *Subscription) {
					defer func() {
						<-d.sem
						if r := recover(); r != nil {
							logging.L(ctx).Error("webhook dispatch panic", "subscription", s.ID, "panic", r)
						}
					}()
					d.send(ctx, s, event)
				}(sub)
				break
			}
		}
	}

	return nil
}

func (d *Dispatcher) send(ctx context.Context, sub *Subscription, event *Event) {
	ctx, span := traces.StartSpan(ctx, "webhooks.send",
		attribute.String("subscription_id", sub.ID),
		attribute.String("url", sub.URL),
	)
	defer span.End()

	payload, err := json.Marshal(event)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "marshal failed")
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
			// Add ±25% jitter to prevent thundering herd on retries.
			halfDelay := int64(delay / 2)
			if halfDelay > 0 {
				n, err := crand.Int(crand.Reader, big.NewInt(halfDelay))
				if err == nil {
					jitter := time.Duration(n.Int64()) - delay/4
					delay += jitter
				}
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				d.updateError(ctx, sub, "context cancelled during retry")
				return
			}
		}

		// Re-validate URL + DNS on every attempt to prevent DNS rebinding SSRF.
		validate := d.urlValidator
		if validate == nil {
			validate = security.ValidateEndpointURL
		}
		if err := validate(sub.URL); err != nil {
			lastErr = fmt.Sprintf("URL validation failed: %v", err)
			break // DNS rebinding is not transient; don't retry
		}

		parsedURL, err := url.Parse(sub.URL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			lastErr = "invalid webhook URL"
			continue
		}

		req, err := http.NewRequestWithContext(ctx, "POST", parsedURL.String(), bytes.NewReader(payload))
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

		resp, err := d.client.Do(req) //nolint:gosec // URL validated above
		if err != nil {
			lastErr = fmt.Sprintf("request failed: %v", err)
			continue
		}
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			metrics.WebhookDeliveriesTotal.WithLabelValues("success").Inc()
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
	metrics.WebhookDeliveriesTotal.WithLabelValues("failed").Inc()
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
	sub.SuspendedUntil = nil
	_ = d.store.Update(ctx, sub)
}

func (d *Dispatcher) updateError(ctx context.Context, sub *Subscription, errMsg string) {
	sub.LastError = errMsg
	sub.ConsecutiveFailures++

	// Auto-deactivate after too many consecutive failures
	if d.retry.MaxFailures > 0 && sub.ConsecutiveFailures >= d.retry.MaxFailures {
		sub.Active = false
	} else {
		// Graduated suspension: temporary backoff to protect the semaphore.
		// 5+ failures → 1min, 10+ → 5min, 20+ → 30min.
		var suspend time.Duration
		switch {
		case sub.ConsecutiveFailures >= 20:
			suspend = 30 * time.Minute
		case sub.ConsecutiveFailures >= 10:
			suspend = 5 * time.Minute
		case sub.ConsecutiveFailures >= 5:
			suspend = 1 * time.Minute
		}
		if suspend > 0 {
			t := time.Now().Add(suspend)
			sub.SuspendedUntil = &t
		}
	}

	_ = d.store.Update(ctx, sub)
}

// isSuspended returns true if the subscription is temporarily paused.
func (s *Subscription) isSuspended() bool {
	return s.SuspendedUntil != nil && time.Now().Before(*s.SuspendedUntil)
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
