package usdc

import (
	"math/big"
	"testing"
)

func TestParse_ValidAmounts(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int64
	}{
		{"one dollar", "1.00", 1_000_000},
		{"fifty cents", "0.50", 500_000},
		{"hundred", "100", 100_000_000},
		{"smallest unit", "0.000001", 1},
		{"whole and frac", "1.500000", 1_500_000},
		{"no frac", "1", 1_000_000},
		{"short frac", "1.5", 1_500_000},
		{"three decimals", "1.123", 1_123_000},
		{"six decimals", "1.123456", 1_123_456},
		{"large amount", "999999.999999", 999_999_999_999},
		{"leading zeros in whole", "007.50", 7_500_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.input)
			if !ok {
				t.Fatalf("Parse(%q) returned ok=false", tt.input)
			}
			if got.Int64() != tt.expected {
				t.Errorf("Parse(%q) = %d, want %d", tt.input, got.Int64(), tt.expected)
			}
		})
	}
}

func TestParse_ZeroVariants(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"zero", "0"},
		{"zero point zero", "0.0"},
		{"zero six decimals", "0.000000"},
		{"just dot zero", "0.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.input)
			if !ok {
				t.Fatalf("Parse(%q) returned ok=false", tt.input)
			}
			if got.Sign() != 0 {
				t.Errorf("Parse(%q) = %s, want 0", tt.input, got.String())
			}
		})
	}
}

func TestParse_EmptyString(t *testing.T) {
	got, ok := Parse("")
	if !ok {
		t.Fatal("Parse(\"\") returned ok=false")
	}
	if got.Sign() != 0 {
		t.Errorf("Parse(\"\") = %s, want 0", got.String())
	}
}

func TestParse_TruncationBeyondSixDecimals(t *testing.T) {
	// "1.1234567890" should truncate to "1.123456"
	got, ok := Parse("1.1234567890")
	if !ok {
		t.Fatal("Parse returned ok=false")
	}
	if got.Int64() != 1_123_456 {
		t.Errorf("Parse(\"1.1234567890\") = %d, want %d (truncated to 6 decimals)", got.Int64(), 1_123_456)
	}
}

func TestParse_NoWholePartWithDot(t *testing.T) {
	// ".50" should parse as 0.50
	got, ok := Parse(".50")
	if !ok {
		t.Fatal("Parse(\".50\") returned ok=false")
	}
	if got.Int64() != 500_000 {
		t.Errorf("Parse(\".50\") = %d, want 500000", got.Int64())
	}
}

func TestParse_InvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"negative", "-1.00"},
		{"negative zero", "-0"},
		{"alphabetic", "abc"},
		{"multiple dots", "1.2.3"},
		{"has letters", "12abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := Parse(tt.input)
			if ok {
				t.Errorf("Parse(%q) should return ok=false", tt.input)
			}
		})
	}
}

func TestParse_VeryLargeAmount(t *testing.T) {
	// Beyond int64 range — use big.Int comparison
	got, ok := Parse("99999999999999.999999")
	if !ok {
		t.Fatal("Parse returned ok=false for very large amount")
	}
	expected, _ := new(big.Int).SetString("99999999999999999999", 10)
	if got.Cmp(expected) != 0 {
		t.Errorf("Parse very large = %s, want %s", got.String(), expected.String())
	}
}

func TestFormat_Nil(t *testing.T) {
	got := Format(nil)
	if got != "0.000000" {
		t.Errorf("Format(nil) = %q, want \"0.000000\"", got)
	}
}

func TestFormat_Zero(t *testing.T) {
	got := Format(big.NewInt(0))
	if got != "0.000000" {
		t.Errorf("Format(0) = %q, want \"0.000000\"", got)
	}
}

func TestFormat_SmallValues(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected string
	}{
		{"one unit", 1, "0.000001"},
		{"ten units", 10, "0.000010"},
		{"hundred units", 100, "0.000100"},
		{"thousand units", 1000, "0.001000"},
		{"hundred thousand", 100_000, "0.100000"},
		{"one dollar", 1_000_000, "1.000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(big.NewInt(tt.input))
			if got != tt.expected {
				t.Errorf("Format(%d) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFormat_LargeValues(t *testing.T) {
	got := Format(big.NewInt(999_999_999_999))
	if got != "999999.999999" {
		t.Errorf("Format(999999999999) = %q, want \"999999.999999\"", got)
	}
}

func TestFormat_NegativeValues(t *testing.T) {
	got := Format(big.NewInt(-1_500_000))
	if got != "-1.500000" {
		t.Errorf("Format(-1500000) = %q, want \"-1.500000\"", got)
	}
}

func TestFormat_ExactlySixDecimals(t *testing.T) {
	// Every formatted output should have exactly 6 decimal places
	amounts := []int64{0, 1, 10, 100, 1000, 10000, 100000, 1000000, 123456789}
	for _, a := range amounts {
		got := Format(big.NewInt(a))
		dotIdx := -1
		for i, ch := range got {
			if ch == '.' {
				dotIdx = i
				break
			}
		}
		if dotIdx == -1 {
			t.Errorf("Format(%d) = %q has no decimal point", a, got)
			continue
		}
		fracLen := len(got) - dotIdx - 1
		if fracLen != 6 {
			t.Errorf("Format(%d) = %q has %d decimal places, want 6", a, got, fracLen)
		}
	}
}

func TestRoundTrip_Canonical(t *testing.T) {
	// Format(Parse(x)) == x for canonical forms (6 decimals)
	canonical := []string{
		"0.000000",
		"0.000001",
		"1.000000",
		"1.500000",
		"100.123456",
		"999999.999999",
	}

	for _, s := range canonical {
		t.Run(s, func(t *testing.T) {
			parsed, ok := Parse(s)
			if !ok {
				t.Fatalf("Parse(%q) returned ok=false", s)
			}
			got := Format(parsed)
			if got != s {
				t.Errorf("RoundTrip: Format(Parse(%q)) = %q", s, got)
			}
		})
	}
}

func TestRoundTrip_NonCanonical(t *testing.T) {
	// Non-canonical inputs get normalized
	tests := []struct {
		input    string
		expected string
	}{
		{"1", "1.000000"},
		{"1.5", "1.500000"},
		{"0.1", "0.100000"},
		{"007.50", "7.500000"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			parsed, ok := Parse(tt.input)
			if !ok {
				t.Fatalf("Parse(%q) returned ok=false", tt.input)
			}
			got := Format(parsed)
			if got != tt.expected {
				t.Errorf("Format(Parse(%q)) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDecimalsConstant(t *testing.T) {
	if Decimals != 6 {
		t.Errorf("Decimals = %d, want 6", Decimals)
	}
}
