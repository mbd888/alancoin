package pagination

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecode_RoundTrip(t *testing.T) {
	ts := time.Date(2026, 2, 15, 10, 30, 0, 0, time.UTC)
	id := "gw_abc123"

	encoded := Encode(ts, id)
	assert.NotEmpty(t, encoded)

	cursor, err := Decode(encoded)
	require.NoError(t, err)
	require.NotNil(t, cursor)
	assert.Equal(t, ts, cursor.CreatedAt)
	assert.Equal(t, id, cursor.ID)
}

func TestDecode_Empty(t *testing.T) {
	cursor, err := Decode("")
	assert.NoError(t, err)
	assert.Nil(t, cursor)
}

func TestDecode_Invalid(t *testing.T) {
	_, err := Decode("not-base64!!!")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cursor")
}

func TestDecode_MalformedPayload(t *testing.T) {
	// Valid base64 but no | separator
	_, err := Decode("bm9waXBl") // "nopipe"
	assert.Error(t, err)
}

func TestComputePage_NoMore(t *testing.T) {
	items := []string{"a", "b", "c"}
	result, cursor, hasMore := ComputePage(items, 5, func(s string) (time.Time, string) {
		return time.Now(), s
	})
	assert.Equal(t, 3, len(result))
	assert.Empty(t, cursor)
	assert.False(t, hasMore)
}

func TestComputePage_HasMore(t *testing.T) {
	items := []string{"a", "b", "c", "d"}
	result, cursor, hasMore := ComputePage(items, 3, func(s string) (time.Time, string) {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), s
	})
	assert.Equal(t, 3, len(result))
	assert.NotEmpty(t, cursor)
	assert.True(t, hasMore)

	// Verify cursor decodes to the last item
	c, err := Decode(cursor)
	require.NoError(t, err)
	assert.Equal(t, "c", c.ID)
}

func TestComputePage_ExactLimit(t *testing.T) {
	items := []string{"a", "b", "c"}
	result, cursor, hasMore := ComputePage(items, 3, func(s string) (time.Time, string) {
		return time.Now(), s
	})
	assert.Equal(t, 3, len(result))
	assert.Empty(t, cursor)
	assert.False(t, hasMore)
}
