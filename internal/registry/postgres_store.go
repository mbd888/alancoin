package registry

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lib/pq"
)

// PostgresStore implements Store using PostgreSQL
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed store
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// -----------------------------------------------------------------------------
// Agent Operations
// -----------------------------------------------------------------------------

func (p *PostgresStore) CreateAgent(ctx context.Context, agent *Agent) error {
	metadata, err := json.Marshal(agent.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal agent metadata: %w", err)
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO agents (address, name, description, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
	`, strings.ToLower(agent.Address), agent.Name, agent.Description, metadata, time.Now())

	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return ErrAgentExists
		}
		return fmt.Errorf("failed to create agent: %w", err)
	}

	// Initialize stats
	if _, err := p.db.ExecContext(ctx, `
		INSERT INTO agent_stats (agent_address) VALUES ($1) ON CONFLICT DO NOTHING
	`, strings.ToLower(agent.Address)); err != nil {
		slog.Warn("failed to initialize agent stats", "address", agent.Address, "error", err)
	}

	return nil
}

func (p *PostgresStore) GetAgent(ctx context.Context, address string) (*Agent, error) {
	address = strings.ToLower(address)

	var agent Agent
	var metadata []byte
	var createdAt, updatedAt time.Time

	err := p.db.QueryRowContext(ctx, `
		SELECT address, name, description, metadata, created_at, updated_at
		FROM agents WHERE address = $1
	`, address).Scan(&agent.Address, &agent.Name, &agent.Description, &metadata, &createdAt, &updatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrAgentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get agent: %w", err)
	}

	agent.CreatedAt = createdAt
	agent.UpdatedAt = updatedAt
	if err := json.Unmarshal(metadata, &agent.Metadata); err != nil {
		slog.Warn("failed to unmarshal agent metadata", "address", agent.Address, "error", err)
	}

	// Load services
	services, svcErr := p.getAgentServices(ctx, address)
	if svcErr != nil {
		slog.Warn("failed to load agent services", "address", address, "error", svcErr)
	}
	agent.Services = services

	// Load stats
	stats, statsErr := p.getAgentStats(ctx, address)
	if statsErr != nil {
		slog.Warn("failed to load agent stats", "address", address, "error", statsErr)
	}
	agent.Stats = stats

	return &agent, nil
}

func (p *PostgresStore) UpdateAgent(ctx context.Context, agent *Agent) error {
	metadata, err := json.Marshal(agent.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal agent metadata: %w", err)
	}

	result, err := p.db.ExecContext(ctx, `
		UPDATE agents SET name = $1, description = $2, metadata = $3, updated_at = NOW()
		WHERE address = $4
	`, agent.Name, agent.Description, metadata, strings.ToLower(agent.Address))

	if err != nil {
		return fmt.Errorf("failed to update agent: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return ErrAgentNotFound
	}

	return nil
}

func (p *PostgresStore) DeleteAgent(ctx context.Context, address string) error {
	result, err := p.db.ExecContext(ctx, `
		DELETE FROM agents WHERE address = $1
	`, strings.ToLower(address))

	if err != nil {
		return fmt.Errorf("failed to delete agent: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return ErrAgentNotFound
	}

	return nil
}

func (p *PostgresStore) ListAgents(ctx context.Context, filter AgentFilter) ([]*Agent, error) {
	query := `SELECT address FROM agents`
	var args []interface{}
	var conditions []string

	if filter.ServiceType != "" {
		conditions = append(conditions, `address IN (SELECT agent_address FROM services WHERE type = $1 AND active = true)`)
		args = append(args, filter.ServiceType)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args)) //nolint:gosec // placeholder index, not user input
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args)) //nolint:gosec // placeholder index, not user input
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var agents []*Agent
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return nil, fmt.Errorf("scan agent address: %w", err)
		}
		agent, err := p.GetAgent(ctx, address)
		if err == nil {
			agents = append(agents, agent)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents: %w", err)
	}

	return agents, nil
}

// -----------------------------------------------------------------------------
// Service Operations
// -----------------------------------------------------------------------------

func (p *PostgresStore) AddService(ctx context.Context, agentAddress string, service *Service) error {
	agentAddress = strings.ToLower(agentAddress)
	metadata, _ := json.Marshal(service.Metadata)

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO services (id, agent_address, type, name, description, price, endpoint, active, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
	`, service.ID, agentAddress, service.Type, service.Name, service.Description,
		service.Price, service.Endpoint, service.Active, metadata)

	if err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return ErrAgentNotFound
		}
		return fmt.Errorf("failed to add service: %w", err)
	}

	return nil
}

