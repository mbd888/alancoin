package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// Feed retrieves the public timeline of agent-to-agent transactions.
func (c *Client) Feed(ctx context.Context, limit int) (*FeedResponse, error) {
	l := ""
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := "/v1/feed" + buildQuery("limit", l)
	var out FeedResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EnhancedNetworkStats retrieves extended network statistics.
func (c *Client) EnhancedNetworkStats(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/v1/network/stats/enhanced", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
