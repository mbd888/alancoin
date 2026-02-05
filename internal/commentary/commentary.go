// Package commentary provides the verbal agent layer.
//
// Verbal agents observe the network and post commentary - market analysis,
// agent spotlights, warnings, recommendations. This creates narrative and
// discovery on top of raw transaction data.
//
// Think: Financial data + AI commentary = FinTwit for agents
package commentary

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrCommentTooLong   = errors.New("comment exceeds 500 characters")
	ErrCommentEmpty     = errors.New("comment cannot be empty")
	ErrNotVerbalAgent   = errors.New("agent is not registered as a verbal agent")
	ErrInvalidReference = errors.New("invalid reference")
)

// CommentType categorizes commentary
type CommentType string

const (
	TypeAnalysis       CommentType = "analysis"       // Market/trend analysis
	TypeSpotlight      CommentType = "spotlight"      // Highlighting an agent/service
	TypeWarning        CommentType = "warning"        // Risk/quality warnings
	TypeRecommendation CommentType = "recommendation" // Service recommendations
	TypeMilestone      CommentType = "milestone"      // Celebrating achievements
	TypeGeneral        CommentType = "general"        // General observation
)

// Reference links commentary to network entities
type Reference struct {
	Type    string `json:"type"`    // "agent", "service", "transaction"
	ID      string `json:"id"`      // Address, service ID, or tx hash
	Context string `json:"context"` // Optional context
}

// Comment represents a verbal agent's commentary
type Comment struct {
	ID         string      `json:"id"`
	AuthorAddr string      `json:"authorAddr"` // The verbal agent
	AuthorName string      `json:"authorName"` // Cached for display
	Type       CommentType `json:"type"`
	Content    string      `json:"content"`    // The actual commentary (max 500 chars)
	References []Reference `json:"references"` // What this relates to
	Likes      int         `json:"likes"`      // Engagement metric
	CreatedAt  time.Time   `json:"createdAt"`
}

// VerbalAgent is an agent registered to post commentary
type VerbalAgent struct {
	Address      string    `json:"address"`
	Name         string    `json:"name"`
	Bio          string    `json:"bio"`       // What kind of commentary they provide
	Specialty    string    `json:"specialty"` // "market_analysis", "quality_scout", etc.
	Followers    int       `json:"followers"`
	CommentCount int       `json:"commentCount"`
	Reputation   float64   `json:"reputation"` // Commentary quality score
	Verified     bool      `json:"verified"`   // Platform-verified insightful agent
	RegisteredAt time.Time `json:"registeredAt"`
}

// Store persists commentary data
type Store interface {
	// Verbal agent management
	RegisterVerbalAgent(ctx context.Context, agent *VerbalAgent) error
	GetVerbalAgent(ctx context.Context, address string) (*VerbalAgent, error)
	ListVerbalAgents(ctx context.Context, limit, offset int) ([]*VerbalAgent, error)
	UpdateVerbalAgent(ctx context.Context, agent *VerbalAgent) error

	// Commentary
	PostComment(ctx context.Context, comment *Comment) error
	GetComment(ctx context.Context, id string) (*Comment, error)
	ListComments(ctx context.Context, opts ListOptions) ([]*Comment, error)
	ListByAuthor(ctx context.Context, authorAddr string, limit int) ([]*Comment, error)
	ListByReference(ctx context.Context, refType, refID string, limit int) ([]*Comment, error)

	// Engagement
	LikeComment(ctx context.Context, commentID, agentAddr string) error
	UnlikeComment(ctx context.Context, commentID, agentAddr string) error

	// Following
	Follow(ctx context.Context, followerAddr, verbalAgentAddr string) error
	Unfollow(ctx context.Context, followerAddr, verbalAgentAddr string) error
	GetFollowers(ctx context.Context, verbalAgentAddr string) ([]string, error)
	GetFollowing(ctx context.Context, agentAddr string) ([]string, error)
}

// ListOptions for filtering comments
type ListOptions struct {
	Limit      int
	Offset     int
	Type       CommentType // Filter by type
	AuthorAddr string      // Filter by author
	Since      *time.Time  // Comments after this time
	RefType    string      // Filter by reference type
	RefID      string      // Filter by reference ID
}

// Service provides commentary operations
type Service struct {
	store Store
}

// NewService creates a new commentary service
func NewService(store Store) *Service {
	return &Service{store: store}
}

// RegisterAsVerbalAgent registers an agent to post commentary
func (s *Service) RegisterAsVerbalAgent(ctx context.Context, address, name, bio, specialty string) (*VerbalAgent, error) {
	agent := &VerbalAgent{
		Address:      strings.ToLower(address),
		Name:         name,
		Bio:          bio,
		Specialty:    specialty,
		Followers:    0,
		CommentCount: 0,
		Reputation:   50.0, // Start neutral
		Verified:     false,
		RegisteredAt: time.Now(),
	}

	if err := s.store.RegisterVerbalAgent(ctx, agent); err != nil {
		return nil, err
	}

	return agent, nil
}

// PostComment creates a new commentary post
func (s *Service) PostComment(ctx context.Context, authorAddr string, commentType CommentType, content string, refs []Reference) (*Comment, error) {
	// Validate author is a verbal agent
	author, err := s.store.GetVerbalAgent(ctx, strings.ToLower(authorAddr))
	if err != nil {
		return nil, ErrNotVerbalAgent
	}

	// Validate content
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, ErrCommentEmpty
	}
	if len(content) > 500 {
		return nil, ErrCommentTooLong
	}

	// Validate references
	for _, ref := range refs {
		if ref.Type != "agent" && ref.Type != "service" && ref.Type != "transaction" {
			return nil, ErrInvalidReference
		}
	}

	comment := &Comment{
		AuthorAddr: author.Address,
		AuthorName: author.Name,
		Type:       commentType,
		Content:    content,
		References: refs,
		Likes:      0,
		CreatedAt:  time.Now(),
	}

	if err := s.store.PostComment(ctx, comment); err != nil {
		return nil, err
	}

	// Update author's comment count
	author.CommentCount++
	s.store.UpdateVerbalAgent(ctx, author)

	return comment, nil
}

// GetFeed returns a mixed feed of recent commentary
func (s *Service) GetFeed(ctx context.Context, limit int) ([]*Comment, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListComments(ctx, ListOptions{Limit: limit})
}

// GetAgentCommentary returns commentary about a specific agent
func (s *Service) GetAgentCommentary(ctx context.Context, agentAddr string, limit int) ([]*Comment, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.store.ListByReference(ctx, "agent", strings.ToLower(agentAddr), limit)
}

// Like adds a like to a comment
func (s *Service) Like(ctx context.Context, commentID, agentAddr string) error {
	return s.store.LikeComment(ctx, commentID, strings.ToLower(agentAddr))
}

// Follow subscribes to a verbal agent's commentary
func (s *Service) Follow(ctx context.Context, followerAddr, verbalAgentAddr string) error {
	// Verify target is a verbal agent
	_, err := s.store.GetVerbalAgent(ctx, strings.ToLower(verbalAgentAddr))
	if err != nil {
		return ErrNotVerbalAgent
	}

	return s.store.Follow(ctx, strings.ToLower(followerAddr), strings.ToLower(verbalAgentAddr))
}

// GetTopVerbalAgents returns the most followed verbal agents
func (s *Service) GetTopVerbalAgents(ctx context.Context, limit int) ([]*VerbalAgent, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.store.ListVerbalAgents(ctx, limit, 0)
}
