package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/auth/info", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(AuthInfo{
			Type:               "api_key",
			Header:             "Authorization: Bearer sk_...",
			AltHeader:          "X-API-Key: sk_...",
			Note:               "Store it securely.",
			PublicEndpoints:    []string{"GET /v1/agents"},
			ProtectedEndpoints: []string{"DELETE /v1/agents/:address"},
		})
	})

	mux.HandleFunc("GET /v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(AuthMe{
			AgentAddress: "0xABC",
			KeyID:        "key_1",
			KeyName:      "default",
			CreatedAt:    now,
		})
	})

	mux.HandleFunc("GET /v1/auth/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listKeysResponse{
			Keys: []APIKeyInfo{
				{ID: "key_1", Name: "default", CreatedAt: now},
				{ID: "key_2", Name: "secondary", CreatedAt: now, Revoked: true},
			},
			Count: 2,
		})
	})

	mux.HandleFunc("POST /v1/auth/keys", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(CreateAPIKeyResponse{
			APIKey:  "sk_new_key",
			KeyID:   "key_3",
			Name:    "test-key",
			Warning: "Store this key securely.",
		})
	})

	mux.HandleFunc("DELETE /v1/auth/keys/key_2", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /v1/auth/keys/key_1/regenerate", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RegenerateKeyResponse{
			APIKey:   "sk_regenerated",
			KeyID:    "key_1_new",
			OldKeyID: "key_1",
			Warning:  "Store this key securely.",
		})
	})

	return httptest.NewServer(mux)
}

func TestAuthInfo(t *testing.T) {
	srv := newAuthServer(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	info, err := c.AuthInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != "api_key" {
		t.Errorf("Type = %q", info.Type)
	}
	if len(info.PublicEndpoints) != 1 {
		t.Errorf("PublicEndpoints = %v", info.PublicEndpoints)
	}
}

func TestAuthMe(t *testing.T) {
	srv := newAuthServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	me, err := c.AuthMe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if me.AgentAddress != "0xABC" {
		t.Errorf("AgentAddress = %q", me.AgentAddress)
	}
	if me.KeyID != "key_1" {
		t.Errorf("KeyID = %q", me.KeyID)
	}
}

func TestListAPIKeys(t *testing.T) {
	srv := newAuthServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	keys, err := c.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Errorf("len = %d", len(keys))
	}
}

func TestCreateAPIKey(t *testing.T) {
	srv := newAuthServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	resp, err := c.CreateAPIKey(context.Background(), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if resp.KeyID != "key_3" {
		t.Errorf("KeyID = %q", resp.KeyID)
	}
	if resp.APIKey != "sk_new_key" {
		t.Errorf("APIKey = %q", resp.APIKey)
	}
}

func TestRevokeAPIKey(t *testing.T) {
	srv := newAuthServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	err := c.RevokeAPIKey(context.Background(), "key_2")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRegenerateAPIKey(t *testing.T) {
	srv := newAuthServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	resp, err := c.RegenerateAPIKey(context.Background(), "key_1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OldKeyID != "key_1" {
		t.Errorf("OldKeyID = %q", resp.OldKeyID)
	}
	if resp.APIKey != "sk_regenerated" {
		t.Errorf("APIKey = %q", resp.APIKey)
	}
}
