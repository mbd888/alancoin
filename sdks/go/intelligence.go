package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// IntelligenceProfile retrieves the full intelligence profile for an agent.
func (c *Client) IntelligenceProfile(ctx context.Context, address string) (*IntelProfile, error) {
	var out IntelProfile
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/intelligence/%s", address), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IntelligenceCreditScore retrieves the credit score and factors for an agent.
func (c *Client) IntelligenceCreditScore(ctx context.Context, address string) (*IntelCreditResponse, error) {
	var out IntelCreditResponse
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/intelligence/%s/credit", address), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IntelligenceRiskScore retrieves the risk score and factors for an agent.
func (c *Client) IntelligenceRiskScore(ctx context.Context, address string) (*IntelRiskResponse, error) {
	var out IntelRiskResponse
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/intelligence/%s/risk", address), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IntelligenceTrends retrieves historical score trends for an agent.
func (c *Client) IntelligenceTrends(ctx context.Context, address string, limit int) (*IntelTrendsResponse, error) {
	l := "100"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/intelligence/%s/trends", address) + buildQuery("limit", l)
	var out IntelTrendsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IntelligenceLeaderboard retrieves the top agents by composite intelligence score.
func (c *Client) IntelligenceLeaderboard(ctx context.Context, limit int) ([]IntelProfile, error) {
	l := "50"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := "/v1/intelligence/network/leaderboard" + buildQuery("limit", l)
	var out intelLeaderboardResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Agents, nil
}

// IntelligenceBenchmarks retrieves network-wide intelligence benchmarks.
func (c *Client) IntelligenceBenchmarks(ctx context.Context) (*IntelBenchmarks, error) {
	var out IntelBenchmarks
	if err := c.doJSON(ctx, http.MethodGet, "/v1/intelligence/network/benchmarks", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IntelligenceBatchLookup retrieves intelligence profiles for multiple agents.
func (c *Client) IntelligenceBatchLookup(ctx context.Context, addresses []string) (map[string]*IntelProfile, error) {
	body := map[string]interface{}{"addresses": addresses}
	var out intelBatchResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/intelligence/batch", body, &out); err != nil {
		return nil, err
	}
	return out.Profiles, nil
}
