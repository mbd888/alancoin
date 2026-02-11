package ledger

import (
	"context"
	"testing"
)

func TestMemoryCurrencyStore_CreditAndGet(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCurrencyStore()

	agent := "0xA"

	// Credit ETH
	err := store.CreditCurrency(ctx, agent, "ETH", "1.5", "0xtx1", "deposit")
	if err != nil {
		t.Fatalf("CreditCurrency failed: %v", err)
	}

	bal, err := store.GetCurrencyBalance(ctx, agent, "ETH")
	if err != nil {
		t.Fatalf("GetCurrencyBalance failed: %v", err)
	}

	if bal.Currency != "ETH" {
		t.Errorf("expected currency ETH, got %s", bal.Currency)
	}
	if bal.Decimals != 18 {
		t.Errorf("expected 18 decimals for ETH, got %d", bal.Decimals)
	}

	// Verify credit was applied
	expected := parseBigDec("1.5")
	actual := parseBigDec(bal.Available)
	if actual.Cmp(expected) != 0 {
		t.Errorf("expected available 1.5, got %s", bal.Available)
	}
}

func TestMemoryCurrencyStore_Debit(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCurrencyStore()

	agent := "0xA"

	_ = store.CreditCurrency(ctx, agent, "USDC", "100", "0xtx1", "deposit")

	err := store.DebitCurrency(ctx, agent, "USDC", "30", "ref_1", "spend")
	if err != nil {
		t.Fatalf("DebitCurrency failed: %v", err)
	}

	bal, _ := store.GetCurrencyBalance(ctx, agent, "USDC")

	expected := parseBigDec("70")
	actual := parseBigDec(bal.Available)
	if actual.Cmp(expected) != 0 {
		t.Errorf("expected available 70, got %s", bal.Available)
	}
}

func TestMemoryCurrencyStore_DebitInsufficientBalance(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCurrencyStore()

	agent := "0xA"

	_ = store.CreditCurrency(ctx, agent, "USDC", "10", "0xtx1", "deposit")

	err := store.DebitCurrency(ctx, agent, "USDC", "20", "ref_1", "spend")
	if err != ErrInsufficientBalance {
		t.Errorf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestMemoryCurrencyStore_DebitNoBalance(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCurrencyStore()

	err := store.DebitCurrency(ctx, "0xA", "ETH", "1", "ref_1", "spend")
	if err != ErrInsufficientBalance {
		t.Errorf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestMemoryCurrencyStore_ListBalances(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCurrencyStore()

	agent := "0xA"

	_ = store.CreditCurrency(ctx, agent, "ETH", "1.5", "0xtx1", "deposit")
	_ = store.CreditCurrency(ctx, agent, "USDC", "100", "0xtx2", "deposit")

	balances, err := store.ListCurrencyBalances(ctx, agent)
	if err != nil {
		t.Fatalf("ListCurrencyBalances failed: %v", err)
	}
	if len(balances) != 2 {
		t.Fatalf("expected 2 currency balances, got %d", len(balances))
	}
}

func TestMemoryCurrencyStore_GetEmptyBalance(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCurrencyStore()

	bal, err := store.GetCurrencyBalance(ctx, "0xA", "WBTC")
	if err != nil {
		t.Fatalf("GetCurrencyBalance failed: %v", err)
	}
	if bal.Currency != "WBTC" {
		t.Errorf("expected currency WBTC, got %s", bal.Currency)
	}
	if bal.Available != "0" {
		t.Errorf("expected available 0, got %s", bal.Available)
	}
	if bal.Decimals != 18 {
		t.Errorf("expected 18 decimals for WBTC, got %d", bal.Decimals)
	}
}

func TestCurrencyDecimals(t *testing.T) {
	tests := []struct {
		currency string
		expected int
	}{
		{"ETH", 18},
		{"WBTC", 18},
		{"BTC", 8},
		{"USDC", 6},
		{"USDT", 6},
		{"DAI", 6},
	}
	for _, tc := range tests {
		if got := currencyDecimals(tc.currency); got != tc.expected {
			t.Errorf("currencyDecimals(%s) = %d, want %d", tc.currency, got, tc.expected)
		}
	}
}

func TestParseBigDec_FormatBigDec_Roundtrip(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"0", "0"},
		{"1", "1"},
		{"1.5", "1.5"},
		{"100.123456789", "100.123456789"},
	}
	for _, tc := range tests {
		result := parseBigDec(tc.input)
		formatted := formatBigDec(result)
		if formatted != tc.expected {
			t.Errorf("parseBigDec(%q) -> formatBigDec = %q, want %q", tc.input, formatted, tc.expected)
		}
	}
}

func TestStaticExchangeRateProvider(t *testing.T) {
	ctx := context.Background()
	provider := NewStaticExchangeRateProvider()
	provider.SetRate("ETH", "USDC", "3500.00")

	rate, err := provider.GetRate(ctx, "ETH", "USDC")
	if err != nil {
		t.Fatalf("GetRate failed: %v", err)
	}
	if rate != "3500.00" {
		t.Errorf("expected rate 3500.00, got %s", rate)
	}

	// Missing pair
	_, err = provider.GetRate(ctx, "BTC", "USDC")
	if err == nil {
		t.Error("expected error for missing rate pair")
	}
}
