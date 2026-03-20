package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// ForensicsAlert represents a spend anomaly alert.
type ForensicsAlert struct {
	ID           string  `json:"id"`
	AgentAddr    string  `json:"agentAddr"`
	Type         string  `json:"type"`
	Severity     string  `json:"severity"`
	Message      string  `json:"message"`
	Score        float64 `json:"score"`
	Baseline     float64 `json:"baseline"`
	Actual       float64 `json:"actual"`
	Sigma        float64 `json:"sigma"`
	DetectedAt   string  `json:"detectedAt"`
	Acknowledged bool    `json:"acknowledged"`
}

// ForensicsBaseline represents an agent's behavioral spending baseline.
type ForensicsBaseline struct {
	AgentAddr           string         `json:"agentAddr"`
	TxCount             int            `json:"txCount"`
	MeanAmount          float64        `json:"meanAmount"`
	StdDevAmount        float64        `json:"stdDevAmount"`
	MeanVelocity        float64        `json:"meanVelocity"`
	StdDevVelocity      float64        `json:"stdDevVelocity"`
	KnownCounterparties map[string]int `json:"knownCounterparties"`
	KnownServices       map[string]int `json:"knownServices"`
	LastUpdated         string         `json:"lastUpdated"`
}

// ForensicsGetBaseline retrieves the behavioral baseline for an agent.
func (c *Client) ForensicsGetBaseline(ctx context.Context, agentAddr string) (*ForensicsBaseline, error) {
	var out struct {
		Baseline ForensicsBaseline `json:"baseline"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/forensics/agents/"+agentAddr+"/baseline", nil, &out); err != nil {
		return nil, err
	}
	return &out.Baseline, nil
}

// ForensicsListAlerts retrieves spend anomaly alerts for an agent.
func (c *Client) ForensicsListAlerts(ctx context.Context, agentAddr string, limit int) ([]ForensicsAlert, error) {
	path := fmt.Sprintf("/v1/forensics/agents/%s/alerts", agentAddr) + buildQuery("limit", fmt.Sprintf("%d", limit))
	var out struct {
		Alerts []ForensicsAlert `json:"alerts"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Alerts, nil
}

// ForensicsAcknowledgeAlert marks an alert as reviewed.
func (c *Client) ForensicsAcknowledgeAlert(ctx context.Context, alertID string) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/forensics/alerts/"+alertID+"/acknowledge", nil, nil)
}
