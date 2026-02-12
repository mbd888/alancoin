package sessionkeys

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/usdc"
)

// Store defines the interface for session key persistence
type Store interface {
	Create(ctx context.Context, key *SessionKey) error
	Get(ctx context.Context, id string) (*SessionKey, error)
	GetByOwner(ctx context.Context, ownerAddr string) ([]*SessionKey, error)
	GetByParent(ctx context.Context, parentKeyID string) ([]*SessionKey, error)
	Update(ctx context.Context, key *SessionKey) error
	Delete(ctx context.Context, id string) error
	CountActive(ctx context.Context) (int64, error) // Count non-revoked, non-expired keys
	ReParentChildren(ctx context.Context, oldParentID, newParentID string) error
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
	store       Store
	resolver    ServiceResolver
	policyStore PolicyStore           // optional: policy engine for additional constraints
	auditLog    DelegationAuditLogger // optional: delegation event audit trail
	keyLocks    sync.Map              // per-key locks to prevent nonce TOCTOU replay
}

// LockKey acquires a per-key mutex and returns the unlock function.
// Callers must defer the returned function to release the lock.
// This serializes validate+record for the same session key, preventing
// concurrent requests from replaying the same nonce.
func (m *Manager) LockKey(keyID string) func() {
	v, _ := m.keyLocks.LoadOrStore(keyID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// LockKeyChain acquires locks for the key and all its ancestors (leaf-to-root order).
// Returns an unlock function that releases all locks in reverse order.
// This prevents concurrent transactions on sibling keys from exceeding the parent budget.
func (m *Manager) LockKeyChain(ctx context.Context, keyID string) func() {
	var unlocks []func()

	// Lock the leaf key first
	unlocks = append(unlocks, m.LockKey(keyID))

	// Walk up and lock each ancestor
	key, err := m.store.Get(ctx, keyID)
	if err != nil {
		return func() {
			for i := len(unlocks) - 1; i >= 0; i-- {
				unlocks[i]()
			}
		}
	}

	parentID := key.ParentKeyID
	depth := 0
	for parentID != "" && depth < MaxDelegationDepth+1 {
		unlocks = append(unlocks, m.LockKey(parentID))
		ancestor, err := m.store.Get(ctx, parentID)
		if err != nil {
			break
		}
		parentID = ancestor.ParentKeyID
		depth++
	}

	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
}

// NewManager creates a new session key manager.
// An optional PolicyStore can be provided to enable the policy engine.
func NewManager(store Store, resolver ServiceResolver, policyStores ...PolicyStore) *Manager {
	m := &Manager{
		store:    store,
		resolver: resolver,
	}
	if len(policyStores) > 0 && policyStores[0] != nil {
		m.policyStore = policyStores[0]
	}
	return m
}

// WithDelegationAuditLogger adds an audit logger for delegation events.
func (m *Manager) WithDelegationAuditLogger(al DelegationAuditLogger) *Manager {
	m.auditLog = al
	return m
}

// AuditLogger returns the manager's delegation audit logger (may be nil).
func (m *Manager) AuditLogger() DelegationAuditLogger {
	return m.auditLog
}

// PolicyStore returns the manager's policy store (may be nil).
func (m *Manager) PolicyStore() PolicyStore {
	return m.policyStore
}

// Store returns the manager's session key store.
func (m *Manager) Store() Store {
	return m.store
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

	// Validate spending limit formats — reject malformed strings that would silently bypass limits
	for _, limit := range []struct{ name, value string }{
		{"maxPerTransaction", req.MaxPerTransaction},
		{"maxPerDay", req.MaxPerDay},
		{"maxTotal", req.MaxTotal},
	} {
		if limit.value != "" {
			v, ok := usdc.Parse(limit.value)
			if !ok || v.Sign() <= 0 {
				return nil, fmt.Errorf("invalid %s: must be a positive decimal number", limit.name)
			}
		}
	}

	// Validate and default scopes
	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes
	}
	for _, s := range scopes {
		if !ValidScopes[s] {
			return nil, ErrInvalidScope
		}
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
			Scopes:              scopes,
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

// Revoke revokes a session key and all descendant keys (cascading revocation)
func (m *Manager) Revoke(ctx context.Context, id string) error {
	key, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}

	now := time.Now()
	key.RevokedAt = &now
	if err := m.store.Update(ctx, key); err != nil {
		return err
	}

	// Audit: log revocation
	rootKeyID := key.RootKeyID
	if rootKeyID == "" {
		rootKeyID = key.ID
	}
	m.logDelegationEvent(ctx, &DelegationLogEntry{
		ParentKeyID:   key.ParentKeyID,
		ChildKeyID:    id,
		RootKeyID:     rootKeyID,
		RootOwnerAddr: key.OwnerAddr,
		Depth:         key.Depth,
		EventType:     DelegationEventRevoke,
		AncestorChain: m.buildAncestorChain(ctx, id),
	})

	// Cascade: revoke all children
	children, err := m.store.GetByParent(ctx, id)
	if err != nil {
		return nil // parent revoked successfully, children lookup failure is non-fatal
	}
	for _, child := range children {
		if child.RevokedAt == nil {
			// Audit: log cascade revocation for each child
			childRootKeyID := child.RootKeyID
			if childRootKeyID == "" {
				childRootKeyID = child.ID
			}
			m.logDelegationEvent(ctx, &DelegationLogEntry{
				ParentKeyID:   id,
				ChildKeyID:    child.ID,
				RootKeyID:     childRootKeyID,
				RootOwnerAddr: child.OwnerAddr,
				Depth:         child.Depth,
				Reason:        "parent revoked: " + id,
				EventType:     DelegationEventCascadeRevoke,
				AncestorChain: m.buildAncestorChain(ctx, child.ID),
			})
			_ = m.Revoke(ctx, child.ID) // best-effort recursive revocation
		}
	}

	return nil
}

// RotationGracePeriod is how long the old key remains usable after rotation.
const RotationGracePeriod = 5 * time.Minute

// RotateKey replaces an active session key with a new one.
// The new key inherits the remaining budget, same permissions, and the
// old key's position in the delegation tree. Children are re-parented
// atomically. The old key enters a grace period before becoming inactive.
func (m *Manager) RotateKey(ctx context.Context, oldKeyID, newPublicKey string) (*SessionKey, error) {
	oldKey, err := m.store.Get(ctx, oldKeyID)
	if err != nil {
		return nil, ErrKeyNotFound
	}

	if !oldKey.IsActive() {
		if oldKey.RevokedAt != nil {
			return nil, ErrKeyRevoked
		}
		return nil, ErrKeyExpired
	}

	if oldKey.RotatedToID != "" {
		return nil, ErrKeyAlreadyRotated
	}

	// Validate new public key format
	publicKey := strings.ToLower(newPublicKey)
	if !strings.HasPrefix(publicKey, "0x") || len(publicKey) != 42 {
		return nil, ErrInvalidPublicKey
	}

	// Compute remaining budget
	remaining := ""
	if oldKey.Permission.MaxTotal != "" {
		maxTotal, _ := usdc.Parse(oldKey.Permission.MaxTotal)
		spent, _ := usdc.Parse(oldKey.Usage.TotalSpent)
		rem := new(big.Int).Sub(maxTotal, spent)
		if rem.Sign() <= 0 {
			return nil, &ValidationError{Code: "no_budget", Message: "No remaining budget to rotate"}
		}
		remaining = usdc.Format(rem)
	}

	// Create new key with same permissions but remaining budget
	graceEnd := time.Now().Add(RotationGracePeriod)
	newKey := &SessionKey{
		ID:        idgen.WithPrefix("sk_"),
		OwnerAddr: oldKey.OwnerAddr,
		PublicKey: publicKey,
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxPerTransaction:   oldKey.Permission.MaxPerTransaction,
			MaxPerDay:           oldKey.Permission.MaxPerDay,
			MaxTotal:            remaining,
			ExpiresAt:           oldKey.Permission.ExpiresAt,
			ValidAfter:          oldKey.Permission.ValidAfter,
			AllowedRecipients:   oldKey.Permission.AllowedRecipients,
			AllowedServiceTypes: oldKey.Permission.AllowedServiceTypes,
			AllowAny:            oldKey.Permission.AllowAny,
			Scopes:              oldKey.Permission.Scopes,
			Label:               oldKey.Permission.Label,
		},
		Usage: SessionKeyUsage{
			TotalSpent:   "0",
			SpentToday:   "0",
			LastResetDay: time.Now().Format("2006-01-02"),
			LastNonce:    0,
		},
		ParentKeyID:     oldKey.ParentKeyID,
		Depth:           oldKey.Depth,
		RootKeyID:       oldKey.RootKeyID,
		DelegationLabel: oldKey.DelegationLabel,
		RotatedFromID:   oldKeyID,
	}

	if err := m.store.Create(ctx, newKey); err != nil {
		return nil, fmt.Errorf("failed to create rotated key: %w", err)
	}

	// Update old key with rotation info
	oldKey.RotatedToID = newKey.ID
	oldKey.RotationGraceEnd = &graceEnd
	if err := m.store.Update(ctx, oldKey); err != nil {
		return nil, fmt.Errorf("failed to mark old key as rotated: %w", err)
	}

	// Re-parent children atomically
	if err := m.store.ReParentChildren(ctx, oldKeyID, newKey.ID); err != nil {
		return nil, fmt.Errorf("failed to re-parent children: %w", err)
	}

	// Audit: log rotation
	rootKeyID := oldKey.RootKeyID
	if rootKeyID == "" {
		rootKeyID = oldKey.ID
	}
	m.logDelegationEvent(ctx, &DelegationLogEntry{
		ParentKeyID:   oldKeyID,
		ChildKeyID:    newKey.ID,
		RootKeyID:     rootKeyID,
		RootOwnerAddr: oldKey.OwnerAddr,
		Depth:         oldKey.Depth,
		MaxTotal:      remaining,
		Reason:        "rotated from " + oldKeyID,
		EventType:     DelegationEventRotate,
		AncestorChain: m.buildAncestorChain(ctx, oldKeyID),
	})

	return newKey, nil
}

// MaxDelegationDepth is the maximum allowed depth in a delegation chain
const MaxDelegationDepth = 5

// CreateDelegated creates a child session key delegated from a parent key.
// The request must be signed by the parent key's private key.
func (m *Manager) CreateDelegated(ctx context.Context, parentKeyID string, req *DelegateRequest) (*SessionKey, error) {
	// 1. Get parent key, verify it's active
	parent, err := m.store.Get(ctx, parentKeyID)
	if err != nil {
		return nil, ErrKeyNotFound
	}
	if !parent.IsActive() {
		return nil, ErrParentNotActive
	}

	// 2. Validate ancestor chain is still active
	if parent.ParentKeyID != "" {
		if err := m.ValidateAncestorChain(ctx, parent); err != nil {
			return nil, err
		}
	}

	// 3. Verify ECDSA signature
	message := CreateDelegationMessage(req.PublicKey, req.MaxTotal, req.Nonce, req.Timestamp)
	recoveredAddr, err := RecoverAddress(message, req.Signature)
	if err != nil {
		return nil, ErrInvalidSignature
	}
	if !strings.EqualFold(recoveredAddr, parent.PublicKey) {
		return nil, ErrSignatureMismatch
	}

	// 4. Check nonce freshness
	if req.Nonce <= parent.Usage.LastNonce {
		return nil, ErrNonceReused
	}

	// 5. Check timestamp freshness (within 5 minutes)
	now := time.Now().Unix()
	if now-req.Timestamp > 5*60 {
		return nil, ErrSignatureExpired
	}
	if req.Timestamp > now+60 {
		return nil, &ValidationError{Code: "invalid_timestamp", Message: "Signature timestamp is in the future"}
	}

	// 6. Check depth limit
	childDepth := parent.Depth + 1
	if childDepth > MaxDelegationDepth {
		return nil, ErrMaxDepthExceeded
	}

	// 7. Validate child limits are subset of parent's remaining budget,
	// accounting for budgets already allocated to existing children
	childMaxTotal, ok := usdc.Parse(req.MaxTotal)
	if !ok || childMaxTotal.Sign() <= 0 {
		return nil, &ValidationError{Code: "invalid_max_total", Message: "maxTotal must be a positive decimal number"}
	}

	if parent.Permission.MaxTotal != "" {
		parentMax, _ := usdc.Parse(parent.Permission.MaxTotal)
		parentSpent, _ := usdc.Parse(parent.Usage.TotalSpent)
		parentRemaining := new(big.Int).Sub(parentMax, parentSpent)

		// Sum up budget already committed to existing active children
		existingChildren, _ := m.store.GetByParent(ctx, parentKeyID)
		committedBudget := new(big.Int)
		for _, child := range existingChildren {
			if child.IsActive() && child.Permission.MaxTotal != "" {
				childMax, _ := usdc.Parse(child.Permission.MaxTotal)
				childSpent, _ := usdc.Parse(child.Usage.TotalSpent)
				// Uncommitted = allocated minus already spent (spent is already in parentSpent)
				uncommitted := new(big.Int).Sub(childMax, childSpent)
				if uncommitted.Sign() > 0 {
					committedBudget.Add(committedBudget, uncommitted)
				}
			}
		}

		available := new(big.Int).Sub(parentRemaining, committedBudget)
		if childMaxTotal.Cmp(available) > 0 {
			return nil, ErrChildExceedsParent
		}
	}

	if req.MaxPerTransaction != "" {
		childPerTx, ok := usdc.Parse(req.MaxPerTransaction)
		if !ok || childPerTx.Sign() <= 0 {
			return nil, &ValidationError{Code: "invalid_limit", Message: "maxPerTransaction must be a positive decimal number"}
		}
		if parent.Permission.MaxPerTransaction != "" {
			parentPerTx, _ := usdc.Parse(parent.Permission.MaxPerTransaction)
			if childPerTx.Cmp(parentPerTx) > 0 {
				return nil, ErrChildExceedsParent
			}
		}
	}

	if req.MaxPerDay != "" {
		childPerDay, ok := usdc.Parse(req.MaxPerDay)
		if !ok || childPerDay.Sign() <= 0 {
			return nil, &ValidationError{Code: "invalid_limit", Message: "maxPerDay must be a positive decimal number"}
		}
		if parent.Permission.MaxPerDay != "" {
			parentPerDay, _ := usdc.Parse(parent.Permission.MaxPerDay)
			if childPerDay.Cmp(parentPerDay) > 0 {
				return nil, ErrChildExceedsParent
			}
		}
	}

	// 8. Intersect AllowedServiceTypes (child can only narrow)
	childServiceTypes := toLower(req.AllowedServiceTypes)
	if !parent.Permission.AllowAny && len(parent.Permission.AllowedServiceTypes) > 0 {
		if len(childServiceTypes) == 0 {
			// Child inherits parent's restrictions
			childServiceTypes = parent.Permission.AllowedServiceTypes
		} else {
			// Intersect
			filtered := intersectStrings(childServiceTypes, parent.Permission.AllowedServiceTypes)
			if len(filtered) == 0 {
				return nil, ErrChildServiceNotAllowed
			}
			childServiceTypes = filtered
		}
	}

	// 9. Intersect AllowedRecipients
	childRecipients := toLower(req.AllowedRecipients)
	if !parent.Permission.AllowAny && len(parent.Permission.AllowedRecipients) > 0 {
		if len(childRecipients) == 0 {
			childRecipients = parent.Permission.AllowedRecipients
		} else {
			filtered := intersectStrings(childRecipients, parent.Permission.AllowedRecipients)
			if len(filtered) == 0 {
				return nil, ErrRecipientNotAllowed
			}
			childRecipients = filtered
		}
	}

	// 10. AllowAny: child can only be AllowAny if parent is AllowAny
	childAllowAny := req.AllowAny
	if childAllowAny && !parent.Permission.AllowAny {
		childAllowAny = false
		// If parent restricts and child has no explicit restrictions, inherit
		if len(childServiceTypes) == 0 && len(childRecipients) == 0 {
			childServiceTypes = parent.Permission.AllowedServiceTypes
			childRecipients = parent.Permission.AllowedRecipients
		}
	}
	// If parent is AllowAny and child has no explicit recipient restrictions,
	// the child inherits AllowAny for recipients (child can only narrow, never widen)
	if parent.Permission.AllowAny && !childAllowAny && len(childRecipients) == 0 {
		childAllowAny = true
	}

	// 10b. Intersect scopes (child can only narrow)
	parentScopes := parent.Permission.Scopes
	if len(parentScopes) == 0 {
		parentScopes = DefaultScopes
	}
	childScopes := req.Scopes
	if len(childScopes) == 0 {
		childScopes = parentScopes // inherit parent's scopes
	} else {
		// Validate all child scopes are recognized
		for _, s := range childScopes {
			if !ValidScopes[s] {
				return nil, ErrInvalidScope
			}
		}
		// Enforce subset: every child scope must exist in parent
		parentScopeSet := make(map[string]bool, len(parentScopes))
		for _, s := range parentScopes {
			parentScopeSet[s] = true
		}
		for _, s := range childScopes {
			if !parentScopeSet[s] {
				return nil, ErrChildScopeNotAllowed
			}
		}
	}

	// 11. Ensure child doesn't outlive parent
	var expiresAt time.Time
	if req.ExpiresIn != "" {
		duration, err := parseDuration(req.ExpiresIn)
		if err != nil {
			return nil, fmt.Errorf("invalid expiresIn format: %w", err)
		}
		expiresAt = time.Now().Add(duration)
	} else {
		expiresAt = parent.Permission.ExpiresAt // default: same as parent
	}
	if expiresAt.After(parent.Permission.ExpiresAt) {
		expiresAt = parent.Permission.ExpiresAt
	}

	// 12. Determine root key ID
	rootKeyID := parent.RootKeyID
	if rootKeyID == "" {
		rootKeyID = parent.ID // parent is root
	}

	// 13. Validate public key format
	publicKey := strings.ToLower(req.PublicKey)
	if !strings.HasPrefix(publicKey, "0x") || len(publicKey) != 42 {
		return nil, ErrInvalidPublicKey
	}

	// 14. Create child key
	childKey := &SessionKey{
		ID:        idgen.WithPrefix("sk_"),
		OwnerAddr: parent.OwnerAddr, // funds always come from root owner
		PublicKey: publicKey,
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxPerTransaction:   req.MaxPerTransaction,
			MaxPerDay:           req.MaxPerDay,
			MaxTotal:            req.MaxTotal,
			ExpiresAt:           expiresAt,
			AllowedRecipients:   childRecipients,
			AllowedServiceTypes: childServiceTypes,
			AllowAny:            childAllowAny,
			Scopes:              childScopes,
			Label:               req.DelegationLabel,
		},
		Usage: SessionKeyUsage{
			TotalSpent:   "0",
			SpentToday:   "0",
			LastResetDay: time.Now().Format("2006-01-02"),
			LastNonce:    0,
		},
		ParentKeyID:     parentKeyID,
		Depth:           childDepth,
		RootKeyID:       rootKeyID,
		DelegationLabel: req.DelegationLabel,
	}

	if err := m.store.Create(ctx, childKey); err != nil {
		return nil, fmt.Errorf("failed to create delegated key: %w", err)
	}

	// Audit: log delegation creation
	m.logDelegationEvent(ctx, &DelegationLogEntry{
		ParentKeyID:   parentKeyID,
		ChildKeyID:    childKey.ID,
		RootKeyID:     rootKeyID,
		RootOwnerAddr: parent.OwnerAddr,
		Depth:         childDepth,
		MaxTotal:      req.MaxTotal,
		Reason:        req.DelegationLabel,
		EventType:     DelegationEventCreate,
		AncestorChain: m.buildAncestorChain(ctx, parentKeyID),
	})

	// 15. Update parent nonce
	parent.Usage.LastNonce = req.Nonce
	if err := m.store.Update(ctx, parent); err != nil {
		// Child was created, nonce update failed.
		// Next delegation will still work with a higher nonce.
		_ = err
	}

	return childKey, nil
}