func (p *PostgresStore) UpdateService(ctx context.Context, agentAddress string, service *Service) error {
	metadata, _ := json.Marshal(service.Metadata)

	result, err := p.db.ExecContext(ctx, `
		UPDATE services SET type = $1, name = $2, description = $3, price = $4, 
		       endpoint = $5, active = $6, metadata = $7, updated_at = NOW()
		WHERE id = $8 AND agent_address = $9
	`, service.Type, service.Name, service.Description, service.Price,
		service.Endpoint, service.Active, metadata, service.ID, strings.ToLower(agentAddress))

	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrServiceNotFound
	}

	return nil
}

func (p *PostgresStore) RemoveService(ctx context.Context, agentAddress, serviceID string) error {
	result, err := p.db.ExecContext(ctx, `
		DELETE FROM services WHERE id = $1 AND agent_address = $2
	`, serviceID, strings.ToLower(agentAddress))

	if err != nil {
		return fmt.Errorf("failed to remove service: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrServiceNotFound
	}

	return nil
}

func (p *PostgresStore) GetService(ctx context.Context, agentAddress, serviceID string) (*Service, error) {
	var svc Service
	var metadata []byte

	err := p.db.QueryRowContext(ctx, `
		SELECT id, agent_address, type, name, description, price, endpoint, active, metadata
		FROM services WHERE id = $1 AND agent_address = $2
	`, serviceID, strings.ToLower(agentAddress)).Scan(
		&svc.ID, &svc.AgentAddress, &svc.Type, &svc.Name, &svc.Description,
		&svc.Price, &svc.Endpoint, &svc.Active, &metadata)

	if err == sql.ErrNoRows {
		return nil, ErrServiceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %w", err)
	}

	if err := json.Unmarshal(metadata, &svc.Metadata); err != nil {
		slog.Warn("failed to unmarshal service metadata", "service", svc.ID, "error", err)
	}
	return &svc, nil
}

// ListServices implements the Store interface for listing services
func (p *PostgresStore) ListServices(ctx context.Context, query AgentQuery) ([]ServiceListing, error) {
	filter := ServiceFilter{
		Type:     query.ServiceType,
		MinPrice: query.MinPrice,
		MaxPrice: query.MaxPrice,
		Limit:    query.Limit,
		Offset:   query.Offset,
	}
	results, err := p.DiscoverServices(ctx, filter)
	if err != nil {
		return nil, err
	}
	// Convert []*ServiceListing to []ServiceListing
	listings := make([]ServiceListing, len(results))
	for i, r := range results {
		listings[i] = *r
	}
	return listings, nil
}

func (p *PostgresStore) DiscoverServices(ctx context.Context, filter ServiceFilter) ([]*ServiceListing, error) {
	// Try materialized view first (pre-joined with stats + reputation)
	results, err := p.discoverFromMatview(ctx, filter)
	if err != nil {
		// Fall back to base tables if matview doesn't exist yet
		return p.discoverFromTables(ctx, filter)
	}
	return results, nil
}

func (p *PostgresStore) discoverFromMatview(ctx context.Context, filter ServiceFilter) ([]*ServiceListing, error) {
	query := `
		SELECT id, type, service_name, description, price, endpoint,
		       agent_address, agent_name, tx_count, success_rate,
		       reputation_score, reputation_tier
		FROM service_listings_mv
		WHERE true
	`
	var args []interface{}

	if filter.Type != "" {
		query += fmt.Sprintf(" AND type = $%d", len(args)+1)
		args = append(args, filter.Type)
	}
	if filter.MinPrice != "" {
		query += fmt.Sprintf(" AND CAST(price AS DECIMAL) >= $%d", len(args)+1)
		args = append(args, filter.MinPrice)
	}
	if filter.MaxPrice != "" {
		query += fmt.Sprintf(" AND CAST(price AS DECIMAL) <= $%d", len(args)+1)
		args = append(args, filter.MaxPrice)
	}

	query += " ORDER BY CAST(price AS DECIMAL) ASC"

	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args)) //nolint:gosec // placeholder index, not user input
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args)) //nolint:gosec // placeholder index, not user input
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*ServiceListing
	for rows.Next() {
		var listing ServiceListing
		if err := rows.Scan(&listing.ID, &listing.Type, &listing.Name, &listing.Description,
			&listing.Price, &listing.Endpoint, &listing.AgentAddress, &listing.AgentName,
			&listing.TxCount, &listing.SuccessRate,
			&listing.ReputationScore, &listing.ReputationTier); err != nil {
			continue
		}
		results = append(results, &listing)
	}
	return results, nil
}

