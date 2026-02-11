package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// CurrencyBalance represents an agent's balance in a specific currency.
type CurrencyBalance struct {
	AgentAddr string    `json:"agentAddr"`
	Currency  string    `json:"currency"`
	Decimals  int       `json:"decimals"`
	Available string    `json:"available"`
	Pending   string    `json:"pending"`
	Escrowed  string    `json:"escrowed"`
	TotalIn   string    `json:"totalIn"`
	TotalOut  string    `json:"totalOut"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ExchangeRate represents a currency conversion rate.
type ExchangeRate struct {
	FromCurrency string    `json:"fromCurrency"`
	ToCurrency   string    `json:"toCurrency"`
	Rate         string    `json:"rate"`
	Source       string    `json:"source"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// CurrencyStore extends the core ledger with multi-currency operations.
type CurrencyStore interface {
	GetCurrencyBalance(ctx context.Context, agentAddr, currency string) (*CurrencyBalance, error)
	CreditCurrency(ctx context.Context, agentAddr, currency, amount, txHash, desc string) error
	DebitCurrency(ctx context.Context, agentAddr, currency, amount, ref, desc string) error
	ListCurrencyBalances(ctx context.Context, agentAddr string) ([]*CurrencyBalance, error)
}

// ExchangeRateProvider returns exchange rates.
type ExchangeRateProvider interface {
	GetRate(ctx context.Context, from, to string) (string, error)
}

// --- PostgresCurrencyStore ---

// PostgresCurrencyStore implements CurrencyStore with PostgreSQL.
type PostgresCurrencyStore struct {
	db *sql.DB
}

// NewPostgresCurrencyStore creates a PostgreSQL-backed currency store.
func NewPostgresCurrencyStore(db *sql.DB) *PostgresCurrencyStore {
	return &PostgresCurrencyStore{db: db}
}

func currencyDecimals(currency string) int {
	switch strings.ToUpper(currency) {
	case "ETH", "WBTC":
		return 18
	case "BTC":
		return 8
	default:
		return 6 // USDC, USDT, etc.
	}
}

func (s *PostgresCurrencyStore) GetCurrencyBalance(ctx context.Context, agentAddr, currency string) (*CurrencyBalance, error) {
	bal := &CurrencyBalance{AgentAddr: agentAddr, Currency: strings.ToUpper(currency)}

	err := s.db.QueryRowContext(ctx, `
		SELECT decimals, available, pending, escrowed, total_in, total_out, updated_at
		FROM agent_currency_balances
		WHERE agent_addr = $1 AND currency = $2
	`, agentAddr, strings.ToUpper(currency)).Scan(&bal.Decimals, &bal.Available, &bal.Pending, &bal.Escrowed, &bal.TotalIn, &bal.TotalOut, &bal.UpdatedAt)

	if err == sql.ErrNoRows {
		return &CurrencyBalance{
			AgentAddr: agentAddr,
			Currency:  strings.ToUpper(currency),
			Decimals:  currencyDecimals(currency),
			Available: "0",
			Pending:   "0",
			Escrowed:  "0",
			TotalIn:   "0",
			TotalOut:  "0",
			UpdatedAt: time.Now(),
		}, nil
	}
	return bal, err
}

func (s *PostgresCurrencyStore) CreditCurrency(ctx context.Context, agentAddr, currency, amount, txHash, desc string) error {
	cur := strings.ToUpper(currency)
	decimals := currencyDecimals(cur)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_currency_balances (agent_addr, currency, decimals, available, total_in, updated_at)
		VALUES ($1, $2, $3, $4::NUMERIC(30,18), $4::NUMERIC(30,18), NOW())
		ON CONFLICT (agent_addr, currency) DO UPDATE SET
			available  = agent_currency_balances.available + $4::NUMERIC(30,18),
			total_in   = agent_currency_balances.total_in + $4::NUMERIC(30,18),
			updated_at = NOW()
	`, agentAddr, cur, decimals, amount)
	return err
}

func (s *PostgresCurrencyStore) DebitCurrency(ctx context.Context, agentAddr, currency, amount, ref, desc string) error {
	cur := strings.ToUpper(currency)

	result, err := s.db.ExecContext(ctx, `
		UPDATE agent_currency_balances SET
			available  = available - $3::NUMERIC(30,18),
			total_out  = total_out + $3::NUMERIC(30,18),
			updated_at = NOW()
		WHERE agent_addr = $1 AND currency = $2 AND available >= $3::NUMERIC(30,18)
	`, agentAddr, cur, amount)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrInsufficientBalance
	}
	return nil
}

func (s *PostgresCurrencyStore) ListCurrencyBalances(ctx context.Context, agentAddr string) ([]*CurrencyBalance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_addr, currency, decimals, available, pending, escrowed, total_in, total_out, updated_at
		FROM agent_currency_balances
		WHERE agent_addr = $1
		ORDER BY currency
	`, agentAddr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var balances []*CurrencyBalance
	for rows.Next() {
		b := &CurrencyBalance{}
		if err := rows.Scan(&b.AgentAddr, &b.Currency, &b.Decimals, &b.Available, &b.Pending, &b.Escrowed, &b.TotalIn, &b.TotalOut, &b.UpdatedAt); err != nil {
			return nil, err
		}
		balances = append(balances, b)
	}
	return balances, rows.Err()
}

// --- MemoryCurrencyStore ---

// MemoryCurrencyStore implements CurrencyStore for demo/testing.
type MemoryCurrencyStore struct {
	balances map[string]*CurrencyBalance // key: "addr:currency"
	mu       sync.RWMutex
}

// NewMemoryCurrencyStore creates an in-memory currency store.
func NewMemoryCurrencyStore() *MemoryCurrencyStore {
	return &MemoryCurrencyStore{
		balances: make(map[string]*CurrencyBalance),
	}
}

func balKey(addr, currency string) string {
	return addr + ":" + strings.ToUpper(currency)
}

func (s *MemoryCurrencyStore) GetCurrencyBalance(_ context.Context, agentAddr, currency string) (*CurrencyBalance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := balKey(agentAddr, currency)
	if bal, ok := s.balances[key]; ok {
		cp := *bal
		return &cp, nil
	}
	return &CurrencyBalance{
		AgentAddr: agentAddr,
		Currency:  strings.ToUpper(currency),
		Decimals:  currencyDecimals(currency),
		Available: "0",
		Pending:   "0",
		Escrowed:  "0",
		TotalIn:   "0",
		TotalOut:  "0",
		UpdatedAt: time.Now(),
	}, nil
}

func (s *MemoryCurrencyStore) CreditCurrency(_ context.Context, agentAddr, currency, amount, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := strings.ToUpper(currency)
	key := balKey(agentAddr, cur)
	bal, ok := s.balances[key]
	if !ok {
		bal = &CurrencyBalance{
			AgentAddr: agentAddr,
			Currency:  cur,
			Decimals:  currencyDecimals(cur),
			Available: "0",
			Pending:   "0",
			Escrowed:  "0",
			TotalIn:   "0",
			TotalOut:  "0",
		}
		s.balances[key] = bal
	}

	avail := parseBigDec(bal.Available)
	totalIn := parseBigDec(bal.TotalIn)
	add := parseBigDec(amount)

	avail.Add(avail, add)
	totalIn.Add(totalIn, add)

	bal.Available = formatBigDec(avail)
	bal.TotalIn = formatBigDec(totalIn)
	bal.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryCurrencyStore) DebitCurrency(_ context.Context, agentAddr, currency, amount, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := balKey(agentAddr, strings.ToUpper(currency))
	bal, ok := s.balances[key]
	if !ok {
		return ErrInsufficientBalance
	}

	avail := parseBigDec(bal.Available)
	totalOut := parseBigDec(bal.TotalOut)
	sub := parseBigDec(amount)

	if avail.Cmp(sub) < 0 {
		return ErrInsufficientBalance
	}

	avail.Sub(avail, sub)
	totalOut.Add(totalOut, sub)

	bal.Available = formatBigDec(avail)
	bal.TotalOut = formatBigDec(totalOut)
	bal.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryCurrencyStore) ListCurrencyBalances(_ context.Context, agentAddr string) ([]*CurrencyBalance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*CurrencyBalance
	prefix := agentAddr + ":"
	for k, v := range s.balances {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			cp := *v
			result = append(result, &cp)
		}
	}
	return result, nil
}

