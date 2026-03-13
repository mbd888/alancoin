package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// Webhook event constants.
const (
	EventPaymentReceived  = "payment.received"
	EventPaymentSent      = "payment.sent"
	EventSessionKeyUsed   = "session_key.used"
	EventSessionKeyCreate = "session_key.created"
	EventSessionKeyRevoke = "session_key.revoked"
	EventBalanceDeposit   = "balance.deposit"
	EventBalanceWithdraw  = "balance.withdraw"
)

// CreateWebhook registers a webhook for an agent.
func (c *Client) CreateWebhook(ctx context.Context, agentAddr string, req CreateWebhookRequest) (*Webhook, error) {
	var out createWebhookResponse
	path := fmt.Sprintf("/v1/agents/%s/webhooks", agentAddr)
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &Webhook{
		ID:     out.ID,
		URL:    out.URL,
		Events: out.Events,
		Secret: out.Secret,
	}, nil
}

// ListWebhooks retrieves all webhooks for an agent.
func (c *Client) ListWebhooks(ctx context.Context, agentAddr string) ([]Webhook, error) {
	path := fmt.Sprintf("/v1/agents/%s/webhooks", agentAddr)
	var out listWebhooksResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Webhooks, nil
}

// DeleteWebhook removes a webhook.
func (c *Client) DeleteWebhook(ctx context.Context, agentAddr, webhookID string) error {
	path := fmt.Sprintf("/v1/agents/%s/webhooks/%s", agentAddr, webhookID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}
