package commentary

import (
	"context"
	"strings"
	"testing"
)

func TestRegisterAsVerbalAgent(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	agent, err := svc.RegisterAsVerbalAgent(ctx, "0xAgent1", "MarketBot", "Market analysis", "market_analysis")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if agent.Address != "0xagent1" {
		t.Errorf("Expected lowercased address, got %s", agent.Address)
	}
	if agent.Name != "MarketBot" {
		t.Errorf("Expected name MarketBot, got %s", agent.Name)
	}
	if agent.Reputation != 50.0 {
		t.Errorf("Expected default reputation 50, got %f", agent.Reputation)
	}
	if agent.Verified {
		t.Error("New agent should not be verified")
	}
}

func TestPostComment_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")

	comment, err := svc.PostComment(ctx, "0xAuthor", TypeAnalysis, "The market is bullish today", []Reference{
		{Type: "agent", ID: "0xTarget", Context: "Price increase"},
	})
	if err != nil {
		t.Fatalf("PostComment failed: %v", err)
	}

	if comment.ID == "" {
		t.Error("Expected non-empty comment ID")
	}
	if comment.AuthorAddr != "0xauthor" {
		t.Errorf("Expected lowercased author, got %s", comment.AuthorAddr)
	}
	if comment.Type != TypeAnalysis {
		t.Errorf("Expected type analysis, got %s", comment.Type)
	}
	if comment.Content != "The market is bullish today" {
		t.Error("Content mismatch")
	}
	if len(comment.References) != 1 {
		t.Errorf("Expected 1 reference, got %d", len(comment.References))
	}
	if comment.Likes != 0 {
		t.Errorf("Expected 0 likes, got %d", comment.Likes)
	}
}

func TestPostComment_NotVerbalAgent(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	_, err := svc.PostComment(ctx, "0xNobody", TypeGeneral, "Hello", nil)
	if err != ErrNotVerbalAgent {
		t.Errorf("Expected ErrNotVerbalAgent, got %v", err)
	}
}

func TestPostComment_EmptyContent(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")

	_, err := svc.PostComment(ctx, "0xAuthor", TypeGeneral, "", nil)
	if err != ErrCommentEmpty {
		t.Errorf("Expected ErrCommentEmpty, got %v", err)
	}
}

func TestPostComment_WhitespaceOnlyContent(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")

	_, err := svc.PostComment(ctx, "0xAuthor", TypeGeneral, "   \t\n  ", nil)
	if err != ErrCommentEmpty {
		t.Errorf("Expected ErrCommentEmpty for whitespace-only, got %v", err)
	}
}

func TestPostComment_TooLong(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")

	longContent := strings.Repeat("a", 501)
	_, err := svc.PostComment(ctx, "0xAuthor", TypeGeneral, longContent, nil)
	if err != ErrCommentTooLong {
		t.Errorf("Expected ErrCommentTooLong, got %v", err)
	}
}

func TestPostComment_ExactlyMaxLength(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")

	exactContent := strings.Repeat("b", 500)
	comment, err := svc.PostComment(ctx, "0xAuthor", TypeGeneral, exactContent, nil)
	if err != nil {
		t.Fatalf("Expected success for 500 char comment, got %v", err)
	}
	if len(comment.Content) != 500 {
		t.Errorf("Expected 500 chars, got %d", len(comment.Content))
	}
}

func TestPostComment_InvalidReference(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")

	_, err := svc.PostComment(ctx, "0xAuthor", TypeGeneral, "Hello", []Reference{
		{Type: "invalid_type", ID: "123"},
	})
	if err != ErrInvalidReference {
		t.Errorf("Expected ErrInvalidReference, got %v", err)
	}
}

func TestPostComment_ValidReferenceTypes(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")

	for _, refType := range []string{"agent", "service", "transaction"} {
		_, err := svc.PostComment(ctx, "0xAuthor", TypeGeneral, "Test "+refType, []Reference{
			{Type: refType, ID: "test-id"},
		})
		if err != nil {
			t.Errorf("Expected success for reference type %q, got %v", refType, err)
		}
	}
}

