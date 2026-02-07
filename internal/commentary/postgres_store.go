package commentary

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
)

// PostgresStore implements Store with PostgreSQL
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed commentary store
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the commentary tables
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		-- Verbal agents (agents registered to post commentary)
		CREATE TABLE IF NOT EXISTS verbal_agents (
			address         VARCHAR(42) PRIMARY KEY,
			name            VARCHAR(255) NOT NULL,
			bio             TEXT,
			specialty       VARCHAR(50),
			followers       INTEGER DEFAULT 0,
			comment_count   INTEGER DEFAULT 0,
			reputation      DECIMAL(5,2) DEFAULT 50.0,
			verified        BOOLEAN DEFAULT FALSE,
			registered_at   TIMESTAMPTZ DEFAULT NOW()
		);

		-- Commentary posts
		CREATE TABLE IF NOT EXISTS comments (
			id              VARCHAR(36) PRIMARY KEY,
			author_addr     VARCHAR(42) NOT NULL REFERENCES verbal_agents(address),
			author_name     VARCHAR(255),
			type            VARCHAR(20) NOT NULL,
			content         TEXT NOT NULL,
			refs            JSONB DEFAULT '[]',
			likes           INTEGER DEFAULT 0,
			created_at      TIMESTAMPTZ DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_comments_author ON comments(author_addr);
		CREATE INDEX IF NOT EXISTS idx_comments_type ON comments(type);
		CREATE INDEX IF NOT EXISTS idx_comments_created ON comments(created_at DESC);

		-- Likes
		CREATE TABLE IF NOT EXISTS comment_likes (
			comment_id      VARCHAR(36) REFERENCES comments(id) ON DELETE CASCADE,
			agent_addr      VARCHAR(42) NOT NULL,
			created_at      TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (comment_id, agent_addr)
		);

		-- Following relationships
		CREATE TABLE IF NOT EXISTS verbal_follows (
			follower_addr   VARCHAR(42) NOT NULL,
			verbal_addr     VARCHAR(42) NOT NULL REFERENCES verbal_agents(address),
			created_at      TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (follower_addr, verbal_addr)
		);

		CREATE INDEX IF NOT EXISTS idx_follows_verbal ON verbal_follows(verbal_addr);
	`)
	return err
}

// RegisterVerbalAgent registers an agent for commentary
func (p *PostgresStore) RegisterVerbalAgent(ctx context.Context, agent *VerbalAgent) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO verbal_agents (address, name, bio, specialty, followers, comment_count, reputation, verified, registered_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (address) DO UPDATE SET
			name = EXCLUDED.name,
			bio = EXCLUDED.bio,
			specialty = EXCLUDED.specialty
	`, agent.Address, agent.Name, agent.Bio, agent.Specialty, agent.Followers, agent.CommentCount, agent.Reputation, agent.Verified, agent.RegisteredAt)
	return err
}

// GetVerbalAgent retrieves a verbal agent
func (p *PostgresStore) GetVerbalAgent(ctx context.Context, address string) (*VerbalAgent, error) {
	agent := &VerbalAgent{}
	err := p.db.QueryRowContext(ctx, `
		SELECT address, name, bio, specialty, followers, comment_count, reputation, verified, registered_at
		FROM verbal_agents WHERE address = $1
	`, address).Scan(
		&agent.Address, &agent.Name, &agent.Bio, &agent.Specialty,
		&agent.Followers, &agent.CommentCount, &agent.Reputation, &agent.Verified, &agent.RegisteredAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotVerbalAgent
	}
	return agent, err
}

