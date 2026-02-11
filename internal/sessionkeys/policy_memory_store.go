package sessionkeys

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// PolicyMemoryStore is an in-memory implementation of PolicyStore.
type PolicyMemoryStore struct {
	mu          sync.RWMutex
	policies    map[string]*Policy             // keyed by policy ID
	attachments map[string][]*PolicyAttachment // keyed by session key ID
}

// NewPolicyMemoryStore creates a new in-memory policy store.
func NewPolicyMemoryStore() *PolicyMemoryStore {
	return &PolicyMemoryStore{
		policies:    make(map[string]*Policy),
		attachments: make(map[string][]*PolicyAttachment),
	}
}

func (s *PolicyMemoryStore) CreatePolicy(_ context.Context, policy *Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.policies[policy.ID]; exists {
		return ErrPolicyAlreadyExists
	}

	cp := copyPolicy(policy)
	s.policies[cp.ID] = cp
	return nil
}

func (s *PolicyMemoryStore) GetPolicy(_ context.Context, id string) (*Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.policies[id]
	if !ok {
		return nil, ErrPolicyNotFound
	}
	return copyPolicy(p), nil
}

func (s *PolicyMemoryStore) ListPolicies(_ context.Context, ownerAddr string) ([]*Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ownerAddr = strings.ToLower(ownerAddr)
	var result []*Policy
	for _, p := range s.policies {
		if strings.ToLower(p.OwnerAddr) == ownerAddr {
			result = append(result, copyPolicy(p))
		}
	}
	return result, nil
}

func (s *PolicyMemoryStore) UpdatePolicy(_ context.Context, policy *Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.policies[policy.ID]; !ok {
		return ErrPolicyNotFound
	}

	cp := copyPolicy(policy)
	s.policies[cp.ID] = cp
	return nil
}

func (s *PolicyMemoryStore) DeletePolicy(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.policies[id]; !ok {
		return ErrPolicyNotFound
	}

	delete(s.policies, id)

	// Remove all attachments referencing this policy.
	for keyID, atts := range s.attachments {
		var kept []*PolicyAttachment
		for _, a := range atts {
			if a.PolicyID != id {
				kept = append(kept, a)
			}
		}
		if len(kept) == 0 {
			delete(s.attachments, keyID)
		} else {
			s.attachments[keyID] = kept
		}
	}

	return nil
}

func (s *PolicyMemoryStore) AttachPolicy(_ context.Context, att *PolicyAttachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Prevent duplicate attachment.
	for _, existing := range s.attachments[att.SessionKeyID] {
		if existing.PolicyID == att.PolicyID {
			return ErrPolicyAlreadyExists
		}
	}

	cp := copyAttachment(att)
	s.attachments[att.SessionKeyID] = append(s.attachments[att.SessionKeyID], cp)
	return nil
}

func (s *PolicyMemoryStore) DetachPolicy(_ context.Context, sessionKeyID, policyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	atts, ok := s.attachments[sessionKeyID]
	if !ok {
		return ErrPolicyNotFound
	}

	var kept []*PolicyAttachment
	found := false
	for _, a := range atts {
		if a.PolicyID == policyID {
			found = true
			continue
		}
		kept = append(kept, a)
	}
	if !found {
		return ErrPolicyNotFound
	}

	if len(kept) == 0 {
		delete(s.attachments, sessionKeyID)
	} else {
		s.attachments[sessionKeyID] = kept
	}
	return nil
}

func (s *PolicyMemoryStore) GetAttachments(_ context.Context, sessionKeyID string) ([]*PolicyAttachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	atts := s.attachments[sessionKeyID]
	result := make([]*PolicyAttachment, len(atts))
	for i, a := range atts {
		result[i] = copyAttachment(a)
	}
	return result, nil
}

func (s *PolicyMemoryStore) UpdateAttachment(_ context.Context, att *PolicyAttachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	atts, ok := s.attachments[att.SessionKeyID]
	if !ok {
		return ErrPolicyNotFound
	}

	for i, a := range atts {
		if a.PolicyID == att.PolicyID {
			atts[i] = copyAttachment(att)
			return nil
		}
	}
	return ErrPolicyNotFound
}

// --- deep-copy helpers ---

func copyPolicy(p *Policy) *Policy {
	cp := *p
	if p.Rules != nil {
		cp.Rules = make([]Rule, len(p.Rules))
		for i, r := range p.Rules {
			cp.Rules[i] = Rule{Type: r.Type}
			if r.Params != nil {
				cp.Rules[i].Params = make(json.RawMessage, len(r.Params))
				copy(cp.Rules[i].Params, r.Params)
			}
		}
	}
	return &cp
}

func copyAttachment(a *PolicyAttachment) *PolicyAttachment {
	cp := *a
	if a.RuleState != nil {
		cp.RuleState = make(json.RawMessage, len(a.RuleState))
		copy(cp.RuleState, a.RuleState)
	}
	return &cp
}