// RecordUsageWithCascade records usage on the key and cascades the spend to all ancestors.
// It validates ancestor budgets before incrementing to prevent overspend.
func (m *Manager) RecordUsageWithCascade(ctx context.Context, keyID string, amount string, nonce uint64) error {
	// Record on the child key itself
	if err := m.RecordUsage(ctx, keyID, amount, nonce); err != nil {
		return err
	}

	// Walk up the delegation chain, incrementing each ancestor's TotalSpent
	key, err := m.store.Get(ctx, keyID)
	if err != nil {
		return nil // child usage recorded, ancestor lookup failure is non-fatal
	}

	parentID := key.ParentKeyID
	amountBig, _ := usdc.Parse(amount)

	// NOTE: Caller (Transact handler) must hold locks on the entire ancestor
	// chain via LockKeyChain() to prevent concurrent sibling overspend.
	for parentID != "" {
		ancestor, err := m.store.Get(ctx, parentID)
		if err != nil {
			break
		}

		totalSpent, _ := usdc.Parse(ancestor.Usage.TotalSpent)
		newTotal := new(big.Int).Add(totalSpent, amountBig)

		// Validate ancestor budget before incrementing
		if ancestor.Permission.MaxTotal != "" {
			maxTotal, ok := usdc.Parse(ancestor.Permission.MaxTotal)
			if ok && newTotal.Cmp(maxTotal) > 0 {
				// Audit: log budget exceeded on ancestor
				ancestorRootKeyID := ancestor.RootKeyID
				if ancestorRootKeyID == "" {
					ancestorRootKeyID = ancestor.ID
				}
				m.logDelegationEvent(ctx, &DelegationLogEntry{
					ParentKeyID:   ancestor.ID,
					ChildKeyID:    keyID,
					RootKeyID:     ancestorRootKeyID,
					RootOwnerAddr: ancestor.OwnerAddr,
					Depth:         ancestor.Depth,
					MaxTotal:      ancestor.Permission.MaxTotal,
					Reason:        fmt.Sprintf("child %s spend %s would exceed ancestor %s budget %s (current: %s)", keyID, amount, ancestor.ID, ancestor.Permission.MaxTotal, ancestor.Usage.TotalSpent),
					EventType:     DelegationEventBudgetExceed,
				})
				return ErrExceedsTotal
			}
		}

		ancestor.Usage.TotalSpent = usdc.Format(newTotal)
		ancestor.Usage.TransactionCount++

		if err := m.store.Update(ctx, ancestor); err != nil {
			break
		}

		parentID = ancestor.ParentKeyID
	}

	return nil
}