func TestPostComment_IncrementsCommentCount(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")

	svc.PostComment(ctx, "0xAuthor", TypeGeneral, "Comment 1", nil)
	svc.PostComment(ctx, "0xAuthor", TypeGeneral, "Comment 2", nil)
	svc.PostComment(ctx, "0xAuthor", TypeGeneral, "Comment 3", nil)

	agent, _ := store.GetVerbalAgent(ctx, "0xauthor")
	if agent.CommentCount != 3 {
		t.Errorf("Expected comment count 3, got %d", agent.CommentCount)
	}
}

func TestGetFeed(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xA", "AgentA", "", "")
	svc.RegisterAsVerbalAgent(ctx, "0xB", "AgentB", "", "")

	svc.PostComment(ctx, "0xA", TypeAnalysis, "Analysis from A", nil)
	svc.PostComment(ctx, "0xB", TypeWarning, "Warning from B", nil)
	svc.PostComment(ctx, "0xA", TypeSpotlight, "Spotlight from A", nil)

	feed, err := svc.GetFeed(ctx, 10)
	if err != nil {
		t.Fatalf("GetFeed failed: %v", err)
	}

	if len(feed) != 3 {
		t.Errorf("Expected 3 comments, got %d", len(feed))
	}
}

func TestGetFeed_DefaultLimit(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	// Zero/negative limit should default to 50
	feed, err := svc.GetFeed(ctx, 0)
	if err != nil {
		t.Fatalf("GetFeed failed: %v", err)
	}
	_ = feed // Just verify no panic
}

func TestGetAgentCommentary(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")

	svc.PostComment(ctx, "0xAuthor", TypeSpotlight, "About Agent1", []Reference{
		{Type: "agent", ID: "0xagent1"},
	})
	svc.PostComment(ctx, "0xAuthor", TypeSpotlight, "About Agent2", []Reference{
		{Type: "agent", ID: "0xagent2"},
	})
	svc.PostComment(ctx, "0xAuthor", TypeSpotlight, "Also about Agent1", []Reference{
		{Type: "agent", ID: "0xagent1"},
	})

	comments, err := svc.GetAgentCommentary(ctx, "0xagent1", 10)
	if err != nil {
		t.Fatalf("GetAgentCommentary failed: %v", err)
	}

	if len(comments) != 2 {
		t.Errorf("Expected 2 comments about agent1, got %d", len(comments))
	}
}

func TestLike(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xAuthor", "Author", "", "")
	comment, _ := svc.PostComment(ctx, "0xAuthor", TypeGeneral, "Likeable post", nil)

	err := svc.Like(ctx, comment.ID, "0xLiker1")
	if err != nil {
		t.Fatalf("Like failed: %v", err)
	}

	// Check count
	c, _ := store.GetComment(ctx, comment.ID)
	if c.Likes != 1 {
		t.Errorf("Expected 1 like, got %d", c.Likes)
	}

	// Double like should not double count
	svc.Like(ctx, comment.ID, "0xLiker1")
	c, _ = store.GetComment(ctx, comment.ID)
	if c.Likes != 1 {
		t.Errorf("Expected 1 like (no double), got %d", c.Likes)
	}

	// Different liker
	svc.Like(ctx, comment.ID, "0xLiker2")
	c, _ = store.GetComment(ctx, comment.ID)
	if c.Likes != 2 {
		t.Errorf("Expected 2 likes, got %d", c.Likes)
	}
}

func TestFollow(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xVerbal", "VerbalAgent", "", "")

	err := svc.Follow(ctx, "0xFollower1", "0xVerbal")
	if err != nil {
		t.Fatalf("Follow failed: %v", err)
	}

	// Verify follower count
	agent, _ := store.GetVerbalAgent(ctx, "0xverbal")
	if agent.Followers != 1 {
		t.Errorf("Expected 1 follower, got %d", agent.Followers)
	}

	// Double follow
	svc.Follow(ctx, "0xFollower1", "0xVerbal")
	agent, _ = store.GetVerbalAgent(ctx, "0xverbal")
	if agent.Followers != 1 {
		t.Errorf("Expected 1 follower (no double), got %d", agent.Followers)
	}
}

func TestFollow_NonVerbalAgent(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	err := svc.Follow(ctx, "0xFollower", "0xNonExistent")
	if err != ErrNotVerbalAgent {
		t.Errorf("Expected ErrNotVerbalAgent, got %v", err)
	}
}

