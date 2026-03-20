// Package kya implements Know Your Agent (KYA) identity certificates.
//
// A KYA certificate is a signed, timestamped identity document for an AI agent
// containing: agent DID, organizational binding (which company authorized it),
// permission scope (spending limits, allowed APIs), and behavioral reputation
// derived from TraceRank payment history.
//
// This enables:
//   - Counterparty verification before accepting agent payments
//   - EU AI Act Article 12 compliant technical documentation
//   - SOC2-ready agent access review reports
//   - Automated certificate revocation when session keys expire
//
// Based on: AgentFacts KYA standard (2026), ERC-8004, W3C DID compatibility.
package kya

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/metrics"
)

var (
	ErrCertNotFound  = errors.New("kya: certificate not found")
	ErrCertRevoked   = errors.New("kya: certificate revoked")
	ErrCertExpired   = errors.New("kya: certificate expired")
	ErrAgentNotFound = errors.New("kya: agent not found")
)

// CertStatus represents the lifecycle state of a KYA certificate.
type CertStatus string

const (
	CertActive  CertStatus = "active"
	CertRevoked CertStatus = "revoked"
	CertExpired CertStatus = "expired"
)

// TrustTier maps TraceRank reputation to a human-readable trust level.
type TrustTier string

const (
	TierAAA TrustTier = "AAA" // Top 5% — instant settlement eligible
	TierAA  TrustTier = "AA"  // Top 15% — reduced escrow
	TierA   TrustTier = "A"   // Top 30% — standard terms
	TierB   TrustTier = "B"   // Average — standard terms
	TierC   TrustTier = "C"   // Below average — full escrow required
	TierD   TrustTier = "D"   // No history or poor history
)

// PermissionScope defines what the agent is authorized to do.
type PermissionScope struct {
	MaxSpendPerTx  string   `json:"maxSpendPerTx,omitempty"`  // USDC ceiling per transaction
	MaxSpendPerDay string   `json:"maxSpendPerDay,omitempty"` // USDC ceiling per day
	TotalBudget    string   `json:"totalBudget,omitempty"`    // Lifetime budget
	AllowedAPIs    []string `json:"allowedApis,omitempty"`    // Permitted service types
	BlockedAPIs    []string `json:"blockedApis,omitempty"`    // Blocked service types
	ExpiresAt      string   `json:"expiresAt,omitempty"`      // Permission expiry (RFC3339)
}

// OrgBinding ties the agent to a legal entity.
type OrgBinding struct {
	TenantID     string `json:"tenantId"`             // Alancoin tenant
	OrgName      string `json:"orgName"`              // Human-readable org name
	Department   string `json:"department,omitempty"` // Cost center / department
	AuthorizedBy string `json:"authorizedBy"`         // Address of authorizing human/agent
	AuthMethod   string `json:"authMethod"`           // "api_key", "session_key", "ecdsa"
}

// ReputationSnapshot captures the agent's standing at certificate issuance.
type ReputationSnapshot struct {
	TraceRankScore float64   `json:"traceRankScore"` // 0-100
	TrustTier      TrustTier `json:"trustTier"`
	DisputeRate    float64   `json:"disputeRate"` // 0.0-1.0
	SuccessRate    float64   `json:"successRate"` // 0.0-1.0
	TxCount        int       `json:"txCount"`
	AccountAgeDays int       `json:"accountAgeDays"`
	ComputedAt     time.Time `json:"computedAt"`
}

// Certificate is a KYA identity document for an agent.
type Certificate struct {
	ID          string             `json:"id"`
	AgentAddr   string             `json:"agentAddr"`
	DID         string             `json:"did"` // W3C DID format: did:alancoin:<addr>
	Org         OrgBinding         `json:"org"`
	Permissions PermissionScope    `json:"permissions"`
	Reputation  ReputationSnapshot `json:"reputation"`
	Status      CertStatus         `json:"status"`
	IssuedAt    time.Time          `json:"issuedAt"`
	ExpiresAt   time.Time          `json:"expiresAt"`
	RevokedAt   *time.Time         `json:"revokedAt,omitempty"`
	Signature   string             `json:"signature"` // HMAC-SHA256 of canonical fields
}

// IsValid returns true if the certificate is active and not expired.
func (c *Certificate) IsValid() bool {
	if c.Status != CertActive {
		return false
	}
	return time.Now().Before(c.ExpiresAt)
}

