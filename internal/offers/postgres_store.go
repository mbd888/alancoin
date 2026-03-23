package offers

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// PostgresStore persists offer and claim data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed offer store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

const offerColumns = `id, seller_addr, service_type, description, price,
	capacity, remaining_cap, conditions, status, total_claims,
	total_revenue, endpoint, expires_at, created_at, updated_at`

const claimColumns = `id, offer_id, buyer_addr, seller_addr, amount,
	status, escrow_ref, created_at, resolved_at`

func (p *PostgresStore) CreateOffer(ctx context.Context, o *Offer) error {
	condJSON, _ := json.Marshal(o.Conditions)
	if o.Conditions == nil {
		condJSON = []byte("[]")
	}

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO offers (
			id, seller_addr, service_type, description, price,
			capacity, remaining_cap, conditions, status, total_claims,
			total_revenue, endpoint, expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5::NUMERIC(20,6),
			$6, $7, $8, $9, $10,
			$11::NUMERIC(20,6), $12, $13, $14, $15
		)`,
		o.ID, o.SellerAddr, o.ServiceType, o.Description, o.Price,
		o.Capacity, o.RemainingCap, condJSON, string(o.Status), o.TotalClaims,
		o.TotalRevenue, nullString(o.Endpoint), o.ExpiresAt, o.CreatedAt, o.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) GetOffer(ctx context.Context, id string) (*Offer, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+offerColumns+` FROM offers WHERE id = $1`, id)
	o, err := scanOffer(row)
	if err == sql.ErrNoRows {
		return nil, ErrOfferNotFound
	}
	return o, err
}

func (p *PostgresStore) UpdateOffer(ctx context.Context, o *Offer) error {
	condJSON, _ := json.Marshal(o.Conditions)
	if o.Conditions == nil {
		condJSON = []byte("[]")
	}

	result, err := p.db.ExecContext(ctx, `
		UPDATE offers SET
			status = $1, remaining_cap = $2, total_claims = $3,
			total_revenue = $4::NUMERIC(20,6), conditions = $5, endpoint = $6,
			updated_at = $7
		WHERE id = $8`,
		string(o.Status), o.RemainingCap, o.TotalClaims,
		o.TotalRevenue, condJSON, nullString(o.Endpoint),
		o.UpdatedAt, o.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrOfferNotFound
	}
	return nil
}

func (p *PostgresStore) ListOffers(ctx context.Context, serviceType string, limit int) ([]*Offer, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+offerColumns+`
		FROM offers
		WHERE service_type = $1 AND status = 'active'
		ORDER BY price ASC, created_at DESC
		LIMIT $2`, serviceType, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanOffers(rows)
}

func (p *PostgresStore) ListOffersBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Offer, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+offerColumns+`
		FROM offers
		WHERE seller_addr = $1
		ORDER BY created_at DESC
		LIMIT $2`, sellerAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanOffers(rows)
}

func (p *PostgresStore) ListExpiredOffers(ctx context.Context, before time.Time, limit int) ([]*Offer, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+offerColumns+`
		FROM offers
		WHERE status = 'active' AND expires_at < $1
		ORDER BY expires_at ASC
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanOffers(rows)
}

func (p *PostgresStore) CreateClaim(ctx context.Context, c *Claim) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO offer_claims (
			id, offer_id, buyer_addr, seller_addr, amount,
			status, escrow_ref, created_at, resolved_at
		) VALUES (
			$1, $2, $3, $4, $5::NUMERIC(20,6),
			$6, $7, $8, $9
		)`,
		c.ID, c.OfferID, c.BuyerAddr, c.SellerAddr, c.Amount,
		string(c.Status), c.EscrowRef, c.CreatedAt, nullTime(c.ResolvedAt),
	)
	return err
}

func (p *PostgresStore) GetClaim(ctx context.Context, id string) (*Claim, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+claimColumns+` FROM offer_claims WHERE id = $1`, id)
	c, err := scanClaim(row)
	if err == sql.ErrNoRows {
		return nil, ErrClaimNotFound
	}
	return c, err
}

func (p *PostgresStore) UpdateClaim(ctx context.Context, c *Claim) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE offer_claims SET
			status = $1, resolved_at = $2
		WHERE id = $3`,
		string(c.Status), nullTime(c.ResolvedAt), c.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrClaimNotFound
	}
	return nil
}

func (p *PostgresStore) ListClaimsByOffer(ctx context.Context, offerID string, limit int) ([]*Claim, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+claimColumns+`
		FROM offer_claims
		WHERE offer_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, offerID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanClaims(rows)
}

func (p *PostgresStore) ListClaimsByBuyer(ctx context.Context, buyerAddr string, limit int) ([]*Claim, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+claimColumns+`
		FROM offer_claims
		WHERE buyer_addr = $1
		ORDER BY created_at DESC
		LIMIT $2`, buyerAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanClaims(rows)
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanOffer(s scanner) (*Offer, error) {
	o := &Offer{}
	var (
		status   string
		condJSON []byte
		endpoint sql.NullString
	)

	err := s.Scan(
		&o.ID, &o.SellerAddr, &o.ServiceType, &o.Description, &o.Price,
		&o.Capacity, &o.RemainingCap, &condJSON, &status, &o.TotalClaims,
		&o.TotalRevenue, &endpoint, &o.ExpiresAt, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	o.Status = OfferStatus(status)
	o.Endpoint = endpoint.String
	if len(condJSON) > 0 {
		_ = json.Unmarshal(condJSON, &o.Conditions)
	}

	return o, nil
}

func scanOffers(rows *sql.Rows) ([]*Offer, error) {
	var result []*Offer
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

func scanClaim(s scanner) (*Claim, error) {
	c := &Claim{}
	var (
		status     string
		resolvedAt sql.NullTime
	)

	err := s.Scan(
		&c.ID, &c.OfferID, &c.BuyerAddr, &c.SellerAddr, &c.Amount,
		&status, &c.EscrowRef, &c.CreatedAt, &resolvedAt,
	)
	if err != nil {
		return nil, err
	}

	c.Status = ClaimStatus(status)
	if resolvedAt.Valid {
		c.ResolvedAt = &resolvedAt.Time
	}

	return c, nil
}

func scanClaims(rows *sql.Rows) ([]*Claim, error) {
	var result []*Claim
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// Compile-time assertion that PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)