// parseBigDec parses a decimal string to big.Int (using 18 decimal places for multi-currency).
func parseBigDec(s string) *big.Int {
	if s == "" || s == "0" {
		return big.NewInt(0)
	}
	parts := strings.Split(s, ".")
	whole := parts[0]
	frac := ""
	if len(parts) > 1 {
		frac = parts[1]
	}
	for len(frac) < 18 {
		frac += "0"
	}
	frac = frac[:18]
	result, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok {
		return big.NewInt(0)
	}
	return result
}

// formatBigDec formats a big.Int as a decimal string with 18 decimal places.
func formatBigDec(amount *big.Int) string {
	if amount == nil {
		return "0"
	}
	s := amount.String()
	if amount.Sign() < 0 {
		s = s[1:] // strip minus for formatting
	}
	for len(s) < 19 {
		s = "0" + s
	}
	decimal := len(s) - 18
	result := s[:decimal] + "." + s[decimal:]
	// Trim trailing zeros but keep at least one decimal
	result = strings.TrimRight(result, "0")
	result = strings.TrimRight(result, ".")
	if amount.Sign() < 0 {
		result = "-" + result
	}
	if result == "" {
		return "0"
	}
	return result
}

// StaticExchangeRateProvider returns a fixed rate (for demo/testing).
type StaticExchangeRateProvider struct {
	rates map[string]string // "ETH:USDC" -> "3500.00"
	mu    sync.RWMutex
}

// NewStaticExchangeRateProvider creates a static rate provider.
func NewStaticExchangeRateProvider() *StaticExchangeRateProvider {
	return &StaticExchangeRateProvider{
		rates: make(map[string]string),
	}
}

// SetRate sets a rate for a currency pair.
func (p *StaticExchangeRateProvider) SetRate(from, to, rate string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rates[strings.ToUpper(from)+":"+strings.ToUpper(to)] = rate
}

// GetRate returns the rate for a currency pair.
func (p *StaticExchangeRateProvider) GetRate(_ context.Context, from, to string) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key := strings.ToUpper(from) + ":" + strings.ToUpper(to)
	if rate, ok := p.rates[key]; ok {
		return rate, nil
	}
	return "", fmt.Errorf("no rate for %s→%s", from, to)
}
