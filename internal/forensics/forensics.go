// Package forensics implements agent spend anomaly detection and security forensics.
//
// The forensics engine establishes behavioral payment baselines for each agent
// and scores every transaction against that baseline in real time. Anomalies
// trigger graduated responses: soft alert -> hard circuit break -> escrow freeze.
//
// Detection signals:
//   - Spend velocity deviation (3-sigma from rolling mean)
//   - New counterparty anomaly (paying agents never seen before)
//   - Service type deviation (using service types outside normal pattern)
//   - Amount deviation (individual transaction significantly above normal)
//   - Time pattern anomaly (activity outside normal hours)
//
// The payment graph IS the behavioral baseline — this is data no SIEM or
// observability tool has. LiteLLM has 5 documented budget bypass bugs.
// This engine catches what budget counters miss.
//
// Based on: Obsidian Security AI detection, Stellar Cyber agentic threat landscape,
// x402 V2 dynamic payTo attack vector.
package forensics

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/metrics"
)

var (
	ErrAgentNotTracked = errors.New("forensics: agent has no baseline yet")
	ErrAlertNotFound   = errors.New("forensics: alert not found")
)

// Severity levels for anomaly alerts.
type AlertSeverity string

const (
	SeverityInfo     AlertSeverity = "info"     // Logged, no action
	SeverityWarning  AlertSeverity = "warning"  // Alert sent, no enforcement
	SeverityCritical AlertSeverity = "critical" // Circuit breaker triggered
)

// AlertType categorizes the kind of anomaly detected.
type AlertType string

const (
	AlertVelocitySpike    AlertType = "velocity_spike"    // Spend rate > 3σ above mean
	AlertNewCounterparty  AlertType = "new_counterparty"  // Paying a never-seen agent
	AlertServiceDeviation AlertType = "service_deviation" // Using unusual service type
	AlertAmountAnomaly    AlertType = "amount_anomaly"    // Single tx > 3σ above mean amount
	AlertTimeAnomaly      AlertType = "time_anomaly"      // Activity outside normal window
	AlertBurstPattern     AlertType = "burst_pattern"     // Many transactions in short window
)

// Alert represents a detected anomaly.
type Alert struct {
	ID           string        `json:"id"`
	AgentAddr    string        `json:"agentAddr"`
	Type         AlertType     `json:"type"`
	Severity     AlertSeverity `json:"severity"`
	Message      string        `json:"message"`
	Score        float64       `json:"score"`    // 0-100 anomaly score
	Baseline     float64       `json:"baseline"` // Expected value
	Actual       float64       `json:"actual"`   // Observed value
	Sigma        float64       `json:"sigma"`    // Standard deviations from mean
	DetectedAt   time.Time     `json:"detectedAt"`
	Acknowledged bool          `json:"acknowledged"`
}

// SpendEvent is a transaction event fed into the forensics engine.
type SpendEvent struct {
	AgentAddr    string    `json:"agentAddr"`
	Counterparty string    `json:"counterparty"`
	Amount       float64   `json:"amount"` // USDC as float for stats
	ServiceType  string    `json:"serviceType"`
	Timestamp    time.Time `json:"timestamp"`
}

// Baseline holds statistical profile of an agent's normal behavior.
type Baseline struct {
	AgentAddr           string         `json:"agentAddr"`
	TxCount             int            `json:"txCount"`
	MeanAmount          float64        `json:"meanAmount"`
	StdDevAmount        float64        `json:"stdDevAmount"`
	MeanVelocity        float64        `json:"meanVelocity"` // USDC per hour
	StdDevVelocity      float64        `json:"stdDevVelocity"`
	KnownCounterparties map[string]int `json:"knownCounterparties"` // addr -> tx count
	KnownServices       map[string]int `json:"knownServices"`       // service type -> tx count
	ActiveHours         [24]int        `json:"activeHours"`         // hourly distribution
	LastUpdated         time.Time      `json:"lastUpdated"`
	m2Amount            float64        // Welford's running sum of squared deviations
	// Rolling window for velocity calculation
	recentAmounts    []float64
	recentTimestamps []time.Time
}

// Store persists baselines and alerts.
type Store interface {
	GetBaseline(ctx context.Context, agentAddr string) (*Baseline, error)
	SaveBaseline(ctx context.Context, b *Baseline) error
	SaveAlert(ctx context.Context, a *Alert) error
	ListAlerts(ctx context.Context, agentAddr string, limit int) ([]*Alert, error)
	ListAllAlerts(ctx context.Context, severity AlertSeverity, limit int) ([]*Alert, error)
	AcknowledgeAlert(ctx context.Context, alertID string) error
}

