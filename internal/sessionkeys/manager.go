package sessionkeys

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/usdc"
)

// Store defines the interface for session key persistence
type Store interface {
	Create(ctx context.Context, key *SessionKey) error
	Get(ctx context.Context, id string) (*SessionKey, error)
	GetByOwner(ctx context.Context, ownerAddr string) ([]*SessionKey, error)
	Update(ctx context.Context, key *SessionKey) error
	Delete(ctx context.Context, id string) error
	CountActive(ctx context.Context) (int64, error) // Count non-revoked, non-expired keys
}

// ServiceResolver resolves service information for validation
type ServiceResolver interface {
	// GetServiceType returns the service type for a given service ID
	GetServiceType(ctx context.Context, serviceID string) (string, error)
	// GetAgentForService returns the agent address offering a service
	GetAgentForService(ctx context.Context, serviceID string) (string, error)
}

// Manager handles session key operations
type Manager struct {
	store    Store
	resolver ServiceResolver
}

// NewManager creates a new session key manager
func NewManager(store Store, resolver ServiceResolver) *Manager {
	return &Manager{
		store:    store,
		resolver: resolver,
	}
}

// Create creates a new session key with the given permissions
func (m *Manager) Create(ctx context.Context, ownerAddr string, req *SessionKeyRequest) (*SessionKey, error) {
	// Validate public key is provided
	if req.PublicKey == "" {
		return nil, &ValidationError{Code: "missing_public_key", Message: "publicKey is required - generate an ECDSA keypair and provide the address"}
	}

	// Validate public key format (should be Ethereum address)
	publicKey := strings.ToLower(req.PublicKey)
	if !strings.HasPrefix(publicKey, "0x") || len(publicKey) != 42 {
		return nil, &ValidationError{Code: "invalid_public_key", Message: "publicKey must be a valid Ethereum address (0x...)"}
	}

	// Parse expiration
	var expiresAt time.Time
	if req.ExpiresAt != "" {
		var err error
		expiresAt, err = time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("invalid expiresAt format: %w", err)
		}
	} else if req.ExpiresIn != "" {
		duration, err := parseDuration(req.ExpiresIn)
		if err != nil {
			return nil, fmt.Errorf("invalid expiresIn format: %w", err)
		}
		expiresAt = time.Now().Add(duration)
	} else {
		// Default: 24 hours
		expiresAt = time.Now().Add(24 * time.Hour)
	}

	// Validate that at least some restriction is set
	if !req.AllowAny && len(req.AllowedRecipients) == 0 && len(req.AllowedServiceTypes) == 0 {
		return nil, fmt.Errorf("must set allowedRecipients, allowedServiceTypes, or allowAny")
	}

	// Create the session key
	key := &SessionKey{
		ID:        idgen.WithPrefix("sk_"),
		OwnerAddr: strings.ToLower(ownerAddr),
		PublicKey: publicKey, // The session key's Ethereum address
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxPerTransaction:   req.MaxPerTransaction,
			MaxPerDay:           req.MaxPerDay,
			MaxTotal:            req.MaxTotal,
			ExpiresAt:           expiresAt,
			AllowedRecipients:   toLower(req.AllowedRecipients),
			AllowedServiceTypes: toLower(req.AllowedServiceTypes),
			AllowAny:            req.AllowAny,
			Label:               req.Label,
		},
		Usage: SessionKeyUsage{
			TotalSpent:   "0",
			SpentToday:   "0",
			LastResetDay: time.Now().Format("2006-01-02"),
			LastNonce:    0,
		},
	}

	if err := m.store.Create(ctx, key); err != nil {
		return nil, fmt.Errorf("failed to create session key: %w", err)
	}

	return key, nil
}

// Get retrieves a session key by ID
func (m *Manager) Get(ctx context.Context, id string) (*SessionKey, error) {
	return m.store.Get(ctx, id)
}

// List returns all session keys for an owner
func (m *Manager) List(ctx context.Context, ownerAddr string) ([]*SessionKey, error) {
	return m.store.GetByOwner(ctx, strings.ToLower(ownerAddr))
}

