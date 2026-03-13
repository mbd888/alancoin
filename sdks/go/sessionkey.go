package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// CreateSessionKey creates a bounded-autonomy session key for an agent.
// The session key allows constrained spending via ECDSA-signed transactions.
func (c *Client) CreateSessionKey(ctx context.Context, agentAddr string, req CreateSessionKeyRequest) (*createSessionKeyResponse, error) {
	var out createSessionKeyResponse
	path := fmt.Sprintf("/v1/agents/%s/sessions", agentAddr)
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSessionKeys lists all session keys for an agent.
func (c *Client) ListSessionKeys(ctx context.Context, agentAddr string) ([]SessionKey, error) {
	path := fmt.Sprintf("/v1/agents/%s/sessions", agentAddr)
	var out listSessionKeysResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

// GetSessionKey retrieves a specific session key.
func (c *Client) GetSessionKey(ctx context.Context, agentAddr, keyID string) (*SessionKey, error) {
	path := fmt.Sprintf("/v1/agents/%s/sessions/%s", agentAddr, keyID)
	var out getSessionKeyResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Session, nil
}

// RevokeSessionKey revokes a session key, releasing any remaining budget.
func (c *Client) RevokeSessionKey(ctx context.Context, agentAddr, keyID string) error {
	path := fmt.Sprintf("/v1/agents/%s/sessions/%s", agentAddr, keyID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// Transact executes a signed transaction using a session key.
// The request must include a valid ECDSA signature over the transaction fields.
func (c *Client) Transact(ctx context.Context, agentAddr, keyID string, req TransactRequest) (*TransactResponse, error) {
	path := fmt.Sprintf("/v1/agents/%s/sessions/%s/transact", agentAddr, keyID)
	var out struct {
		Transaction TransactResponse `json:"transaction"`
	}
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return &out.Transaction, nil
}

// Delegate creates a child session key from a parent session key.
// Requires ECDSA signature authorization from the parent key holder.
func (c *Client) Delegate(ctx context.Context, parentKeyID string, req DelegateRequest) (map[string]any, error) {
	path := fmt.Sprintf("/v1/sessions/%s/delegate", parentKeyID)
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodPost, path, &req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DelegationTree retrieves the delegation hierarchy rooted at a session key.
func (c *Client) DelegationTree(ctx context.Context, keyID string) (*DelegationTreeNode, error) {
	path := fmt.Sprintf("/v1/sessions/%s/tree", keyID)
	var out delegationTreeResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Tree, nil
}

// DelegationLog retrieves the audit log for a session key's delegation events.
func (c *Client) DelegationLog(ctx context.Context, keyID string) ([]DelegationLogEntry, error) {
	path := fmt.Sprintf("/v1/sessions/%s/delegation-log", keyID)
	var out delegationLogResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Log, nil
}
