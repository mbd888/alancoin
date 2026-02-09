package gas

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// PriceOracle provides ETH/USD price with caching
type PriceOracle struct {
	mu         sync.RWMutex
	price      float64
	lastUpdate time.Time
	ttl        time.Duration
	fallback   float64
	client     *http.Client
}

// NewPriceOracle creates a price oracle with a fallback price and cache TTL
func NewPriceOracle(fallbackPrice float64, cacheTTL time.Duration) *PriceOracle {
	return &PriceOracle{
		price:    fallbackPrice,
		fallback: fallbackPrice,
		ttl:      cacheTTL,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// GetETHPrice returns the current ETH/USD price.
// Fetches from CoinGecko API if cache is stale, falls back to last known price.
func (o *PriceOracle) GetETHPrice(ctx context.Context) float64 {
	o.mu.RLock()
	if time.Since(o.lastUpdate) < o.ttl && o.price > 0 {
		price := o.price
		o.mu.RUnlock()
		return price
	}
	o.mu.RUnlock()

	// Cache is stale, fetch new price
	newPrice, err := o.fetchPrice(ctx)
	if err != nil {
		// Mark cache as stale so next call retries immediately
		// instead of serving the stale price until original TTL expires
		o.mu.Lock()
		o.lastUpdate = time.Time{} // Force refresh on next call
		price := o.price
		o.mu.Unlock()
		if price > 0 {
			return price
		}
		return o.fallback
	}

	o.mu.Lock()
	o.price = newPrice
	o.lastUpdate = time.Now()
	o.mu.Unlock()

	return newPrice
}

// fetchPrice queries the CoinGecko simple price API (free, no key required)
func (o *PriceOracle) fetchPrice(ctx context.Context) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.coingecko.com/api/v3/simple/price?ids=ethereum&vs_currencies=usd",
		nil,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch price: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("price API returned status %d", resp.StatusCode)
	}

	var result struct {
		Ethereum struct {
			USD float64 `json:"usd"`
		} `json:"ethereum"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode price response: %w", err)
	}

	if result.Ethereum.USD <= 0 {
		return 0, fmt.Errorf("invalid price returned: %f", result.Ethereum.USD)
	}

	return result.Ethereum.USD, nil
}