// CountActive returns the count of active (non-revoked, non-expired) session keys
func (m *Manager) CountActive(ctx context.Context) (int64, error) {
	return m.store.CountActive(ctx)
}

// Revoke revokes a session key
func (m *Manager) Revoke(ctx context.Context, id string) error {
	key, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}

	now := time.Now()
	key.RevokedAt = &now
	return m.store.Update(ctx, key)
}

// Validate checks if a transaction is allowed under a session key
func (m *Manager) Validate(ctx context.Context, keyID string, to string, amount string, serviceID string) error {
	key, err := m.store.Get(ctx, keyID)
	if err != nil {
		return ErrKeyNotFound
	}

	return m.validateTransaction(ctx, key, to, amount, serviceID)
}

// ValidateSigned validates a cryptographically signed transaction request
// This verifies:
// 1. Signature is valid and matches session key's public key
// 2. Nonce hasn't been used (replay protection)
// 3. Timestamp is fresh (within 5 minutes)
// 4. Transaction is allowed under session key permissions
func (m *Manager) ValidateSigned(ctx context.Context, keyID string, req *SignedTransactRequest) error {
	key, err := m.store.Get(ctx, keyID)
	if err != nil {
		return ErrKeyNotFound
	}

	// 1. Verify signature
	message := CreateTransactionMessage(req.To, req.Amount, req.Nonce, req.Timestamp)
	recoveredAddr, err := RecoverAddress(message, req.Signature)
	if err != nil {
		return &ValidationError{
			Code:    "invalid_signature",
			Message: "Failed to verify signature: " + err.Error(),
		}
	}

	// Check recovered address matches session key's public key
	if !strings.EqualFold(recoveredAddr, key.PublicKey) {
		return &ValidationError{
			Code:    "signature_mismatch",
			Message: fmt.Sprintf("Signature from %s does not match session key %s", recoveredAddr, key.PublicKey),
		}
	}

	// 2. Check nonce (must be greater than last used)
	if req.Nonce <= key.Usage.LastNonce {
		return &ValidationError{
			Code:    "nonce_reused",
			Message: fmt.Sprintf("Nonce %d must be greater than last used nonce %d", req.Nonce, key.Usage.LastNonce),
		}
	}

	// 3. Check timestamp freshness (within 5 minutes)
	now := time.Now().Unix()
	maxAge := int64(5 * 60) // 5 minutes
	if now-req.Timestamp > maxAge {
		return &ValidationError{
			Code:    "signature_expired",
			Message: fmt.Sprintf("Signature timestamp is %d seconds old (max %d)", now-req.Timestamp, maxAge),
		}
	}

	// Don't allow future timestamps (with small tolerance)
	if req.Timestamp > now+60 {
		return &ValidationError{
			Code:    "invalid_timestamp",
			Message: "Signature timestamp is in the future",
		}
	}

	// 4. Validate permissions
	return m.validateTransaction(ctx, key, req.To, req.Amount, req.ServiceID)
}

// RecordUsage updates usage stats after a successful transaction
func (m *Manager) RecordUsage(ctx context.Context, keyID string, amount string, nonce uint64) error {
	key, err := m.store.Get(ctx, keyID)
	if err != nil {
		return err
	}

	amountBig, _ := usdc.Parse(amount)

	// Update total spent
	totalSpent, _ := usdc.Parse(key.Usage.TotalSpent)
	newTotal := new(big.Int).Add(totalSpent, amountBig)
	key.Usage.TotalSpent = usdc.Format(newTotal)

	// Update daily spent (reset if new day)
	today := time.Now().Format("2006-01-02")
	if key.Usage.LastResetDay != today {
		key.Usage.SpentToday = "0"
		key.Usage.LastResetDay = today
	}
	spentToday, _ := usdc.Parse(key.Usage.SpentToday)
	newDaily := new(big.Int).Add(spentToday, amountBig)
	key.Usage.SpentToday = usdc.Format(newDaily)

	// Update counters
	key.Usage.TransactionCount++
	key.Usage.LastUsed = time.Now()
	key.Usage.LastNonce = nonce // Track nonce for replay protection

	return m.store.Update(ctx, key)
}