// AgentProvider retrieves agent information for certificate generation.
type AgentProvider interface {
	GetAgentName(ctx context.Context, addr string) (string, error)
	GetAgentCreatedAt(ctx context.Context, addr string) (time.Time, error)
}

// ReputationProvider retrieves reputation data.
type ReputationProvider interface {
	GetScore(ctx context.Context, addr string) (float64, error)
	GetSuccessRate(ctx context.Context, addr string) (float64, error)
	GetDisputeRate(ctx context.Context, addr string) (float64, error)
	GetTxCount(ctx context.Context, addr string) (int, error)
}

// Store persists KYA certificates.
type Store interface {
	Create(ctx context.Context, cert *Certificate) error
	Get(ctx context.Context, id string) (*Certificate, error)
	GetByAgent(ctx context.Context, agentAddr string) (*Certificate, error)
	Update(ctx context.Context, cert *Certificate) error
	ListByTenant(ctx context.Context, tenantID string, limit int) ([]*Certificate, error)
}

// Service manages KYA certificate lifecycle.
type Service struct {
	store      Store
	agents     AgentProvider
	reputation ReputationProvider
	hmacKey    []byte
	logger     *slog.Logger
	mu         sync.Mutex
}

// NewService creates a new KYA service.
func NewService(store Store, agents AgentProvider, rep ReputationProvider, hmacKey []byte, logger *slog.Logger) *Service {
	return &Service{
		store:      store,
		agents:     agents,
		reputation: rep,
		hmacKey:    hmacKey,
		logger:     logger,
	}
}

// Issue creates a new KYA certificate for an agent.
func (s *Service) Issue(ctx context.Context, agentAddr string, org OrgBinding, perms PermissionScope, validDays int) (*Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for existing active cert — revoke it first
	if existing, err := s.store.GetByAgent(ctx, agentAddr); err == nil && existing.IsValid() {
		existing.Status = CertRevoked
		now := time.Now()
		existing.RevokedAt = &now
		if err := s.store.Update(ctx, existing); err != nil {
			return nil, fmt.Errorf("kya: revoke existing cert: %w", err)
		}
	}

	// Build reputation snapshot
	rep, err := s.buildReputation(ctx, agentAddr)
	if err != nil {
		return nil, fmt.Errorf("kya: build reputation: %w", err)
	}

	now := time.Now()
	cert := &Certificate{
		ID:          idgen.WithPrefix("kya_"),
		AgentAddr:   agentAddr,
		DID:         "did:alancoin:" + agentAddr,
		Org:         org,
		Permissions: perms,
		Reputation:  *rep,
		Status:      CertActive,
		IssuedAt:    now,
		ExpiresAt:   now.AddDate(0, 0, validDays),
	}

	// Sign the certificate
	cert.Signature = s.sign(cert)

	if err := s.store.Create(ctx, cert); err != nil {
		return nil, fmt.Errorf("kya: create cert: %w", err)
	}

	metrics.KYACertificatesIssuedTotal.Inc()

	s.logger.Info("kya: certificate issued",
		"cert_id", cert.ID,
		"agent", agentAddr,
		"tier", cert.Reputation.TrustTier,
		"expires", cert.ExpiresAt,
	)

	return cert, nil
}

// GetByAgent returns the active certificate for an agent, or ErrCertNotFound.
func (s *Service) GetByAgent(ctx context.Context, agentAddr string) (*Certificate, error) {
	return s.store.GetByAgent(ctx, agentAddr)
}

// Verify checks a certificate's validity and signature.
func (s *Service) Verify(ctx context.Context, certID string) (*Certificate, error) {
	cert, err := s.store.Get(ctx, certID)
	if err != nil {
		return nil, ErrCertNotFound
	}

	if cert.Status == CertRevoked {
		return cert, ErrCertRevoked
	}

	if !cert.IsValid() {
		cert.Status = CertExpired
		_ = s.store.Update(ctx, cert)
		return cert, ErrCertExpired
	}

	// Verify HMAC signature
	expected := s.sign(cert)
	if cert.Signature != expected {
		return cert, errors.New("kya: signature mismatch — certificate may have been tampered")
	}

	return cert, nil
}

