package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// AuthInfo returns information about the authentication scheme.
func (c *Client) AuthInfo(ctx context.Context) (*AuthInfo, error) {
	var out AuthInfo
	if err := c.doJSON(ctx, http.MethodGet, "/v1/auth/info", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AuthMe returns the identity of the currently authenticated agent.
func (c *Client) AuthMe(ctx context.Context) (*AuthMe, error) {
	var out AuthMe
	if err := c.doJSON(ctx, http.MethodGet, "/v1/auth/me", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAPIKeys lists all API keys for the authenticated agent.
func (c *Client) ListAPIKeys(ctx context.Context) ([]APIKeyInfo, error) {
	var out listKeysResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/auth/keys", nil, &out); err != nil {
		return nil, err
	}
	return out.Keys, nil
}

// CreateAPIKey creates a new API key for the authenticated agent.
func (c *Client) CreateAPIKey(ctx context.Context, name string) (*CreateAPIKeyResponse, error) {
	req := CreateAPIKeyRequest{Name: name}
	var out CreateAPIKeyResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/auth/keys", &req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeAPIKey revokes an API key by ID.
func (c *Client) RevokeAPIKey(ctx context.Context, keyID string) error {
	path := fmt.Sprintf("/v1/auth/keys/%s", keyID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// RegenerateAPIKey regenerates an API key, returning a new secret.
func (c *Client) RegenerateAPIKey(ctx context.Context, keyID string) (*RegenerateKeyResponse, error) {
	path := fmt.Sprintf("/v1/auth/keys/%s/regenerate", keyID)
	var out RegenerateKeyResponse
	if err := c.doJSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
