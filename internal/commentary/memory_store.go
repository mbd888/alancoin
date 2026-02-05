package commentary

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory implementation of Store for testing
type MemoryStore struct {
	mu           sync.RWMutex
	verbalAgents map[string]*VerbalAgent    // address -> agent
	comments     map[string]*Comment        // id -> comment
	likes        map[string]map[string]bool // commentID -> agentAddr -> liked
	follows      map[string]map[string]bool // verbalAgentAddr -> followerAddr -> following
}

// NewMemoryStore creates a new in-memory commentary store
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		verbalAgents: make(map[string]*VerbalAgent),
		comments:     make(map[string]*Comment),
		likes:        make(map[string]map[string]bool),
		follows:      make(map[string]map[string]bool),
	}
}

// Compile-time interface check
var _ Store = (*MemoryStore)(nil)

// RegisterVerbalAgent registers an agent for commentary
func (m *MemoryStore) RegisterVerbalAgent(ctx context.Context, agent *VerbalAgent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(agent.Address)
	if agent.RegisteredAt.IsZero() {
		agent.RegisteredAt = time.Now()
	}
	agent.Address = addr
	m.verbalAgents[addr] = agent
	return nil
}

// GetVerbalAgent retrieves a verbal agent
func (m *MemoryStore) GetVerbalAgent(ctx context.Context, address string) (*VerbalAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.verbalAgents[strings.ToLower(address)]
	if !ok {
		return nil, ErrNotVerbalAgent
	}
	copy := *agent
	return &copy, nil
}

// ListVerbalAgents returns verbal agents ordered by followers
func (m *MemoryStore) ListVerbalAgents(ctx context.Context, limit, offset int) ([]*VerbalAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var agents []*VerbalAgent
	for _, agent := range m.verbalAgents {
		copy := *agent
		agents = append(agents, &copy)
	}

	// Sort by followers descending
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Followers > agents[j].Followers
	})

	// Apply pagination
	if offset >= len(agents) {
		return []*VerbalAgent{}, nil
	}
	end := offset + limit
	if end > len(agents) {
		end = len(agents)
	}
	return agents[offset:end], nil
}

// UpdateVerbalAgent updates a verbal agent
func (m *MemoryStore) UpdateVerbalAgent(ctx context.Context, agent *VerbalAgent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr := strings.ToLower(agent.Address)
	if _, ok := m.verbalAgents[addr]; !ok {
		return ErrNotVerbalAgent
	}
	agent.Address = addr
	m.verbalAgents[addr] = agent
	return nil
}

// PostComment creates a new comment
func (m *MemoryStore) PostComment(ctx context.Context, comment *Comment) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if comment.ID == "" {
		comment.ID = m.generateID("cmt_")
	}
	if comment.CreatedAt.IsZero() {
		comment.CreatedAt = time.Now()
	}
	m.comments[comment.ID] = comment
	return nil
}

// GetComment retrieves a comment by ID
func (m *MemoryStore) GetComment(ctx context.Context, id string) (*Comment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	comment, ok := m.comments[id]
	if !ok {
		return nil, ErrCommentEmpty
	}
	copy := *comment
	return &copy, nil
}

// ListComments returns comments with optional filters
func (m *MemoryStore) ListComments(ctx context.Context, opts ListOptions) ([]*Comment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var comments []*Comment
	for _, comment := range m.comments {
		// Apply filters
		if opts.Type != "" && comment.Type != opts.Type {
			continue
		}
		if opts.AuthorAddr != "" && !strings.EqualFold(comment.AuthorAddr, opts.AuthorAddr) {
			continue
		}
		if opts.Since != nil && comment.CreatedAt.Before(*opts.Since) {
			continue
		}
		copy := *comment
		comments = append(comments, &copy)
	}

	// Sort by created_at descending
	sort.Slice(comments, func(i, j int) bool {
		return comments[i].CreatedAt.After(comments[j].CreatedAt)
	})

	// Apply pagination
	if opts.Offset >= len(comments) {
		return []*Comment{}, nil
	}
	end := opts.Offset + opts.Limit
	if opts.Limit == 0 {
		end = len(comments)
	}
	if end > len(comments) {
		end = len(comments)
	}
	return comments[opts.Offset:end], nil
}