// ValidateAncestorChain verifies that all ancestor keys in the delegation chain
// are still active and have sufficient budget for the given amount.
// If amount is empty, only activity is checked (used during delegation creation).
func (m *Manager) ValidateAncestorChain(ctx context.Context, key *SessionKey, amount ...string) error {
	var amountBig *big.Int
	if len(amount) > 0 && amount[0] != "" {
		amountBig, _ = usdc.Parse(amount[0])
	}

	parentID := key.ParentKeyID
	for parentID != "" {
		ancestor, err := m.store.Get(ctx, parentID)
		if err != nil {
			return ErrAncestorInvalid
		}
		if !ancestor.IsActive() {
			return ErrAncestorInvalid
		}

		// Check ancestor budget if amount provided
		if amountBig != nil && ancestor.Permission.MaxTotal != "" {
			maxTotal, ok := usdc.Parse(ancestor.Permission.MaxTotal)
			if ok {
				spent, _ := usdc.Parse(ancestor.Usage.TotalSpent)
				newTotal := new(big.Int).Add(spent, amountBig)
				if newTotal.Cmp(maxTotal) > 0 {
					return ErrExceedsTotal
				}
			}
		}

		parentID = ancestor.ParentKeyID
	}
	return nil
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
			Code:    "invalid_signature",
			Message: "Signature does not match session key",
		}
	}

	// 2. Check nonce (must be greater than last used)
	if req.Nonce <= key.Usage.LastNonce {
		return &ValidationError{
			Code:    "nonce_reused",
			Message: "Nonce has already been used",
		}
	}

	// 3. Check timestamp freshness (within 5 minutes)
	now := time.Now().Unix()
	maxAge := int64(5 * 60) // 5 minutes
	if now-req.Timestamp > maxAge {
		return &ValidationError{
			Code:    "signature_expired",
			Message: "Signature has expired",
		}
	}

	// Don't allow future timestamps (with small tolerance)
	if req.Timestamp > now+60 {
		return &ValidationError{
			Code:    "invalid_timestamp",
			Message: "Signature timestamp is in the future",
		}
	}

	// 4. Validate ancestor chain for delegated keys (including ancestor budgets)
	if key.ParentKeyID != "" {
		if err := m.ValidateAncestorChain(ctx, key, req.Amount); err != nil {
			return err
		}
	}

	// 5. Validate permissions
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

	if err := m.store.Update(ctx, key); err != nil {
		return err
	}

	// Update rate_limit window counters on attached policies
	if m.policyStore != nil {
		recordPolicyUsage(ctx, m.policyStore, keyID)
	}

	return nil
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

	// Evaluate attached policies (rate_limit, time_window, cooldown, tx_count)
	if m.policyStore != nil {
		if err := evaluatePolicies(ctx, m.policyStore, key); err != nil {
			return err
		}
	}

	return nil
}