// Config tunes the anomaly detection thresholds.
type Config struct {
	SigmaThreshold   float64       // Standard deviations for anomaly (default: 3.0)
	MinTxForBaseline int           // Minimum transactions before scoring (default: 10)
	VelocityWindow   time.Duration // Rolling window for velocity (default: 1h)
	BurstThreshold   int           // Max tx in burst window (default: 50)
	BurstWindow      time.Duration // Burst detection window (default: 5m)
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		SigmaThreshold:   3.0,
		MinTxForBaseline: 10,
		VelocityWindow:   time.Hour,
		BurstThreshold:   50,
		BurstWindow:      5 * time.Minute,
	}
}

// AgentPauser can suspend an agent's ability to transact when a critical anomaly is detected.
type AgentPauser interface {
	PauseAgent(ctx context.Context, agentAddr, reason string) error
}

// WebhookNotifier sends webhook events for critical alerts.
type WebhookNotifier interface {
	EmitForensicsCriticalAlert(agentAddr, alertID, alertType string, score float64)
}

// IncidentSink receives every alert the service emits, regardless of severity.
// Typically backed by the compliance aggregator. Must not block the Ingest
// path for longer than a few ms; heavy processing should be async.
type IncidentSink interface {
	RecordForensicsAlert(ctx context.Context, a *Alert)
}

// Service manages anomaly detection and forensic analysis.
type Service struct {
	store    Store
	config   Config
	logger   *slog.Logger
	pauser   AgentPauser     // auto-pause on critical alerts
	webhooks WebhookNotifier // notify on critical alerts
	sink     IncidentSink    // receives every surfaced alert
	// Per-agent locks instead of a single global mutex.
	locks sync.Map // map[string]*sync.Mutex
	// Alert rate limiting: track last alert time per agent to prevent spam.
	// Key: agentAddr, Value: time.Time of last alert.
	lastAlert sync.Map // map[string]time.Time
}

// NewService creates a new forensics service.
func NewService(store Store, cfg Config, logger *slog.Logger) *Service {
	return &Service{store: store, config: cfg, logger: logger}
}

// WithAgentPauser adds automatic agent suspension on critical alerts.
func (s *Service) WithAgentPauser(p AgentPauser) *Service {
	s.pauser = p
	return s
}

// WithWebhooks adds webhook notifications for critical alerts.
func (s *Service) WithWebhooks(w WebhookNotifier) *Service {
	s.webhooks = w
	return s
}

// WithIncidentSink wires a sink that receives every alert the service emits.
// Safe to call with nil to disable the sink.
func (s *Service) WithIncidentSink(sink IncidentSink) *Service {
	s.sink = sink
	return s
}

