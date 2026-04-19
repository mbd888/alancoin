package usdc

import (
	"context"
	"database/sql"
	"math/big"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresPayoutStore_PutUpsertsByClientRef(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()
	store := NewPostgresPayoutStore(db)

	p := &Payout{
		ClientRef:   "ref-abc",
		ChainID:     testChainID,
		From:        "0x1111111111111111111111111111111111111111",
		To:          "0x2222222222222222222222222222222222222222",
		Amount:      big.NewInt(1_500_000),
		Nonce:       7,
		TxHash:      "0xdeadbeef",
		Status:      TxStatusPending,
		SubmittedAt: time.Now().UTC(),
	}

	mock.ExpectExec("INSERT INTO payouts").
		WithArgs(
			p.ClientRef, p.ChainID, p.From, p.To, p.Amount.String(),
			int64(p.Nonce), p.TxHash, string(p.Status), p.SubmittedAt,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Put(context.Background(), p); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresPayoutStore_GetReturnsNilOnMissingRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresPayoutStore(db)

	mock.ExpectQuery("SELECT client_ref").
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	got, err := store.GetByClientRef(context.Background(), "missing")
	if err != nil {
		t.Fatalf("GetByClientRef: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing ref, got %+v", got)
	}
	_ = mock.ExpectationsWereMet()
}

func TestPostgresPayoutStore_GetReturnsRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresPayoutStore(db)

	submittedAt := time.Now().UTC().Truncate(time.Second)
	rows := sqlmock.NewRows([]string{
		"client_ref", "chain_id", "from_addr", "to_addr", "amount", "nonce",
		"tx_hash", "status", "submitted_at", "finalized_at",
		"receipt_block", "receipt_status", "receipt_confs", "last_error",
	}).AddRow(
		"ref-abc", int64(testChainID), "0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222", "1500000", int64(7),
		"0xdeadbeef", string(TxStatusSuccess), submittedAt, submittedAt,
		int64(42), string(TxStatusSuccess), int64(12), nil,
	)
	mock.ExpectQuery("SELECT client_ref").
		WithArgs("ref-abc").
		WillReturnRows(rows)

	got, err := store.GetByClientRef(context.Background(), "ref-abc")
	if err != nil {
		t.Fatalf("GetByClientRef: %v", err)
	}
	if got == nil {
		t.Fatal("expected payout, got nil")
	}
	if got.ClientRef != "ref-abc" || got.Nonce != 7 || got.Amount.String() != "1500000" {
		t.Errorf("unexpected payout: %+v", got)
	}
	if got.Receipt == nil || got.Receipt.Confirmations != 12 {
		t.Errorf("receipt not decoded: %+v", got.Receipt)
	}
	_ = mock.ExpectationsWereMet()
}