// ScopeChecker verifies that a session key possesses a required scope.
// This interface is consumed by other packages (streams, escrow) to enforce
// fine-grained capability control without importing the full Manager.
type ScopeChecker interface {
	ValidateScope(ctx context.Context, keyID, scope string) error
}

// ValidateScope checks that the session key identified by keyID has the given scope.
// Returns ErrScopeNotAllowed if the key lacks the scope, ErrKeyNotFound if the key
// does not exist.
func (m *Manager) ValidateScope(ctx context.Context, keyID, scope string) error {
	key, err := m.store.Get(ctx, keyID)
	if err != nil {
		return ErrKeyNotFound
	}
	if !key.HasScope(scope) {
		return ErrScopeNotAllowed
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

// buildAncestorChain returns the list of key IDs from the given key up to the root.
func (m *Manager) buildAncestorChain(ctx context.Context, keyID string) []string {
	var chain []string
	current := keyID
	depth := 0
	for current != "" && depth < MaxDelegationDepth+2 {
		chain = append(chain, current)
		key, err := m.store.Get(ctx, current)
		if err != nil {
			break
		}
		current = key.ParentKeyID
		depth++
	}
	return chain
}

// logDelegationEvent is a best-effort audit logger; failures are silently ignored.
func (m *Manager) logDelegationEvent(ctx context.Context, entry *DelegationLogEntry) {
	if m.auditLog == nil {
		return
	}
	_ = m.auditLog.LogEvent(ctx, entry)
}

// intersectStrings returns elements present in both slices (case-insensitive)
func intersectStrings(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, s := range b {
		set[strings.ToLower(s)] = true
	}
	var result []string
	for _, s := range a {
		if set[strings.ToLower(s)] {
			result = append(result, strings.ToLower(s))
		}
	}
	return result
}