// Revoke invalidates a certificate.
func (s *Service) Revoke(ctx context.Context, certID string, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cert, err := s.store.Get(ctx, certID)
	if err != nil {
		return ErrCertNotFound
	}

	cert.Status = CertRevoked
	now := time.Now()
	cert.RevokedAt = &now

	if err := s.store.Update(ctx, cert); err != nil {
		return fmt.Errorf("kya: revoke: %w", err)
	}

	metrics.KYACertificatesRevokedTotal.Inc()

	s.logger.Info("kya: certificate revoked", "cert_id", certID, "reason", reason)
	return nil
}

// ComplianceExport generates an EU AI Act Article 12 technical documentation package.
func (s *Service) ComplianceExport(ctx context.Context, certID string) (*ComplianceReport, error) {
	cert, err := s.store.Get(ctx, certID)
	if err != nil {
		return nil, ErrCertNotFound
	}

	return &ComplianceReport{
		CertificateID:  cert.ID,
		AgentDID:       cert.DID,
		Org:            cert.Org,
		Permissions:    cert.Permissions,
		Reputation:     cert.Reputation,
		Status:         cert.Status,
		IssuedAt:       cert.IssuedAt,
		ExpiresAt:      cert.ExpiresAt,
		RevokedAt:      cert.RevokedAt,
		SignatureValid: cert.Signature == s.sign(cert),
		GeneratedAt:    time.Now(),
		Standard:       "EU AI Act Article 12 — Technical Documentation",
		SchemaVersion:  "1.0",
	}, nil
}

// ComplianceReport is the EU AI Act Article 12 export format.
type ComplianceReport struct {
	CertificateID  string             `json:"certificateId"`
	AgentDID       string             `json:"agentDid"`
	Org            OrgBinding         `json:"org"`
	Permissions    PermissionScope    `json:"permissions"`
	Reputation     ReputationSnapshot `json:"reputation"`
	Status         CertStatus         `json:"status"`
	IssuedAt       time.Time          `json:"issuedAt"`
	ExpiresAt      time.Time          `json:"expiresAt"`
	RevokedAt      *time.Time         `json:"revokedAt,omitempty"`
	SignatureValid bool               `json:"signatureValid"`
	GeneratedAt    time.Time          `json:"generatedAt"`
	Standard       string             `json:"standard"`
	SchemaVersion  string             `json:"schemaVersion"`
}

func (s *Service) buildReputation(ctx context.Context, addr string) (*ReputationSnapshot, error) {
	score, err := s.reputation.GetScore(ctx, addr)
	if err != nil {
		score = 0
	}
	successRate, _ := s.reputation.GetSuccessRate(ctx, addr)
	disputeRate, _ := s.reputation.GetDisputeRate(ctx, addr)
	txCount, _ := s.reputation.GetTxCount(ctx, addr)

	createdAt, err := s.agents.GetAgentCreatedAt(ctx, addr)
	ageDays := 0
	if err == nil {
		ageDays = int(time.Since(createdAt).Hours() / 24)
	}

	return &ReputationSnapshot{
		TraceRankScore: score,
		TrustTier:      computeTier(score, txCount, disputeRate),
		DisputeRate:    disputeRate,
		SuccessRate:    successRate,
		TxCount:        txCount,
		AccountAgeDays: ageDays,
		ComputedAt:     time.Now(),
	}, nil
}

func computeTier(score float64, txCount int, disputeRate float64) TrustTier {
	if disputeRate > 0.1 || txCount < 5 {
		return TierD
	}
	if score >= 90 && txCount >= 100 && disputeRate < 0.01 {
		return TierAAA
	}
	if score >= 80 && txCount >= 50 && disputeRate < 0.02 {
		return TierAA
	}
	if score >= 65 && txCount >= 20 && disputeRate < 0.05 {
		return TierA
	}
	if score >= 40 {
		return TierB
	}
	return TierC
}

func (s *Service) sign(cert *Certificate) string {
	// Canonical signing payload: ID + agent + DID + tenant + issuedAt + expiresAt
	payload := fmt.Sprintf("%s|%s|%s|%s|%d|%d",
		cert.ID, cert.AgentAddr, cert.DID, cert.Org.TenantID,
		cert.IssuedAt.UnixMilli(), cert.ExpiresAt.UnixMilli(),
	)
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