func (p *PostgresStore) discoverFromTables(ctx context.Context, filter ServiceFilter) ([]*ServiceListing, error) {
	query := `
		SELECT s.id, s.type, s.name, s.description, s.price, s.endpoint,
		       a.address, a.name as agent_name
		FROM services s
		JOIN agents a ON s.agent_address = a.address
		WHERE s.active = true
	`
	var args []interface{}

	if filter.Type != "" {
		query += fmt.Sprintf(" AND s.type = $%d", len(args)+1)
		args = append(args, filter.Type)
	}
	if filter.MinPrice != "" {
		query += fmt.Sprintf(" AND CAST(s.price AS DECIMAL) >= $%d", len(args)+1)
		args = append(args, filter.MinPrice)
	}
	if filter.MaxPrice != "" {
		query += fmt.Sprintf(" AND CAST(s.price AS DECIMAL) <= $%d", len(args)+1)
		args = append(args, filter.MaxPrice)
	}

	query += " ORDER BY CAST(s.price AS DECIMAL) ASC"

	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args)) //nolint:gosec // placeholder index, not user input
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args)) //nolint:gosec // placeholder index, not user input
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to discover services: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []*ServiceListing
	for rows.Next() {
		var listing ServiceListing
		if err := rows.Scan(&listing.ID, &listing.Type, &listing.Name, &listing.Description,
			&listing.Price, &listing.Endpoint, &listing.AgentAddress, &listing.AgentName); err != nil {
			continue
		}
		results = append(results, &listing)
	}
	return results, nil
}

// -----------------------------------------------------------------------------
// Transaction Operations
// -----------------------------------------------------------------------------

func (p *PostgresStore) RecordTransaction(ctx context.Context, tx *Transaction) error {
	if tx.ID == "" {
		tx.ID = generateTxID()
	}
	if tx.CreatedAt.IsZero() {
		tx.CreatedAt = time.Now()
	}

	metadata, _ := json.Marshal(tx.Metadata)

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO transactions (id, tx_hash, from_address, to_address, amount, service_id, status, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, tx.ID, tx.TxHash, strings.ToLower(tx.From), strings.ToLower(tx.To),
		tx.Amount, nullString(tx.ServiceID), tx.Status, metadata, tx.CreatedAt)

	if err != nil {
		// Check for unique constraint violation on tx_hash
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return fmt.Errorf("duplicate transaction hash: %s", tx.TxHash)
		}
		return fmt.Errorf("failed to record transaction: %w", err)
	}

	return nil
}

