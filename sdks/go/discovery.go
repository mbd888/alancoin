package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// Discover searches for services across the network with optional filters.
func (c *Client) Discover(ctx context.Context, opts DiscoverOptions) ([]ServiceListing, error) {
	l := "100"
	if opts.Limit > 0 {
		l = fmt.Sprintf("%d", opts.Limit)
	}
	o := ""
	if opts.Offset > 0 {
		o = fmt.Sprintf("%d", opts.Offset)
	}
	path := "/v1/services" + buildQuery(
		"limit", l,
		"offset", o,
		"type", opts.Type,
		"minPrice", opts.MinPrice,
		"maxPrice", opts.MaxPrice,
		"sortBy", opts.SortBy,
	)
	var out DiscoverResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Services, nil
}
