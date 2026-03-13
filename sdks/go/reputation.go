package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// GetReputation retrieves the reputation for a single agent.
func (c *Client) GetReputation(ctx context.Context, address string) (*Reputation, error) {
	var out ReputationResponse
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/reputation/%s", address), nil, &out); err != nil {
		return nil, err
	}
	return &out.Reputation, nil
}

// BatchReputation retrieves reputation scores for up to 100 addresses at once.
func (c *Client) BatchReputation(ctx context.Context, addresses []string) ([]BatchReputationEntry, error) {
	req := struct {
		Addresses []string `json:"addresses"`
	}{Addresses: addresses}
	var out BatchReputationResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/reputation/batch", &req, &out); err != nil {
		return nil, err
	}
	return out.Scores, nil
}

// CompareAgents compares 2-10 agents' reputation scores.
func (c *Client) CompareAgents(ctx context.Context, addresses []string) (*CompareAgentsResponse, error) {
	req := struct {
		Addresses []string `json:"addresses"`
	}{Addresses: addresses}
	var out CompareAgentsResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/reputation/compare", &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Leaderboard retrieves the reputation leaderboard.
func (c *Client) Leaderboard(ctx context.Context, limit int, minScore float64, tier string) (*LeaderboardResponse, error) {
	l := "20"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	ms := ""
	if minScore > 0 {
		ms = fmt.Sprintf("%.2f", minScore)
	}
	path := "/v1/reputation" + buildQuery("limit", l, "minScore", ms, "tier", tier)
	var out LeaderboardResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReputationHistory retrieves historical reputation snapshots for an agent.
func (c *Client) ReputationHistory(ctx context.Context, address string, limit int, from, to string) ([]ReputationSnapshot, error) {
	l := "100"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/reputation/%s/history", address) + buildQuery("limit", l, "from", from, "to", to)
	var out HistoryResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Snapshots, nil
}