func (p *PostgresStore) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	var tx Transaction
	var serviceID sql.NullString
	var metadata []byte

	err := p.db.QueryRowContext(ctx, `
		SELECT id, tx_hash, from_address, to_address, amount, service_id, status, metadata, created_at
		FROM transactions WHERE id = $1
	`, id).Scan(&tx.ID, &tx.TxHash, &tx.From, &tx.To, &tx.Amount, &serviceID, &tx.Status, &metadata, &tx.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, errors.New("transaction not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	tx.ServiceID = serviceID.String
	if err := json.Unmarshal(metadata, &tx.Metadata); err != nil {
		slog.Warn("failed to unmarshal transaction metadata", "tx", tx.ID, "error", err)
	}
	return &tx, nil
}

func (p *PostgresStore) ListTransactions(ctx context.Context, agentAddress string, limit int) ([]*Transaction, error) {
	if limit == 0 {
		limit = 100
	}
	agentAddress = strings.ToLower(agentAddress)

	rows, err := p.db.QueryContext(ctx, `
		SELECT id, tx_hash, from_address, to_address, amount, service_id, status, created_at
		FROM transactions
		WHERE from_address = $1 OR to_address = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, agentAddress, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to list transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var transactions []*Transaction
	for rows.Next() {
		var tx Transaction
		var serviceID sql.NullString
		if err := rows.Scan(&tx.ID, &tx.TxHash, &tx.From, &tx.To, &tx.Amount, &serviceID, &tx.Status, &tx.CreatedAt); err != nil {
			continue
		}
		tx.ServiceID = serviceID.String
		transactions = append(transactions, &tx)
	}

	return transactions, nil
}

func (p *PostgresStore) GetRecentTransactions(ctx context.Context, limit int) ([]*Transaction, error) {
	if limit == 0 {
		limit = 50
	}

	rows, err := p.db.QueryContext(ctx, `
		SELECT id, tx_hash, from_address, to_address, amount, service_id, status, created_at
		FROM transactions
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to get recent transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var transactions []*Transaction
	for rows.Next() {
		var tx Transaction
		var serviceID sql.NullString
		if err := rows.Scan(&tx.ID, &tx.TxHash, &tx.From, &tx.To, &tx.Amount, &serviceID, &tx.Status, &tx.CreatedAt); err != nil {
			continue
		}
		tx.ServiceID = serviceID.String
		transactions = append(transactions, &tx)
	}

	return transactions, nil
}

func (p *PostgresStore) UpdateAgentStats(ctx context.Context, address string, fn func(*AgentStats)) error {
	// Stats are updated automatically via trigger, but we can manually update if needed
	stats, err := p.getAgentStats(ctx, address)
	if err != nil {
		stats = AgentStats{}
	}

	fn(&stats)

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO agent_stats (agent_address, transaction_count, total_received, total_spent, success_rate, last_active, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (agent_address) DO UPDATE SET
			transaction_count = $2, total_received = $3, total_spent = $4, 
			success_rate = $5, last_active = $6, updated_at = NOW()
	`, strings.ToLower(address), stats.TransactionCount, stats.TotalReceived, stats.TotalSpent, stats.SuccessRate, stats.LastActive)

	return err
}

// -----------------------------------------------------------------------------
// Network Stats
// -----------------------------------------------------------------------------

func (p *PostgresStore) GetNetworkStats(ctx context.Context) (*NetworkStats, error) {
	var stats NetworkStats

	err := p.db.QueryRowContext(ctx, `
		SELECT total_agents, total_services, total_transactions, total_volume
		FROM network_stats
	`).Scan(&stats.TotalAgents, &stats.TotalServices, &stats.TotalTransactions, &stats.TotalVolume)

	if err != nil {
		// Return zeros if view fails
		return &NetworkStats{}, nil
	}

	return &stats, nil
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (p *PostgresStore) getAgentServices(ctx context.Context, address string) ([]Service, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, type, name, description, price, endpoint, active, metadata
		FROM services WHERE agent_address = $1
	`, address)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var services []Service
	for rows.Next() {
		var svc Service
		var metadata []byte
		if err := rows.Scan(&svc.ID, &svc.Type, &svc.Name, &svc.Description,
			&svc.Price, &svc.Endpoint, &svc.Active, &metadata); err != nil {
			continue
		}
		svc.AgentAddress = address
		if err := json.Unmarshal(metadata, &svc.Metadata); err != nil {
			slog.Warn("failed to unmarshal service metadata", "service", svc.ID, "error", err)
		}
		services = append(services, svc)
	}
	return services, nil
}

func (p *PostgresStore) getAgentStats(ctx context.Context, address string) (AgentStats, error) {
	var stats AgentStats
	var lastActive sql.NullTime

	err := p.db.QueryRowContext(ctx, `
		SELECT transaction_count, total_received, total_spent, success_rate, last_active
		FROM agent_stats WHERE agent_address = $1
	`, strings.ToLower(address)).Scan(&stats.TransactionCount, &stats.TotalReceived,
		&stats.TotalSpent, &stats.SuccessRate, &lastActive)

	if err != nil {
		return AgentStats{SuccessRate: 1.0}, nil
	}

	if lastActive.Valid {
		stats.LastActive = lastActive.Time
	}

	return stats, nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func generateTxID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: should never happen with crypto/rand
		return fmt.Sprintf("tx_%d", time.Now().UnixNano())
	}
	return "tx_" + hex.EncodeToString(b)
}
