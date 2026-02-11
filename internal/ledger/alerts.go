package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/usdc"
)

// AlertConfig defines a balance alert rule.
type AlertConfig struct {
	ID         string    `json:"id"`
	AgentAddr  string    `json:"agentAddr"`
	AlertType  string    `json:"alertType"` // low_balance, large_tx, credit_high
	Threshold  string    `json:"threshold"`
	WebhookURL string    `json:"webhookUrl,omitempty"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Alert represents a triggered alert.
type Alert struct {
	ID           int64     `json:"id"`
	ConfigID     string    `json:"configId"`
	AgentAddr    string    `json:"agentAddr"`
	AlertType    string    `json:"alertType"`
	Message      string    `json:"message"`
	Metadata     string    `json:"metadata,omitempty"`
	Acknowledged bool      `json:"acknowledged"`
	CreatedAt    time.Time `json:"createdAt"`
}

// AlertStore persists alert configs and triggered alerts.
type AlertStore interface {
	GetConfigs(ctx context.Context, agentAddr string) ([]*AlertConfig, error)
	CreateConfig(ctx context.Context, config *AlertConfig) error
	DeleteConfig(ctx context.Context, configID string) error
	CreateAlert(ctx context.Context, alert *Alert) error
	GetAlerts(ctx context.Context, agentAddr string, limit int) ([]*Alert, error)
}

// AlertChecker evaluates alert rules after balance-changing operations.
type AlertChecker struct {
	store AlertStore
}

// NewAlertChecker creates a new alert checker.
func NewAlertChecker(store AlertStore) *AlertChecker {
	return &AlertChecker{store: store}
}

// Check evaluates all active alert configs for an agent. Call after balance changes.
func (c *AlertChecker) Check(ctx context.Context, agentAddr string, bal *Balance, operation string, amount string) {
	configs, err := c.store.GetConfigs(ctx, agentAddr)
	if err != nil || len(configs) == 0 {
		return
	}

	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}

		var triggered bool
		var message string

		switch cfg.AlertType {
		case "low_balance":
			threshold, ok := usdc.Parse(cfg.Threshold)
			if !ok {
				continue
			}
			avail, _ := usdc.Parse(bal.Available)
			if avail.Cmp(threshold) <= 0 {
				triggered = true
				message = fmt.Sprintf("Balance %s dropped to/below threshold %s", bal.Available, cfg.Threshold)
			}
		case "large_tx":
			threshold, ok := usdc.Parse(cfg.Threshold)
			if !ok {
				continue
			}
			txAmt, _ := usdc.Parse(amount)
			if txAmt.Cmp(threshold) >= 0 {
				triggered = true
				message = fmt.Sprintf("Transaction of %s exceeds threshold %s (operation: %s)", amount, cfg.Threshold, operation)
			}
		case "credit_high":
			// Threshold is a percentage (0-100), not a USDC amount
			thresholdPct, ok := new(big.Int).SetString(cfg.Threshold, 10)
			if !ok {
				continue
			}
			creditUsed, _ := usdc.Parse(bal.CreditUsed)
			creditLimit, _ := usdc.Parse(bal.CreditLimit)
			if creditLimit.Sign() > 0 {
				pct := new(big.Int).Mul(creditUsed, big.NewInt(100))
				pct.Div(pct, creditLimit)
				if pct.Cmp(thresholdPct) >= 0 {
					triggered = true
					message = fmt.Sprintf("Credit utilization %d%% exceeds threshold %s%%", pct.Int64(), cfg.Threshold)
				}
			}
		}

		if triggered {
			alert := &Alert{
				ConfigID:  cfg.ID,
				AgentAddr: agentAddr,
				AlertType: cfg.AlertType,
				Message:   message,
				CreatedAt: time.Now(),
			}
			_ = c.store.CreateAlert(ctx, alert)

			// Fire webhook if configured (best-effort, non-blocking)
			if cfg.WebhookURL != "" {
				go fireAlertWebhook(cfg.WebhookURL, alert)
			}
		}
	}
}

func fireAlertWebhook(webhookURL string, alert *Alert) {
	body, err := json.Marshal(alert)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err == nil {
		_ = resp.Body.Close()
	}
}

// --- PostgresAlertStore ---

// PostgresAlertStore implements AlertStore with PostgreSQL.
type PostgresAlertStore struct {
	db *sql.DB
}

// NewPostgresAlertStore creates a new PostgreSQL-backed alert store.
func NewPostgresAlertStore(db *sql.DB) *PostgresAlertStore {
	return &PostgresAlertStore{db: db}
}

func (s *PostgresAlertStore) GetConfigs(ctx context.Context, agentAddr string) ([]*AlertConfig, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_addr, alert_type, threshold, COALESCE(webhook_url, ''), enabled, created_at
		FROM balance_alert_configs WHERE agent_addr = $1 AND enabled = TRUE
	`, agentAddr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var configs []*AlertConfig
	for rows.Next() {
		c := &AlertConfig{}
		if err := rows.Scan(&c.ID, &c.AgentAddr, &c.AlertType, &c.Threshold, &c.WebhookURL, &c.Enabled, &c.CreatedAt); err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	return configs, rows.Err()
}

func (s *PostgresAlertStore) CreateConfig(ctx context.Context, config *AlertConfig) error {
	if config.ID == "" {
		config.ID = idgen.New()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO balance_alert_configs (id, agent_addr, alert_type, threshold, webhook_url, enabled, created_at)
		VALUES ($1, $2, $3, $4::NUMERIC(20,6), $5, $6, NOW())
	`, config.ID, config.AgentAddr, config.AlertType, config.Threshold, config.WebhookURL, config.Enabled)
	return err
}

func (s *PostgresAlertStore) DeleteConfig(ctx context.Context, configID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE balance_alert_configs SET enabled = FALSE WHERE id = $1
	`, configID)
	return err
}

func (s *PostgresAlertStore) CreateAlert(ctx context.Context, alert *Alert) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO balance_alerts (config_id, agent_addr, alert_type, message, metadata, acknowledged, created_at)
		VALUES ($1, $2, $3, $4, COALESCE($5::JSONB, '{}'), FALSE, NOW())
	`, alert.ConfigID, alert.AgentAddr, alert.AlertType, alert.Message, alert.Metadata)
	return err
}

func (s *PostgresAlertStore) GetAlerts(ctx context.Context, agentAddr string, limit int) ([]*Alert, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, config_id, agent_addr, alert_type, COALESCE(message, ''), COALESCE(metadata::TEXT, '{}'), acknowledged, created_at
		FROM balance_alerts WHERE agent_addr = $1
		ORDER BY created_at DESC LIMIT $2
	`, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var alerts []*Alert
	for rows.Next() {
		a := &Alert{}
		if err := rows.Scan(&a.ID, &a.ConfigID, &a.AgentAddr, &a.AlertType, &a.Message, &a.Metadata, &a.Acknowledged, &a.CreatedAt); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// --- MemoryAlertStore ---

// MemoryAlertStore implements AlertStore for demo/testing.
type MemoryAlertStore struct {
	configs []*AlertConfig
	alerts  []*Alert
	nextID  int64
	mu      sync.RWMutex
}

// NewMemoryAlertStore creates an in-memory alert store.
func NewMemoryAlertStore() *MemoryAlertStore {
	return &MemoryAlertStore{}
}

func (s *MemoryAlertStore) GetConfigs(_ context.Context, agentAddr string) ([]*AlertConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*AlertConfig
	for _, c := range s.configs {
		if c.AgentAddr == agentAddr && c.Enabled {
			cp := *c
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (s *MemoryAlertStore) CreateConfig(_ context.Context, config *AlertConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if config.ID == "" {
		config.ID = idgen.New()
	}
	cp := *config
	cp.Enabled = true
	cp.CreatedAt = time.Now()
	s.configs = append(s.configs, &cp)
	return nil
}

func (s *MemoryAlertStore) DeleteConfig(_ context.Context, configID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, c := range s.configs {
		if c.ID == configID {
			c.Enabled = false
			return nil
		}
	}
	return nil
}

func (s *MemoryAlertStore) CreateAlert(_ context.Context, alert *Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	cp := *alert
	cp.ID = s.nextID
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	s.alerts = append(s.alerts, &cp)
	return nil
}

func (s *MemoryAlertStore) GetAlerts(_ context.Context, agentAddr string, limit int) ([]*Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	var result []*Alert
	for i := len(s.alerts) - 1; i >= 0 && len(result) < limit; i-- {
		if s.alerts[i].AgentAddr == agentAddr {
			cp := *s.alerts[i]
			result = append(result, &cp)
		}
	}
	return result, nil
}

// Alerts returns all stored alerts (for testing).
func (s *MemoryAlertStore) Alerts() []*Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Alert, len(s.alerts))
	copy(result, s.alerts)
	return result
}
