package validation

import (
	"testing"
)

func TestIsValidEthAddress(t *testing.T) {
	tests := []struct {
		addr  string
		valid bool
	}{
		{"0x1234567890123456789012345678901234567890", true},
		{"0xabcdefABCDEF1234567890123456789012345678", true},
		{"0x0000000000000000000000000000000000000000", true},

		// Invalid cases
		{"1234567890123456789012345678901234567890", false},     // No 0x
		{"0x12345678901234567890123456789012345678", false},     // Too short
		{"0x123456789012345678901234567890123456789012", false}, // Too long
		{"0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG", false},   // Invalid chars
		{"", false},
		{"0x", false},
	}

	for _, tc := range tests {
		result := IsValidEthAddress(tc.addr)
		if result != tc.valid {
			t.Errorf("IsValidEthAddress(%q) = %v, want %v", tc.addr, result, tc.valid)
		}
	}
}

func TestSanitizeAddress(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"0x1234567890123456789012345678901234567890", "0x1234567890123456789012345678901234567890"},
		{"0xABCDEF1234567890123456789012345678901234", "0xabcdef1234567890123456789012345678901234"},
		{"  0x1234567890123456789012345678901234567890  ", "0x1234567890123456789012345678901234567890"},
		{"1234567890123456789012345678901234567890", "0x1234567890123456789012345678901234567890"},
	}

	for _, tc := range tests {
		result := SanitizeAddress(tc.input)
		if result != tc.expected {
			t.Errorf("SanitizeAddress(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestSanitizeString(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"  hello  ", 10, "hello"},
		{"hello world", 5, "hello"},
		{"hello\x00world", 20, "helloworld"},
	}

	for _, tc := range tests {
		result := SanitizeString(tc.input, tc.maxLen)
		if result != tc.expected {
			t.Errorf("SanitizeString(%q, %d) = %q, want %q", tc.input, tc.maxLen, result, tc.expected)
		}
	}
}

func TestValidate(t *testing.T) {
	// Test valid input
	errors := Validate(
		Required("name", "John"),
		ValidAddress("address", "0x1234567890123456789012345678901234567890"),
	)
	if len(errors) != 0 {
		t.Errorf("Expected no errors, got %v", errors)
	}

	// Test invalid input
	errors = Validate(
		Required("name", ""),
		ValidAddress("address", "invalid"),
	)
	if len(errors) != 2 {
		t.Errorf("Expected 2 errors, got %d", len(errors))
	}
}

func TestValidAmount(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"1.00", true},
		{"0.50", true},
		{"100", true},
		{"0.000001", true},

		// Invalid
		{".50", false},
		{"1.", false},
		{"abc", false},
		{"-1.00", false},
		{"1.2.3", false},
	}

	for _, tc := range tests {
		err := ValidAmount("amount", tc.value)()
		valid := err == nil
		if valid != tc.valid {
			t.Errorf("ValidAmount(%q) valid=%v, want %v", tc.value, valid, tc.valid)
		}
	}
}

func TestMaxLength(t *testing.T) {
	// Under limit
	err := MaxLength("field", "hello", 10)()
	if err != nil {
		t.Error("Expected no error for string under limit")
	}

	// At limit
	err = MaxLength("field", "hello", 5)()
	if err != nil {
		t.Error("Expected no error for string at limit")
	}

	// Over limit
	err = MaxLength("field", "hello world", 5)()
	if err == nil {
		t.Error("Expected error for string over limit")
	}
}