// validateTransaction performs all permission validation checks
func (m *Manager) validateTransaction(ctx context.Context, key *SessionKey, to string, amount string, serviceID string) error {
	now := time.Now()
	to = strings.ToLower(to)

	// Check if revoked
	if key.RevokedAt != nil {
		return ErrKeyRevoked
	}

	// Check expiration
	if now.After(key.Permission.ExpiresAt) {
		return ErrKeyExpired
	}

	// Check valid after
	if !key.Permission.ValidAfter.IsZero() && now.Before(key.Permission.ValidAfter) {
		return ErrKeyNotYetValid
	}

	// Parse amount
	amountBig, ok := usdc.Parse(amount)
	if !ok {
		return &ValidationError{Code: "invalid_amount", Message: "Invalid amount format"}
	}
	if amountBig.Sign() <= 0 {
		return &ValidationError{Code: "invalid_amount", Message: "Amount must be positive"}
	}

	// Check per-transaction limit
	if key.Permission.MaxPerTransaction != "" {
		maxPerTx, ok := usdc.Parse(key.Permission.MaxPerTransaction)
		if ok && amountBig.Cmp(maxPerTx) > 0 {
			return ErrExceedsPerTx
		}
	}

	// Check daily limit (reset if new day)
	// Use a local variable -- do NOT mutate key.Usage here as this is a read-path
	today := now.Format("2006-01-02")
	spentToday := key.Usage.SpentToday
	if key.Usage.LastResetDay != today {
		spentToday = "0"
	}

	if key.Permission.MaxPerDay != "" {
		maxDaily, ok := usdc.Parse(key.Permission.MaxPerDay)
		if ok {
			spent, _ := usdc.Parse(spentToday)
			newTotal := new(big.Int).Add(spent, amountBig)
			if newTotal.Cmp(maxDaily) > 0 {
				return ErrExceedsDaily
			}
		}
	}

	// Check total limit
	if key.Permission.MaxTotal != "" {
		maxTotal, ok := usdc.Parse(key.Permission.MaxTotal)
		if ok {
			spent, _ := usdc.Parse(key.Usage.TotalSpent)
			newTotal := new(big.Int).Add(spent, amountBig)
			if newTotal.Cmp(maxTotal) > 0 {
				return ErrExceedsTotal
			}
		}
	}

	// Check recipient restrictions
	if !key.Permission.AllowAny {
		allowed := false

		// Check explicit recipients
		for _, addr := range key.Permission.AllowedRecipients {
			if strings.ToLower(addr) == to {
				allowed = true
				break
			}
		}

		// Check service types (requires resolver)
		if !allowed && len(key.Permission.AllowedServiceTypes) > 0 && serviceID != "" && m.resolver != nil {
			serviceType, err := m.resolver.GetServiceType(ctx, serviceID)
			if err == nil {
				for _, t := range key.Permission.AllowedServiceTypes {
					if strings.EqualFold(t, serviceType) {
						allowed = true
						break
					}
				}
			}
		}

		// Check allowed service agents
		if !allowed && len(key.Permission.AllowedServiceAgents) > 0 {
			for _, addr := range key.Permission.AllowedServiceAgents {
				if strings.ToLower(addr) == to {
					allowed = true
					break
				}
			}
		}

		if !allowed {
			return ErrRecipientNotAllowed
		}
	}

	return nil
}

// Helper functions

func parseDuration(s string) (time.Duration, error) {
	// Support "7d" for days
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var d int
		if _, err := fmt.Sscanf(days, "%d", &d); err != nil {
			return 0, err
		}
		return time.Duration(d) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func toLower(ss []string) []string {
	result := make([]string, len(ss))
	for i, s := range ss {
		result[i] = strings.ToLower(s)
	}
	return result
}
