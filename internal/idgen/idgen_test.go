package idgen

import (
	"strings"
	"testing"
)

func TestNew_Format(t *testing.T) {
	id := New()
	// Should be UUID-like: 8-4-4-4-12 hex chars = 36 chars total
	if len(id) != 36 {
		t.Errorf("New() len = %d, want 36", len(id))
	}
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("New() has %d parts, want 5", len(parts))
	}
	wantLens := []int{8, 4, 4, 4, 12}
	for i, part := range parts {
		if len(part) != wantLens[i] {
			t.Errorf("part[%d] len = %d, want %d", i, len(part), wantLens[i])
		}
	}
}

func TestNew_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		id := New()
		if seen[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
}

func TestWithPrefix(t *testing.T) {
	id := WithPrefix("sk_")
	if !strings.HasPrefix(id, "sk_") {
		t.Errorf("WithPrefix(\"sk_\") = %q, missing prefix", id)
	}
	// prefix (3) + 24 hex chars = 27
	if len(id) != 27 {
		t.Errorf("WithPrefix len = %d, want 27", len(id))
	}
}

func TestWithPrefix_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		id := WithPrefix("tx_")
		if seen[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
}

func TestHex(t *testing.T) {
	h := Hex(16)
	if len(h) != 32 {
		t.Errorf("Hex(16) len = %d, want 32", len(h))
	}

	h4 := Hex(4)
	if len(h4) != 8 {
		t.Errorf("Hex(4) len = %d, want 8", len(h4))
	}
}

func TestHex_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		h := Hex(16)
		if seen[h] {
			t.Fatalf("duplicate hex: %s", h)
		}
		seen[h] = true
	}
}

func TestHex_ValidHex(t *testing.T) {
	h := Hex(8)
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("invalid hex char %q in %q", string(c), h)
		}
	}
}
