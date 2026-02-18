// Package pagination provides cursor-based pagination utilities.
package pagination

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cursor represents a position in a paginated result set.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Encode returns an opaque cursor string from a timestamp and ID.
func Encode(createdAt time.Time, id string) string {
	raw := fmt.Sprintf("%d|%s", createdAt.UnixNano(), id)
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// Decode parses an opaque cursor string. Returns nil for empty input.
func Decode(s string) (*Cursor, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid cursor")
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	return &Cursor{
		CreatedAt: time.Unix(0, nanos).UTC(),
		ID:        parts[1],
	}, nil
}

// ComputePage takes a slice of items (fetched with limit+1), the requested limit,
// and a function to extract (createdAt, id) from the last item.
// Returns the trimmed items, next cursor, and has_more flag.
func ComputePage[T any](items []T, limit int, extractKey func(T) (time.Time, string)) ([]T, string, bool) {
	if len(items) <= limit {
		return items, "", false
	}
	items = items[:limit]
	last := items[len(items)-1]
	createdAt, id := extractKey(last)
	return items, Encode(createdAt, id), true
}
