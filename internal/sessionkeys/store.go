package sessionkeys

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// MemoryStore is an in-memory implementation of Store
type MemoryStore struct {
	mu   sync.RWMutex
	keys map[string]*SessionKey // keyed by ID
}

// NewMemoryStore creates a new in-memory session key store
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		keys: make(map[string]*SessionKey),
	}
}

// Create stores a new session key
func (s *MemoryStore) Create(ctx context.Context, key *SessionKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.keys[key.ID]; exists {
		return errors.New("session key already exists")
	}

	// Store a copy
	keyCopy := *key
	s.keys[key.ID] = &keyCopy
	return nil
}

// Get retrieves a session key by ID
func (s *MemoryStore) Get(ctx context.Context, id string) (*SessionKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key, exists := s.keys[id]
	if !exists {
		return nil, ErrKeyNotFound
	}

	// Return a copy
	keyCopy := *key
	return &keyCopy, nil
}

// GetByOwner returns all session keys for an owner address
func (s *MemoryStore) GetByOwner(ctx context.Context, ownerAddr string) ([]*SessionKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ownerAddr = strings.ToLower(ownerAddr)
	var result []*SessionKey

	for _, key := range s.keys {
		if strings.ToLower(key.OwnerAddr) == ownerAddr {
			keyCopy := *key
			result = append(result, &keyCopy)
		}
	}

	return result, nil
}

// GetByParent returns all session keys with the given parent key ID
func (s *MemoryStore) GetByParent(ctx context.Context, parentKeyID string) ([]*SessionKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*SessionKey
	for _, key := range s.keys {
		if key.ParentKeyID == parentKeyID {
			keyCopy := *key
			result = append(result, &keyCopy)
		}
	}
	return result, nil
}

// Update updates an existing session key
func (s *MemoryStore) Update(ctx context.Context, key *SessionKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.keys[key.ID]; !exists {
		return ErrKeyNotFound
	}

	keyCopy := *key
	s.keys[key.ID] = &keyCopy
	return nil
}

// Delete removes a session key
func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.keys[id]; !exists {
		return ErrKeyNotFound
	}

	delete(s.keys, id)
	return nil
}

// CountActive returns the number of active (non-revoked, non-expired) session keys
func (s *MemoryStore) CountActive(ctx context.Context) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int64
	for _, key := range s.keys {
		if key.IsActive() {
			count++
		}
	}
	return count, nil
}
