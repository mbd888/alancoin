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

// DiscoverByType is a convenience method that discovers services of a specific type,
// sorted by the given strategy (e.g., "cheapest", "reputation", "best_value").
func (c *Client) DiscoverByType(ctx context.Context, serviceType, sortBy string) ([]ServiceListing, error) {
	return c.Discover(ctx, DiscoverOptions{
		Type:   serviceType,
		SortBy: sortBy,
	})
}

// DiscoverCheapest finds the cheapest service of a given type.
// Returns nil if no services are found.
func (c *Client) DiscoverCheapest(ctx context.Context, serviceType string) (*ServiceListing, error) {
	services, err := c.Discover(ctx, DiscoverOptions{
		Type:   serviceType,
		SortBy: "cheapest",
		Limit:  1,
	})
	if err != nil {
		return nil, err
	}
	if len(services) == 0 {
		return nil, nil
	}
	return &services[0], nil
}

// DiscoverBestValue finds the best-value service of a given type, balancing
// price and reputation. Returns nil if no services are found.
func (c *Client) DiscoverBestValue(ctx context.Context, serviceType string) (*ServiceListing, error) {
	services, err := c.Discover(ctx, DiscoverOptions{
		Type:   serviceType,
		SortBy: "best_value",
		Limit:  1,
	})
	if err != nil {
		return nil, err
	}
	if len(services) == 0 {
		return nil, nil
	}
	return &services[0], nil
}

// DiscoverInPriceRange finds services within a given price range.
func (c *Client) DiscoverInPriceRange(ctx context.Context, serviceType, minPrice, maxPrice string) ([]ServiceListing, error) {
	return c.Discover(ctx, DiscoverOptions{
		Type:     serviceType,
		MinPrice: minPrice,
		MaxPrice: maxPrice,
		SortBy:   "cheapest",
	})
}
