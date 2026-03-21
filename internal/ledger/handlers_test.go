package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------- helpers ----------

func setupHandler() (*gin.Engine, *Handler, *Ledger) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	r := gin.New()
	v1 := r.Group("/v1")
	h.RegisterRoutes(v1)

	return r, h, l
}

func setupHandlerWithEvents() (*gin.Engine, *Handler, *Ledger) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	h := NewHandler(l, slog.Default())

	r := gin.New()
	v1 := r.Group("/v1")
	h.RegisterRoutes(v1)

	return r, h, l
}

// ---------- mock types ----------

type mockWithdrawalExecutor struct {
	transferFn func(ctx context.Context, to common.Address, amount *big.Int) (string, error)
}

func (m *mockWithdrawalExecutor) Transfer(ctx context.Context, to common.Address, amount *big.Int) (string, error) {
	if m.transferFn != nil {
		return m.transferFn(ctx, to, amount)
	}
	return "0xtxhash123", nil
}

type mockReputationScorer struct {
	score float64
	tier  string
	err   error
}

func (m *mockReputationScorer) GetScore(_ context.Context, _ string) (float64, string, error) {
	return m.score, m.tier, m.err
}

// ---------- GetBalance ----------

func TestHandler_GetBalance_Success(t *testing.T) {
	router, _, l := setupHandler()
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	req := httptest.NewRequest("GET", "/v1/agents/"+agent+"/balance", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	bal := resp["balance"].(map[string]interface{})
	if bal["available"] != "10.000000" {
		t.Errorf("expected available 10.000000, got %v", bal["available"])
	}
}

func TestHandler_GetBalance_NewAgent(t *testing.T) {
	router, _, _ := setupHandler()

	req := httptest.NewRequest("GET", "/v1/agents/0xunknown/balance", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetBalance_PointInTime(t *testing.T) {
	router, _, l := setupHandlerWithEvents()
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	req := httptest.NewRequest("GET", "/v1/agents/"+agent+"/balance?at=2099-01-01T00:00:00Z", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_GetBalance_InvalidTimestamp(t *testing.T) {
	router, _, _ := setupHandler()
	agent := "0x1234567890123456789012345678901234567890"

	req := httptest.NewRequest("GET", "/v1/agents/"+agent+"/balance?at=notadate", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_GetBalance_PointInTime_NoEventStore(t *testing.T) {
	router, _, _ := setupHandler() // no event store
	agent := "0x1234567890123456789012345678901234567890"

	req := httptest.NewRequest("GET", "/v1/agents/"+agent+"/balance?at=2099-01-01T00:00:00Z", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when no event store, got %d", w.Code)
	}
}

// ---------- GetHistory ----------

func TestHandler_GetHistory_Success(t *testing.T) {
	router, _, l := setupHandler()
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.Spend(ctx, agent, "3.00", "sk_1")

	req := httptest.NewRequest("GET", "/v1/agents/"+agent+"/ledger?limit=10", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	entries := resp["entries"].([]interface{})
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestHandler_GetHistory_DefaultLimit(t *testing.T) {
	router, _, _ := setupHandler()

	req := httptest.NewRequest("GET", "/v1/agents/0xtest/ledger", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_GetHistory_InvalidCursor(t *testing.T) {
	router, _, _ := setupHandler()

	req := httptest.NewRequest("GET", "/v1/agents/0xtest/ledger?cursor=notvalid", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid cursor, got %d", w.Code)
	}
}

func TestHandler_GetHistory_WithPagination(t *testing.T) {
	router, _, l := setupHandler()
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	// Create enough entries to trigger pagination
	for i := 0; i < 5; i++ {
		l.Deposit(ctx, agent, "1.00", "0xtx"+string(rune('a'+i)))
	}

	// Request with limit=2 to trigger hasMore
	req := httptest.NewRequest("GET", "/v1/agents/"+agent+"/ledger?limit=2", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	entries := resp["entries"].([]interface{})
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
	if resp["hasMore"] != true {
		t.Error("expected hasMore=true")
	}
	cursor, ok := resp["nextCursor"].(string)
	if !ok || cursor == "" {
		t.Error("expected non-empty nextCursor")
	}

	// Follow cursor to get next page
	req = httptest.NewRequest("GET", "/v1/agents/"+agent+"/ledger?limit=2&cursor="+cursor, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cursor follow: expected 200, got %d", w.Code)
	}
}

func TestHandler_GetHistory_InvalidLimit(t *testing.T) {
	router, _, _ := setupHandler()

	// Limit out of range should fall back to default
	req := httptest.NewRequest("GET", "/v1/agents/0xtest/ledger?limit=999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ---------- RecordDeposit ----------

func TestHandler_RecordDeposit_Success(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	r := gin.New()
	r.POST("/admin/deposits", h.RecordDeposit)

	body, _ := json.Marshal(DepositRequest{
		AgentAddress: "0x1234567890123456789012345678901234567890",
		Amount:       "5.50",
		TxHash:       "0xdeposithash",
	})
	req := httptest.NewRequest("POST", "/admin/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_RecordDeposit_InvalidBody(t *testing.T) {
	h := NewHandler(New(NewMemoryStore()), slog.Default())
	r := gin.New()
	r.POST("/admin/deposits", h.RecordDeposit)

	req := httptest.NewRequest("POST", "/admin/deposits", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_RecordDeposit_InvalidAddress(t *testing.T) {
	h := NewHandler(New(NewMemoryStore()), slog.Default())
	r := gin.New()
	r.POST("/admin/deposits", h.RecordDeposit)

	body, _ := json.Marshal(DepositRequest{
		AgentAddress: "not-an-address",
		Amount:       "5.50",
		TxHash:       "0xhash",
	})
	req := httptest.NewRequest("POST", "/admin/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_RecordDeposit_InvalidAmount(t *testing.T) {
	h := NewHandler(New(NewMemoryStore()), slog.Default())
	r := gin.New()
	r.POST("/admin/deposits", h.RecordDeposit)

	body, _ := json.Marshal(DepositRequest{
		AgentAddress: "0x1234567890123456789012345678901234567890",
		Amount:       "not-a-number",
		TxHash:       "0xhash",
	})
	req := httptest.NewRequest("POST", "/admin/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_RecordDeposit_Duplicate(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	r := gin.New()
	r.POST("/admin/deposits", h.RecordDeposit)

	body, _ := json.Marshal(DepositRequest{
		AgentAddress: "0x1234567890123456789012345678901234567890",
		Amount:       "5.50",
		TxHash:       "0xdup",
	})

	// First deposit
	req := httptest.NewRequest("POST", "/admin/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first deposit: expected 201, got %d", w.Code)
	}

	// Duplicate
	req = httptest.NewRequest("POST", "/admin/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate deposit: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------- RequestWithdrawal ----------

func TestHandler_RequestWithdrawal_Pending(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default()) // no executor => pending mode

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	r := gin.New()
	r.POST("/agents/:address/withdraw", h.RequestWithdrawal)

	body, _ := json.Marshal(WithdrawRequest{Amount: "3.00"})
	req := httptest.NewRequest("POST", "/agents/"+agent+"/withdraw", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "pending" {
		t.Errorf("expected status pending, got %v", resp["status"])
	}
}

func TestHandler_RequestWithdrawal_InsufficientBalance(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "1.00", "0xtx1")

	r := gin.New()
	r.POST("/agents/:address/withdraw", h.RequestWithdrawal)

	body, _ := json.Marshal(WithdrawRequest{Amount: "100.00"})
	req := httptest.NewRequest("POST", "/agents/"+agent+"/withdraw", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_RequestWithdrawal_InvalidBody(t *testing.T) {
	h := NewHandler(New(NewMemoryStore()), slog.Default())
	r := gin.New()
	r.POST("/agents/:address/withdraw", h.RequestWithdrawal)

	req := httptest.NewRequest("POST", "/agents/0xtest/withdraw", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_RequestWithdrawal_InvalidAmount(t *testing.T) {
	h := NewHandler(New(NewMemoryStore()), slog.Default())
	r := gin.New()
	r.POST("/agents/:address/withdraw", h.RequestWithdrawal)

	body, _ := json.Marshal(WithdrawRequest{Amount: "abc"})
	req := httptest.NewRequest("POST", "/agents/0xtest/withdraw", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_RequestWithdrawal_WithExecutor_Success(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	executor := &mockWithdrawalExecutor{}
	h := NewHandlerWithWithdrawals(l, executor, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	r := gin.New()
	r.POST("/agents/:address/withdraw", h.RequestWithdrawal)

	body, _ := json.Marshal(WithdrawRequest{Amount: "3.00"})
	req := httptest.NewRequest("POST", "/agents/"+agent+"/withdraw", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "completed" {
		t.Errorf("expected status completed, got %v", resp["status"])
	}
}

func TestHandler_RequestWithdrawal_WithExecutor_TransferFails(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	executor := &mockWithdrawalExecutor{
		transferFn: func(_ context.Context, _ common.Address, _ *big.Int) (string, error) {
			return "", errors.New("chain error")
		},
	}
	h := NewHandlerWithWithdrawals(l, executor, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	r := gin.New()
	r.POST("/agents/:address/withdraw", h.RequestWithdrawal)

	body, _ := json.Marshal(WithdrawRequest{Amount: "3.00"})
	req := httptest.NewRequest("POST", "/agents/"+agent+"/withdraw", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}

	// Balance should be restored after hold release
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "10.000000" {
		t.Errorf("expected available restored to 10.000000, got %s", bal.Available)
	}
}

func TestHandler_RequestWithdrawal_WithExecutor_InsufficientBalance(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	executor := &mockWithdrawalExecutor{}
	h := NewHandlerWithWithdrawals(l, executor, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "1.00", "0xtx1")

	r := gin.New()
	r.POST("/agents/:address/withdraw", h.RequestWithdrawal)

	body, _ := json.Marshal(WithdrawRequest{Amount: "50.00"})
	req := httptest.NewRequest("POST", "/agents/"+agent+"/withdraw", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------- Reconcile ----------

func TestHandler_Reconcile_Success(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	r := gin.New()
	r.GET("/admin/reconcile", h.Reconcile)

	req := httptest.NewRequest("GET", "/admin/reconcile", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Reconcile_NoEventStore(t *testing.T) {
	l := New(NewMemoryStore())
	h := NewHandler(l, slog.Default())

	r := gin.New()
	r.GET("/admin/reconcile", h.Reconcile)

	req := httptest.NewRequest("GET", "/admin/reconcile", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHandler_Reconcile_DiscrepanciesFilter(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	r := gin.New()
	r.GET("/admin/reconcile", h.Reconcile)

	// With discrepancies=true, matching balances should be filtered out
	req := httptest.NewRequest("GET", "/admin/reconcile?discrepancies=true", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	count := resp["count"].(float64)
	if count != 0 {
		t.Errorf("expected 0 discrepancies, got %v", count)
	}
}

// ---------- QueryAudit ----------

func TestHandler_QueryAudit_NotConfigured(t *testing.T) {
	l := New(NewMemoryStore()) // no audit logger
	h := NewHandler(l, slog.Default())

	r := gin.New()
	r.GET("/admin/audit", h.QueryAudit)

	req := httptest.NewRequest("GET", "/admin/audit?agent=0xtest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", w.Code)
	}
}

func TestHandler_QueryAudit_MissingAgent(t *testing.T) {
	al := NewMemoryAuditLogger()
	l := New(NewMemoryStore()).WithAuditLogger(al)
	h := NewHandler(l, slog.Default())

	r := gin.New()
	r.GET("/admin/audit", h.QueryAudit)

	req := httptest.NewRequest("GET", "/admin/audit", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_QueryAudit_Success(t *testing.T) {
	al := NewMemoryAuditLogger()
	store := NewMemoryStore()
	l := New(store).WithAuditLogger(al)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	r := gin.New()
	r.GET("/admin/audit", h.QueryAudit)

	req := httptest.NewRequest("GET", "/admin/audit?agent="+agent, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	count := resp["count"].(float64)
	if count < 1 {
		t.Errorf("expected at least 1 audit entry, got %v", count)
	}
}

func TestHandler_QueryAudit_WithFilters(t *testing.T) {
	al := NewMemoryAuditLogger()
	store := NewMemoryStore()
	l := New(store).WithAuditLogger(al)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.Spend(ctx, agent, "3.00", "sk_1")

	r := gin.New()
	r.GET("/admin/audit", h.QueryAudit)

	// With from/to/operation/limit filters
	req := httptest.NewRequest("GET", "/admin/audit?agent="+agent+"&operation=deposit&limit=5&from=2000-01-01T00:00:00Z&to=2099-01-01T00:00:00Z", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	count := resp["count"].(float64)
	if count != 1 {
		t.Errorf("expected 1 deposit audit entry, got %v", count)
	}
}

// ---------- Reverse ----------

func TestHandler_Reverse_Success(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	entries, _ := l.GetHistory(ctx, agent, 10)
	entryID := entries[0].ID

	r := gin.New()
	r.POST("/admin/reversals", h.Reverse)

	body, _ := json.Marshal(ReverseRequest{EntryID: entryID, Reason: "test reversal"})
	req := httptest.NewRequest("POST", "/admin/reversals", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Reverse_InvalidBody(t *testing.T) {
	h := NewHandler(New(NewMemoryStore()), slog.Default())
	r := gin.New()
	r.POST("/admin/reversals", h.Reverse)

	req := httptest.NewRequest("POST", "/admin/reversals", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_Reverse_NotFound(t *testing.T) {
	h := NewHandler(New(NewMemoryStore()), slog.Default())
	r := gin.New()
	r.POST("/admin/reversals", h.Reverse)

	body, _ := json.Marshal(ReverseRequest{EntryID: "nonexistent", Reason: "test"})
	req := httptest.NewRequest("POST", "/admin/reversals", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_Reverse_AlreadyReversed(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	entries, _ := l.GetHistory(ctx, agent, 10)
	entryID := entries[0].ID

	// First reversal
	l.Reverse(ctx, entryID, "first", "admin")

	r := gin.New()
	r.POST("/admin/reversals", h.Reverse)

	body, _ := json.Marshal(ReverseRequest{EntryID: entryID, Reason: "second"})
	req := httptest.NewRequest("POST", "/admin/reversals", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestHandler_Reverse_InsufficientBalance(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.Spend(ctx, agent, "9.00", "sk_1")

	entries, _ := l.GetHistory(ctx, agent, 10)
	// Find the deposit entry
	var depositID string
	for _, e := range entries {
		if e.Type == "deposit" {
			depositID = e.ID
			break
		}
	}

	r := gin.New()
	r.POST("/admin/reversals", h.Reverse)

	body, _ := json.Marshal(ReverseRequest{EntryID: depositID, Reason: "test"})
	req := httptest.NewRequest("POST", "/admin/reversals", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for insufficient balance, got %d", w.Code)
	}
}

// ---------- GetCreditInfo ----------

func TestHandler_GetCreditInfo_Success(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")
	store.SetCreditLimit(ctx, agent, "50.00")

	r := gin.New()
	r.GET("/agents/:address/credit", h.GetCreditInfo)

	req := httptest.NewRequest("GET", "/agents/"+agent+"/credit", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["limit"] != "50.00" {
		t.Errorf("expected limit 50.00, got %v", resp["limit"])
	}
}

// ---------- ApplyForCredit ----------

func TestHandler_ApplyForCredit_NotConfigured(t *testing.T) {
	h := NewHandler(New(NewMemoryStore()), slog.Default()) // no reputation scorer

	r := gin.New()
	r.POST("/agents/:address/credit/apply", h.ApplyForCredit)

	req := httptest.NewRequest("POST", "/agents/0xtest/credit/apply", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandler_ApplyForCredit_Approved(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	scorer := &mockReputationScorer{score: 75, tier: "gold"}
	h := NewHandler(l, slog.Default()).WithReputation(scorer)

	agent := "0x1234567890123456789012345678901234567890"
	// Ensure the agent exists in the store for SetCreditLimit
	l.Deposit(context.Background(), agent, "1.00", "0xtx_setup")

	r := gin.New()
	r.POST("/agents/:address/credit/apply", h.ApplyForCredit)

	req := httptest.NewRequest("POST", "/agents/"+agent+"/credit/apply", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "approved" {
		t.Errorf("expected status approved, got %v", resp["status"])
	}
}

func TestHandler_ApplyForCredit_Denied(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	scorer := &mockReputationScorer{score: 30, tier: "bronze"}
	h := NewHandler(l, slog.Default()).WithReputation(scorer)

	r := gin.New()
	r.POST("/agents/:address/credit/apply", h.ApplyForCredit)

	req := httptest.NewRequest("POST", "/agents/0xtest/credit/apply", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "denied" {
		t.Errorf("expected status denied, got %v", resp["status"])
	}
}

func TestHandler_ApplyForCredit_ReputationError(t *testing.T) {
	scorer := &mockReputationScorer{err: errors.New("service down")}
	h := NewHandler(New(NewMemoryStore()), slog.Default()).WithReputation(scorer)

	r := gin.New()
	r.POST("/agents/:address/credit/apply", h.ApplyForCredit)

	req := httptest.NewRequest("POST", "/agents/0xtest/credit/apply", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHandler_ApplyForCredit_ScoreClamping(t *testing.T) {
	// Test score boundary clamping (negative and > 100)
	tests := []struct {
		name       string
		score      float64
		wantStatus string
	}{
		{"negative_score_denied", -10, "denied"},
		{"over_100_approved", 150, "approved"},
		{"exactly_50_approved", 50, "approved"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			l := New(store)
			scorer := &mockReputationScorer{score: tt.score, tier: "test"}
			h := NewHandler(l, slog.Default()).WithReputation(scorer)

			agent := "0x1234567890123456789012345678901234567890"
			l.Deposit(context.Background(), agent, "1.00", "0xtx_"+tt.name)

			r := gin.New()
			r.POST("/agents/:address/credit/apply", h.ApplyForCredit)

			req := httptest.NewRequest("POST", "/agents/"+agent+"/credit/apply", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}

			var resp map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["status"] != tt.wantStatus {
				t.Errorf("expected status %s, got %v", tt.wantStatus, resp["status"])
			}
		})
	}
}

// ---------- ListActiveCredit ----------

func TestHandler_ListActiveCredit_WithMemoryStore(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")
	store.SetCreditLimit(ctx, agent, "50.00")

	r := gin.New()
	r.GET("/credit/active", h.ListActiveCredit)

	req := httptest.NewRequest("GET", "/credit/active", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	count := resp["count"].(float64)
	if count < 1 {
		t.Errorf("expected at least 1 active credit, got %v", count)
	}
}

// ---------- isValidAmount ----------

func TestIsValidAmount(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"10.00", true},
		{"0.50", true},
		{"100", true},
		{"0.000001", true},
		{"-1.00", false},
		{"abc", false},
		{"", false},
		{" 10.00 ", true},
		{"10.00.00", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isValidAmount(tt.input); got != tt.valid {
				t.Errorf("isValidAmount(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		})
	}
}

// ---------- RegisterRoutes ----------

func TestHandler_RegisterRoutes(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	h := NewHandler(l, slog.Default())

	r := gin.New()
	v1 := r.Group("/v1")
	h.RegisterRoutes(v1)
	h.RegisterCreditRoutes(v1)
	h.RegisterAdminRoutes(v1)

	// Verify routes exist by hitting them
	routes := r.Routes()
	if len(routes) == 0 {
		t.Fatal("no routes registered")
	}

	expected := map[string]bool{
		"GET /v1/agents/:address/balance":       false,
		"GET /v1/agents/:address/ledger":        false,
		"GET /v1/agents/:address/credit":        false,
		"POST /v1/agents/:address/credit/apply": false,
		"GET /v1/credit/active":                 false,
		"GET /v1/admin/reconcile":               false,
		"GET /v1/admin/audit":                   false,
		"POST /v1/admin/reversals":              false,
	}

	for _, route := range routes {
		key := route.Method + " " + route.Path
		if _, ok := expected[key]; ok {
			expected[key] = true
		}
	}

	for key, found := range expected {
		if !found {
			t.Errorf("expected route %s not registered", key)
		}
	}
}