// ListVerbalAgents returns verbal agents ordered by followers
func (p *PostgresStore) ListVerbalAgents(ctx context.Context, limit, offset int) ([]*VerbalAgent, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT address, name, bio, specialty, followers, comment_count, reputation, verified, registered_at
		FROM verbal_agents
		ORDER BY followers DESC, reputation DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []*VerbalAgent
	for rows.Next() {
		agent := &VerbalAgent{}
		if err := rows.Scan(
			&agent.Address, &agent.Name, &agent.Bio, &agent.Specialty,
			&agent.Followers, &agent.CommentCount, &agent.Reputation, &agent.Verified, &agent.RegisteredAt,
		); err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

// UpdateVerbalAgent updates a verbal agent
func (p *PostgresStore) UpdateVerbalAgent(ctx context.Context, agent *VerbalAgent) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE verbal_agents SET
			name = $2, bio = $3, specialty = $4, followers = $5,
			comment_count = $6, reputation = $7, verified = $8
		WHERE address = $1
	`, agent.Address, agent.Name, agent.Bio, agent.Specialty,
		agent.Followers, agent.CommentCount, agent.Reputation, agent.Verified)
	return err
}

// PostComment creates a new comment
func (p *PostgresStore) PostComment(ctx context.Context, comment *Comment) error {
	// Generate ID if not set
	if comment.ID == "" {
		comment.ID = generateID("cmt_")
	}

	refsJSON, err := json.Marshal(comment.References)
	if err != nil {
		return err
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO comments (id, author_addr, author_name, type, content, refs, likes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, comment.ID, comment.AuthorAddr, comment.AuthorName, comment.Type,
		comment.Content, refsJSON, comment.Likes, comment.CreatedAt)
	return err
}

// GetComment retrieves a comment by ID
func (p *PostgresStore) GetComment(ctx context.Context, id string) (*Comment, error) {
	comment := &Comment{}
	var refsJSON []byte

	err := p.db.QueryRowContext(ctx, `
		SELECT id, author_addr, author_name, type, content, refs, likes, created_at
		FROM comments WHERE id = $1
	`, id).Scan(
		&comment.ID, &comment.AuthorAddr, &comment.AuthorName, &comment.Type,
		&comment.Content, &refsJSON, &comment.Likes, &comment.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal(refsJSON, &comment.References)
	return comment, nil
}

// ListComments returns comments with optional filters
func (p *PostgresStore) ListComments(ctx context.Context, opts ListOptions) ([]*Comment, error) {
	query := `
		SELECT id, author_addr, author_name, type, content, refs, likes, created_at
		FROM comments
		WHERE 1=1
	`
	args := []interface{}{}
	argN := 1

	if opts.Type != "" {
		query += ` AND type = $` + string(rune('0'+argN))
		args = append(args, opts.Type)
		argN++
	}
	if opts.AuthorAddr != "" {
		query += ` AND author_addr = $` + string(rune('0'+argN))
		args = append(args, opts.AuthorAddr)
		argN++
	}
	if opts.Since != nil {
		query += ` AND created_at > $` + string(rune('0'+argN))
		args = append(args, opts.Since)
		argN++
	}

	query += ` ORDER BY created_at DESC LIMIT $` + string(rune('0'+argN))
	args = append(args, opts.Limit)

	if opts.Offset > 0 {
		argN++
		query += ` OFFSET $` + string(rune('0'+argN))
		args = append(args, opts.Offset)
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return p.scanComments(rows)
}

// ListByAuthor returns comments by a specific author
func (p *PostgresStore) ListByAuthor(ctx context.Context, authorAddr string, limit int) ([]*Comment, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, author_addr, author_name, type, content, refs, likes, created_at
		FROM comments
		WHERE author_addr = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, authorAddr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return p.scanComments(rows)
}

// ListByReference returns comments referencing a specific entity
func (p *PostgresStore) ListByReference(ctx context.Context, refType, refID string, limit int) ([]*Comment, error) {
	// Use json.Marshal to safely encode the reference for JSONB query
	ref := []map[string]string{{"type": refType, "id": refID}}
	refJSON, _ := json.Marshal(ref)

	// Query using JSONB contains
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, author_addr, author_name, type, content, refs, likes, created_at
		FROM comments
		WHERE refs @> $1::jsonb
		ORDER BY created_at DESC
		LIMIT $2
	`, string(refJSON), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return p.scanComments(rows)
}

func (p *PostgresStore) scanComments(rows *sql.Rows) ([]*Comment, error) {
	var comments []*Comment
	for rows.Next() {
		comment := &Comment{}
		var refsJSON []byte

		if err := rows.Scan(
			&comment.ID, &comment.AuthorAddr, &comment.AuthorName, &comment.Type,
			&comment.Content, &refsJSON, &comment.Likes, &comment.CreatedAt,
		); err != nil {
			return nil, err
		}

		_ = json.Unmarshal(refsJSON, &comment.References)
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

// LikeComment adds a like
func (p *PostgresStore) LikeComment(ctx context.Context, commentID, agentAddr string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert like
	_, err = tx.ExecContext(ctx, `
		INSERT INTO comment_likes (comment_id, agent_addr) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, commentID, agentAddr)
	if err != nil {
		return err
	}

	// Increment count
	_, err = tx.ExecContext(ctx, `UPDATE comments SET likes = likes + 1 WHERE id = $1`, commentID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// UnlikeComment removes a like
func (p *PostgresStore) UnlikeComment(ctx context.Context, commentID, agentAddr string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		DELETE FROM comment_likes WHERE comment_id = $1 AND agent_addr = $2
	`, commentID, agentAddr)
	if err != nil {
		return err
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		_, _ = tx.ExecContext(ctx, `UPDATE comments SET likes = likes - 1 WHERE id = $1`, commentID)
	}

	return tx.Commit()
}

// Follow creates a follow relationship
func (p *PostgresStore) Follow(ctx context.Context, followerAddr, verbalAgentAddr string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO verbal_follows (follower_addr, verbal_addr) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, followerAddr, verbalAgentAddr)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE verbal_agents SET followers = followers + 1 WHERE address = $1
	`, verbalAgentAddr)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// Unfollow removes a follow relationship
func (p *PostgresStore) Unfollow(ctx context.Context, followerAddr, verbalAgentAddr string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		DELETE FROM verbal_follows WHERE follower_addr = $1 AND verbal_addr = $2
	`, followerAddr, verbalAgentAddr)
	if err != nil {
		return err
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		_, _ = tx.ExecContext(ctx, `
			UPDATE verbal_agents SET followers = followers - 1 WHERE address = $1
		`, verbalAgentAddr)
	}

	return tx.Commit()
}

// GetFollowers returns addresses following a verbal agent
func (p *PostgresStore) GetFollowers(ctx context.Context, verbalAgentAddr string) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT follower_addr FROM verbal_follows WHERE verbal_addr = $1
	`, verbalAgentAddr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var followers []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, err
		}
		followers = append(followers, addr)
	}
	return followers, rows.Err()
}

// GetFollowing returns verbal agents an address follows
func (p *PostgresStore) GetFollowing(ctx context.Context, agentAddr string) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT verbal_addr FROM verbal_follows WHERE follower_addr = $1
	`, agentAddr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var following []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, err
		}
		following = append(following, addr)
	}
	return following, rows.Err()
}

func generateID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