func TestUnfollow(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xVerbal", "VerbalAgent", "", "")
	store.Follow(ctx, "0xfollower", "0xverbal")

	agent, _ := store.GetVerbalAgent(ctx, "0xverbal")
	if agent.Followers != 1 {
		t.Fatalf("Setup: expected 1 follower, got %d", agent.Followers)
	}

	store.Unfollow(ctx, "0xfollower", "0xverbal")
	agent, _ = store.GetVerbalAgent(ctx, "0xverbal")
	if agent.Followers != 0 {
		t.Errorf("Expected 0 followers after unfollow, got %d", agent.Followers)
	}
}

func TestGetTopVerbalAgents(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	svc.RegisterAsVerbalAgent(ctx, "0xA", "AgentA", "", "")
	svc.RegisterAsVerbalAgent(ctx, "0xB", "AgentB", "", "")
	svc.RegisterAsVerbalAgent(ctx, "0xC", "AgentC", "", "")

	// Give followers
	store.Follow(ctx, "0x1", "0xa")
	store.Follow(ctx, "0x2", "0xa")
	store.Follow(ctx, "0x3", "0xa") // A has 3
	store.Follow(ctx, "0x1", "0xc") // C has 1

	agents, err := svc.GetTopVerbalAgents(ctx, 10)
	if err != nil {
		t.Fatalf("GetTopVerbalAgents failed: %v", err)
	}

	if len(agents) != 3 {
		t.Fatalf("Expected 3 agents, got %d", len(agents))
	}

	// Should be sorted by followers descending
	if agents[0].Address != "0xa" {
		t.Errorf("Expected first agent to be 0xa (3 followers), got %s", agents[0].Address)
	}
}

func TestMemoryStore_GetFollowers(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.RegisterVerbalAgent(ctx, &VerbalAgent{Address: "0xverbal", Name: "V"})
	store.Follow(ctx, "0xf1", "0xverbal")
	store.Follow(ctx, "0xf2", "0xverbal")

	followers, err := store.GetFollowers(ctx, "0xverbal")
	if err != nil {
		t.Fatalf("GetFollowers failed: %v", err)
	}

	if len(followers) != 2 {
		t.Errorf("Expected 2 followers, got %d", len(followers))
	}
}

func TestMemoryStore_GetFollowing(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.RegisterVerbalAgent(ctx, &VerbalAgent{Address: "0xva", Name: "VA"})
	store.RegisterVerbalAgent(ctx, &VerbalAgent{Address: "0xvb", Name: "VB"})

	store.Follow(ctx, "0xfollower", "0xva")
	store.Follow(ctx, "0xfollower", "0xvb")

	following, err := store.GetFollowing(ctx, "0xfollower")
	if err != nil {
		t.Fatalf("GetFollowing failed: %v", err)
	}

	if len(following) != 2 {
		t.Errorf("Expected following 2 agents, got %d", len(following))
	}
}

func TestMemoryStore_UnlikeComment(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.RegisterVerbalAgent(ctx, &VerbalAgent{Address: "0xauthor", Name: "A"})
	store.PostComment(ctx, &Comment{ID: "c1", AuthorAddr: "0xauthor", Content: "Test"})

	store.LikeComment(ctx, "c1", "0xliker")
	c, _ := store.GetComment(ctx, "c1")
	if c.Likes != 1 {
		t.Fatalf("Expected 1 like, got %d", c.Likes)
	}

	store.UnlikeComment(ctx, "c1", "0xliker")
	c, _ = store.GetComment(ctx, "c1")
	if c.Likes != 0 {
		t.Errorf("Expected 0 likes after unlike, got %d", c.Likes)
	}
}

func TestMemoryStore_ListComments_FilterByType(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.PostComment(ctx, &Comment{ID: "c1", AuthorAddr: "0xa", Type: TypeAnalysis, Content: "Analysis"})
	store.PostComment(ctx, &Comment{ID: "c2", AuthorAddr: "0xa", Type: TypeWarning, Content: "Warning"})
	store.PostComment(ctx, &Comment{ID: "c3", AuthorAddr: "0xa", Type: TypeAnalysis, Content: "More Analysis"})

	comments, _ := store.ListComments(ctx, ListOptions{Type: TypeAnalysis, Limit: 10})
	if len(comments) != 2 {
		t.Errorf("Expected 2 analysis comments, got %d", len(comments))
	}
}