// ListByAuthor returns comments by a specific author
func (m *MemoryStore) ListByAuthor(ctx context.Context, authorAddr string, limit int) ([]*Comment, error) {
	return m.ListComments(ctx, ListOptions{
		AuthorAddr: authorAddr,
		Limit:      limit,
	})
}

// ListByReference returns comments referencing a specific entity
func (m *MemoryStore) ListByReference(ctx context.Context, refType, refID string, limit int) ([]*Comment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var comments []*Comment
	for _, comment := range m.comments {
		for _, ref := range comment.References {
			if ref.Type == refType && ref.ID == refID {
				copy := *comment
				comments = append(comments, &copy)
				break
			}
		}
	}

	// Sort by created_at descending
	sort.Slice(comments, func(i, j int) bool {
		return comments[i].CreatedAt.After(comments[j].CreatedAt)
	})

	if limit > 0 && len(comments) > limit {
		comments = comments[:limit]
	}
	return comments, nil
}

// LikeComment adds a like
func (m *MemoryStore) LikeComment(ctx context.Context, commentID, agentAddr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	comment, ok := m.comments[commentID]
	if !ok {
		return ErrCommentEmpty
	}

	if m.likes[commentID] == nil {
		m.likes[commentID] = make(map[string]bool)
	}

	addr := strings.ToLower(agentAddr)
	if !m.likes[commentID][addr] {
		m.likes[commentID][addr] = true
		comment.Likes++
	}
	return nil
}

// UnlikeComment removes a like
func (m *MemoryStore) UnlikeComment(ctx context.Context, commentID, agentAddr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	comment, ok := m.comments[commentID]
	if !ok {
		return ErrCommentEmpty
	}

	addr := strings.ToLower(agentAddr)
	if m.likes[commentID] != nil && m.likes[commentID][addr] {
		delete(m.likes[commentID], addr)
		comment.Likes--
	}
	return nil
}

// Follow creates a follow relationship
func (m *MemoryStore) Follow(ctx context.Context, followerAddr, verbalAgentAddr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	verbalAddr := strings.ToLower(verbalAgentAddr)
	agent, ok := m.verbalAgents[verbalAddr]
	if !ok {
		return ErrNotVerbalAgent
	}

	if m.follows[verbalAddr] == nil {
		m.follows[verbalAddr] = make(map[string]bool)
	}

	follower := strings.ToLower(followerAddr)
	if !m.follows[verbalAddr][follower] {
		m.follows[verbalAddr][follower] = true
		agent.Followers++
	}
	return nil
}

// Unfollow removes a follow relationship
func (m *MemoryStore) Unfollow(ctx context.Context, followerAddr, verbalAgentAddr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	verbalAddr := strings.ToLower(verbalAgentAddr)
	agent, ok := m.verbalAgents[verbalAddr]
	if !ok {
		return ErrNotVerbalAgent
	}

	follower := strings.ToLower(followerAddr)
	if m.follows[verbalAddr] != nil && m.follows[verbalAddr][follower] {
		delete(m.follows[verbalAddr], follower)
		agent.Followers--
	}
	return nil
}

// GetFollowers returns addresses following a verbal agent
func (m *MemoryStore) GetFollowers(ctx context.Context, verbalAgentAddr string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	verbalAddr := strings.ToLower(verbalAgentAddr)
	followers := m.follows[verbalAddr]
	if followers == nil {
		return []string{}, nil
	}

	var result []string
	for addr := range followers {
		result = append(result, addr)
	}
	return result, nil
}

// GetFollowing returns verbal agents an address follows
func (m *MemoryStore) GetFollowing(ctx context.Context, agentAddr string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addr := strings.ToLower(agentAddr)
	var following []string
	for verbalAddr, followers := range m.follows {
		if followers[addr] {
			following = append(following, verbalAddr)
		}
	}
	return following, nil
}

func (m *MemoryStore) generateID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