func (s *Service) agentLock(addr string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(addr, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Ingest processes a spend event, updates the baseline, and checks for anomalies.
// Returns any alerts generated. Callers should check alerts and take action.
// Uses per-agent locks so different agents can be processed concurrently.
func (s *Service) Ingest(ctx context.Context, event SpendEvent) ([]*Alert, error) {
	mu := s.agentLock(event.AgentAddr)
	mu.Lock()
	defer mu.Unlock()

	baseline, err := s.store.GetBaseline(ctx, event.AgentAddr)
	if err != nil {
		// First time seeing this agent — create baseline
		baseline = &Baseline{
			AgentAddr:           event.AgentAddr,
			KnownCounterparties: make(map[string]int),
			KnownServices:       make(map[string]int),
		}
	}

	var alerts []*Alert

	// Only score if we have enough baseline data
	if baseline.TxCount >= s.config.MinTxForBaseline {
		alerts = s.detectAnomalies(baseline, event)
	}

	// Rate limit alerts per agent+type: suppress duplicate alert types within 30 seconds.
	// This prevents spam during sustained anomalous activity while still reporting new alert types.
	if len(alerts) > 0 {
		var filtered []*Alert
		for _, a := range alerts {
			key := event.AgentAddr + ":" + string(a.Type)
			if lastTime, ok := s.lastAlert.Load(key); ok {
				if time.Since(lastTime.(time.Time)) < 30*time.Second {
					// Suppress duplicate type, but always allow critical
					if a.Severity != SeverityCritical {
						continue
					}
				}
			}
			s.lastAlert.Store(key, time.Now())
			filtered = append(filtered, a)
		}
		alerts = filtered
	}

	// Update baseline with new event
	s.updateBaseline(baseline, event)

	if err := s.store.SaveBaseline(ctx, baseline); err != nil {
		return nil, err
	}

	metrics.ForensicsEventsIngested.Inc()

	// Persist alerts
	for _, alert := range alerts {
		if err := s.store.SaveAlert(ctx, alert); err != nil {
			s.logger.Error("forensics: failed to save alert", "err", err)
		}
		metrics.ForensicsAlertsTotal.WithLabelValues(string(alert.Severity)).Inc()
		s.logger.Warn("forensics: anomaly detected",
			"agent", alert.AgentAddr,
			"type", alert.Type,
			"severity", alert.Severity,
			"score", alert.Score,
			"sigma", alert.Sigma,
		)

		if s.sink != nil {
			s.sink.RecordForensicsAlert(ctx, alert)
		}

		// Critical alerts: auto-pause agent + notify via webhook
		if alert.Severity == SeverityCritical {
			if s.pauser != nil {
				if err := s.pauser.PauseAgent(ctx, alert.AgentAddr,
					"forensics: critical anomaly detected — "+alert.Message); err != nil {
					s.logger.Error("forensics: failed to pause agent", "agent", alert.AgentAddr, "err", err)
				} else {
					s.logger.Warn("forensics: agent auto-paused due to critical alert",
						"agent", alert.AgentAddr, "alert", alert.ID)
				}
			}
			if s.webhooks != nil {
				s.webhooks.EmitForensicsCriticalAlert(alert.AgentAddr, alert.ID, string(alert.Type), alert.Score)
			}
		}
	}

	return alerts, nil
}

// GetBaseline returns the current behavioral baseline for an agent.
func (s *Service) GetBaseline(ctx context.Context, agentAddr string) (*Baseline, error) {
	return s.store.GetBaseline(ctx, agentAddr)
}

// ListAlerts returns recent alerts for an agent.
func (s *Service) ListAlerts(ctx context.Context, agentAddr string, limit int) ([]*Alert, error) {
	return s.store.ListAlerts(ctx, agentAddr, limit)
}

// AcknowledgeAlert marks an alert as reviewed.
func (s *Service) AcknowledgeAlert(ctx context.Context, alertID string) error {
	return s.store.AcknowledgeAlert(ctx, alertID)
}

func (s *Service) detectAnomalies(b *Baseline, event SpendEvent) []*Alert {
	var alerts []*Alert

	// 1. Amount anomaly: single tx significantly above normal
	if b.StdDevAmount > 0 {
		sigma := (event.Amount - b.MeanAmount) / b.StdDevAmount
		if sigma > s.config.SigmaThreshold {
			score := math.Min(sigma/s.config.SigmaThreshold*50, 100)
			alerts = append(alerts, &Alert{
				ID:         idgen.WithPrefix("alrt_"),
				AgentAddr:  event.AgentAddr,
				Type:       AlertAmountAnomaly,
				Severity:   severityFromScore(score),
				Message:    "Transaction amount significantly above agent's normal pattern",
				Score:      score,
				Baseline:   b.MeanAmount,
				Actual:     event.Amount,
				Sigma:      sigma,
				DetectedAt: time.Now(),
			})
		}
	}

	// 2. New counterparty: paying an agent never seen before
	if _, known := b.KnownCounterparties[event.Counterparty]; !known && b.TxCount >= s.config.MinTxForBaseline {
		uniqueRatio := float64(len(b.KnownCounterparties)) / float64(b.TxCount)
		// If agent usually deals with few counterparties, a new one is more suspicious
		score := math.Min((1.0-uniqueRatio)*60+20, 100)
		if uniqueRatio < 0.3 { // concentrated counterparty set
			alerts = append(alerts, &Alert{
				ID:         idgen.WithPrefix("alrt_"),
				AgentAddr:  event.AgentAddr,
				Type:       AlertNewCounterparty,
				Severity:   SeverityWarning,
				Message:    "Payment to previously unseen counterparty from agent with concentrated payment pattern",
				Score:      score,
				Baseline:   float64(len(b.KnownCounterparties)),
				Actual:     1,
				DetectedAt: time.Now(),
			})
		}
	}

	// 3. Service type deviation: using a service type never used before
	if _, known := b.KnownServices[event.ServiceType]; !known && event.ServiceType != "" {
		alerts = append(alerts, &Alert{
			ID:         idgen.WithPrefix("alrt_"),
			AgentAddr:  event.AgentAddr,
			Type:       AlertServiceDeviation,
			Severity:   SeverityInfo,
			Message:    "Agent using service type not in its historical pattern",
			Score:      30,
			Baseline:   float64(len(b.KnownServices)),
			Actual:     1,
			DetectedAt: time.Now(),
		})
	}

	// 4. Velocity spike: spend rate over rolling window exceeds normal
	if b.StdDevVelocity > 0 && len(b.recentAmounts) > 0 {
		currentVelocity := s.computeVelocity(b)
		sigma := (currentVelocity - b.MeanVelocity) / b.StdDevVelocity
		if sigma > s.config.SigmaThreshold {
			score := math.Min(sigma/s.config.SigmaThreshold*50, 100)
			alerts = append(alerts, &Alert{
				ID:         idgen.WithPrefix("alrt_"),
				AgentAddr:  event.AgentAddr,
				Type:       AlertVelocitySpike,
				Severity:   severityFromScore(score),
				Message:    "Spend velocity significantly exceeds agent's normal rate",
				Score:      score,
				Baseline:   b.MeanVelocity,
				Actual:     currentVelocity,
				Sigma:      sigma,
				DetectedAt: time.Now(),
			})
		}
	}

	// 5. Burst pattern: too many transactions in short window
	if len(b.recentTimestamps) >= s.config.BurstThreshold {
		windowStart := time.Now().Add(-s.config.BurstWindow)
		burstCount := 0
		for _, ts := range b.recentTimestamps {
			if ts.After(windowStart) {
				burstCount++
			}
		}
		if burstCount >= s.config.BurstThreshold {
			alerts = append(alerts, &Alert{
				ID:         idgen.WithPrefix("alrt_"),
				AgentAddr:  event.AgentAddr,
				Type:       AlertBurstPattern,
				Severity:   SeverityCritical,
				Message:    "Burst of transactions exceeds threshold — possible runaway loop",
				Score:      90,
				Baseline:   float64(s.config.BurstThreshold),
				Actual:     float64(burstCount),
				DetectedAt: time.Now(),
			})
		}
	}

	// 6. Time anomaly: activity outside normal hours
	hour := event.Timestamp.Hour()
	if b.TxCount >= 50 { // need enough data for hourly patterns
		totalInHour := b.ActiveHours[hour]
		avgPerHour := float64(b.TxCount) / 24.0
		if avgPerHour > 0 && float64(totalInHour) < avgPerHour*0.05 {
			// This hour has <5% of expected activity — unusual time
			alerts = append(alerts, &Alert{
				ID:         idgen.WithPrefix("alrt_"),
				AgentAddr:  event.AgentAddr,
				Type:       AlertTimeAnomaly,
				Severity:   SeverityInfo,
				Message:    "Transaction at unusual time for this agent",
				Score:      25,
				Baseline:   avgPerHour,
				Actual:     float64(totalInHour),
				DetectedAt: time.Now(),
			})
		}
	}

	return alerts
}

func (s *Service) updateBaseline(b *Baseline, event SpendEvent) {
	// Update running mean and stddev (Welford's algorithm)
	b.TxCount++
	delta := event.Amount - b.MeanAmount
	b.MeanAmount += delta / float64(b.TxCount)
	delta2 := event.Amount - b.MeanAmount
	b.m2Amount += delta * delta2
	if b.TxCount > 1 {
		b.StdDevAmount = math.Sqrt(b.m2Amount / float64(b.TxCount-1))
	}

	// Update counterparty and service maps
	b.KnownCounterparties[event.Counterparty]++
	if event.ServiceType != "" {
		b.KnownServices[event.ServiceType]++
	}

	// Update hourly distribution
	b.ActiveHours[event.Timestamp.Hour()]++

	// Update rolling window for velocity
	b.recentAmounts = append(b.recentAmounts, event.Amount)
	b.recentTimestamps = append(b.recentTimestamps, event.Timestamp)

	// Trim rolling window
	cutoff := time.Now().Add(-s.config.VelocityWindow)
	trimIdx := 0
	for trimIdx < len(b.recentTimestamps) && b.recentTimestamps[trimIdx].Before(cutoff) {
		trimIdx++
	}
	if trimIdx > 0 {
		b.recentAmounts = b.recentAmounts[trimIdx:]
		b.recentTimestamps = b.recentTimestamps[trimIdx:]
	}

	// Update velocity stats
	velocity := s.computeVelocity(b)
	if b.TxCount <= s.config.MinTxForBaseline {
		b.MeanVelocity = velocity
	} else {
		// EMA for velocity
		alpha := 0.1
		b.MeanVelocity = alpha*velocity + (1-alpha)*b.MeanVelocity
		diff := velocity - b.MeanVelocity
		b.StdDevVelocity = math.Sqrt(alpha*diff*diff + (1-alpha)*b.StdDevVelocity*b.StdDevVelocity)
	}

	b.LastUpdated = time.Now()
}

func (s *Service) computeVelocity(b *Baseline) float64 {
	if len(b.recentAmounts) == 0 {
		return 0
	}
	var total float64
	for _, a := range b.recentAmounts {
		total += a
	}
	hours := s.config.VelocityWindow.Hours()
	if hours == 0 {
		return total
	}
	return total / hours
}

func severityFromScore(score float64) AlertSeverity {
	if score >= 80 {
		return SeverityCritical
	}
	if score >= 50 {
		return SeverityWarning
	}
	return SeverityInfo
}
