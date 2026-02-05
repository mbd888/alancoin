// Package auth provides API authentication for Alancoin.
//
// Authentication model:
// - Public endpoints (discovery, stats): No auth required
// - Mutations (update, delete): Require API key with ownership proof
// - API keys are issued on agent registration
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// Errors
var (
	ErrNoAPIKey      = errors.New("API key required")
	ErrInvalidAPIKey = errors.New("invalid or expired API key")
	ErrNotOwner      = errors.New("not authorized for this resource")
	ErrKeyNotFound   = errors.New("API key not found")
)

// APIKey represents an API key
type APIKey struct {
	ID        string     `json:"id"`
	Hash      string     `json:"-"`         // SHA256 hash of key (stored)
	AgentAddr string     `json:"agentAddr"` // The agent this key belongs to
	Name      string     `json:"name"`      // Friendly name
	CreatedAt time.Time  `json:"createdAt"`
	LastUsed  time.Time  `json:"lastUsed,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Revoked   bool       `json:"revoked"`
}

// Store persists API keys
type Store interface {
	Create(ctx context.Context, key *APIKey) error
	GetByHash(ctx context.Context, hash string) (*APIKey, error)
	GetByAgent(ctx context.Context, addr string) ([]*APIKey, error)
	Update(ctx context.Context, key *APIKey) error
	Delete(ctx context.Context, id string) error
}

// Manager handles authentication
type Manager struct {
	store Store
}

// NewManager creates a new auth manager
func NewManager(store Store) *Manager {
	return &Manager{store: store}
}

// GenerateKey creates a new API key for an agent
// Returns the raw key (shown once) and the stored metadata
func (m *Manager) GenerateKey(ctx context.Context, agentAddr, name string) (rawKey string, key *APIKey, err error) {
	// Generate 32 random bytes
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}

	// Create raw key with prefix
	rawKey = "sk_" + hex.EncodeToString(b)

	// Create key record
	key = &APIKey{
		ID:        "ak_" + hex.EncodeToString(b[:8]),
		Hash:      hashKey(rawKey),
		AgentAddr: strings.ToLower(agentAddr),
		Name:      name,
		CreatedAt: time.Now(),
	}

	if err := m.store.Create(ctx, key); err != nil {
		return "", nil, err
	}

	return rawKey, key, nil
}

// ValidateKey validates an API key and returns the key metadata
func (m *Manager) ValidateKey(ctx context.Context, rawKey string) (*APIKey, error) {
	if rawKey == "" {
		return nil, ErrNoAPIKey
	}

	// Clean the key
	rawKey = strings.TrimPrefix(rawKey, "Bearer ")
	rawKey = strings.TrimSpace(rawKey)

	if !strings.HasPrefix(rawKey, "sk_") {
		return nil, ErrInvalidAPIKey
	}

	// Look up by hash
	hash := hashKey(rawKey)
	key, err := m.store.GetByHash(ctx, hash)
	if err != nil {
		return nil, ErrInvalidAPIKey
	}

	// Check revoked
	if key.Revoked {
		return nil, ErrInvalidAPIKey
	}

	// Check expired
	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		return nil, ErrInvalidAPIKey
	}

	// Update last used (fire and forget)
	go func() {
		key.LastUsed = time.Now()
		m.store.Update(context.Background(), key)
	}()

	return key, nil
}

// ListKeys returns all keys for an agent
func (m *Manager) ListKeys(ctx context.Context, agentAddr string) ([]*APIKey, error) {
	return m.store.GetByAgent(ctx, strings.ToLower(agentAddr))
}

// RevokeKey revokes an API key
func (m *Manager) RevokeKey(ctx context.Context, keyID, agentAddr string) error {
	keys, err := m.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		return err
	}

	for _, k := range keys {
		if k.ID == keyID {
			k.Revoked = true
			return m.store.Update(ctx, k)
		}
	}

	return ErrKeyNotFound
}

func hashKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// MemoryStore is an in-memory implementation of Store
type MemoryStore struct {
	mu   sync.RWMutex
	keys map[string]*APIKey // by ID
}

// NewMemoryStore creates a new in-memory store
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		keys: make(map[string]*APIKey),
	}
}

func (s *MemoryStore) Create(ctx context.Context, key *APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key.ID] = key
	return nil
}

func (s *MemoryStore) GetByHash(ctx context.Context, hash string) (*APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.keys {
		if k.Hash == hash {
			return k, nil
		}
	}
	return nil, ErrKeyNotFound
}

func (s *MemoryStore) GetByAgent(ctx context.Context, addr string) ([]*APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*APIKey
	for _, k := range s.keys {
		if strings.EqualFold(k.AgentAddr, addr) {
			result = append(result, k)
		}
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, key *APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key.ID] = key
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys, id)
	return nil
}
