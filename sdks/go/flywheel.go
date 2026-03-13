package alancoin

import (
	"context"
	"net/http"
)

// FlywheelHealth retrieves the current network health score and sub-scores.
func (c *Client) FlywheelHealth(ctx context.Context) (*FlywheelHealth, error) {
	var out FlywheelHealth
	if err := c.doJSON(ctx, http.MethodGet, "/v1/flywheel/health", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FlywheelState retrieves the full flywheel state snapshot.
func (c *Client) FlywheelState(ctx context.Context) (*FlywheelState, error) {
	var out FlywheelState
	if err := c.doJSON(ctx, http.MethodGet, "/v1/flywheel/state", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FlywheelHistory retrieves historical health scores over time.
func (c *Client) FlywheelHistory(ctx context.Context) ([]FlywheelHistoryEntry, error) {
	var out flywheelHistoryResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/flywheel/history", nil, &out); err != nil {
		return nil, err
	}
	return out.History, nil
}

// FlywheelIncentives retrieves the current incentive schedule (fee discounts + discovery boosts).
func (c *Client) FlywheelIncentives(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/v1/flywheel/incentives", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
