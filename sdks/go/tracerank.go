package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// TraceRankScore retrieves the graph-based TraceRank score for a single agent.
func (c *Client) TraceRankScore(ctx context.Context, address string) (*TraceRankScore, error) {
	var out TraceRankScore
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/tracerank/%s", address), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TraceRankLeaderboard retrieves the top agents ranked by TraceRank score.
func (c *Client) TraceRankLeaderboard(ctx context.Context, limit int) ([]TraceRankScore, error) {
	l := "50"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := "/v1/tracerank/leaderboard" + buildQuery("limit", l)
	var out traceRankLeaderboardResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Agents, nil
}

// TraceRankRuns retrieves recent TraceRank computation run metadata.
func (c *Client) TraceRankRuns(ctx context.Context, limit int) ([]TraceRankRun, error) {
	l := "10"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := "/v1/tracerank/runs" + buildQuery("limit", l)
	var out traceRankRunsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Runs, nil
}
