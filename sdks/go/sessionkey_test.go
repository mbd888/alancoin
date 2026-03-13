package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newSessionKeyServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/agents/0xABC/sessions", func(w http.ResponseWriter, r *http.Request) {
		var req CreateSessionKeyRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.PublicKey == "" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "publicKey required"})
			return
		}
		json.NewEncoder(w).Encode(createSessionKeyResponse{
			ID: "sk_test_1",
			Permission: SessionKeyPermission{
				MaxTotal:  req.MaxTotal,
				ExpiresAt: time.Now().Add(time.Hour),
				AllowAny:  req.AllowAny,
				Label:     req.Label,
			},
			Usage: SessionKeyUsage{TotalSpent: "0.00"},
		})
	})

	mux.HandleFunc("GET /v1/agents/0xABC/sessions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listSessionKeysResponse{
			Sessions: []SessionKey{
				{ID: "sk_1", OwnerAddr: "0xABC", PublicKey: "0x04abc..."},
				{ID: "sk_2", OwnerAddr: "0xABC", PublicKey: "0x04def..."},
			},
		})
	})

	mux.HandleFunc("GET /v1/agents/0xABC/sessions/sk_1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(getSessionKeyResponse{
			Session: SessionKey{
				ID:        "sk_1",
				OwnerAddr: "0xABC",
				PublicKey: "0x04abc...",
				Permission: SessionKeyPermission{
					MaxTotal:  "10.00",
					ExpiresAt: time.Now().Add(time.Hour),
				},
				Usage: SessionKeyUsage{
					TransactionCount: 5,
					TotalSpent:       "3.50",
				},
			},
		})
	})

	mux.HandleFunc("DELETE /v1/agents/0xABC/sessions/sk_1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /v1/agents/0xABC/sessions/sk_1/transact", func(w http.ResponseWriter, r *http.Request) {
		var req TransactRequest
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(map[string]any{
			"transaction": TransactResponse{
				TxHash:       "0xtx123",
				From:         "0xABC",
				To:           req.To,
				Amount:       req.Amount,
				SessionKeyID: "sk_1",
				Timestamp:    time.Now(),
			},
		})
	})

	mux.HandleFunc("GET /v1/sessions/sk_1/tree", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(delegationTreeResponse{
			Tree: DelegationTreeNode{
				KeyID:            "sk_1",
				Depth:            0,
				MaxTotal:         "10.00",
				TotalSpent:       "3.50",
				Remaining:        "6.50",
				TransactionCount: 5,
				Active:           true,
				Children: []*DelegationTreeNode{
					{
						KeyID:      "sk_child_1",
						Depth:      1,
						MaxTotal:   "2.00",
						TotalSpent: "0.50",
						Remaining:  "1.50",
						Active:     true,
					},
				},
			},
		})
	})

	mux.HandleFunc("POST /v1/agents/0xABC/sessions/sk_1/rotate", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(getSessionKeyResponse{
			Session: SessionKey{
				ID:            "sk_rotated",
				OwnerAddr:     "0xABC",
				PublicKey:     "0x04new...",
				RotatedFromID: "sk_1",
				Permission: SessionKeyPermission{
					MaxTotal:  "6.50",
					ExpiresAt: time.Now().Add(time.Hour),
				},
				Usage: SessionKeyUsage{TotalSpent: "0.00"},
			},
		})
	})

	mux.HandleFunc("GET /v1/sessions/sk_1/delegation-log", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(delegationLogResponse{
			Log: []DelegationLogEntry{
				{ID: 1, ParentKeyID: "sk_1", ChildKeyID: "sk_child_1", EventType: "delegated", Depth: 1},
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestCreateSessionKey(t *testing.T) {
	srv := newSessionKeyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	resp, err := c.CreateSessionKey(context.Background(), "0xABC", CreateSessionKeyRequest{
		PublicKey: "0x04abc...",
		MaxTotal:  "10.00",
		AllowAny:  true,
		Label:     "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "sk_test_1" {
		t.Errorf("ID = %q", resp.ID)
	}
	if resp.Permission.MaxTotal != "10.00" {
		t.Errorf("MaxTotal = %q", resp.Permission.MaxTotal)
	}
}

func TestListSessionKeys(t *testing.T) {
	srv := newSessionKeyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	keys, err := c.ListSessionKeys(context.Background(), "0xABC")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Errorf("len = %d", len(keys))
	}
}

func TestGetSessionKey(t *testing.T) {
	srv := newSessionKeyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	key, err := c.GetSessionKey(context.Background(), "0xABC", "sk_1")
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != "sk_1" {
		t.Errorf("ID = %q", key.ID)
	}
	if key.Usage.TransactionCount != 5 {
		t.Errorf("TransactionCount = %d", key.Usage.TransactionCount)
	}
	if key.Usage.TotalSpent != "3.50" {
		t.Errorf("TotalSpent = %q", key.Usage.TotalSpent)
	}
}

func TestRevokeSessionKey(t *testing.T) {
	srv := newSessionKeyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	err := c.RevokeSessionKey(context.Background(), "0xABC", "sk_1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransact(t *testing.T) {
	srv := newSessionKeyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	resp, err := c.Transact(context.Background(), "0xABC", "sk_1", TransactRequest{
		To:        "0xDEF",
		Amount:    "0.50",
		Nonce:     1,
		Timestamp: time.Now().Unix(),
		Signature: "0xsig...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TxHash != "0xtx123" {
		t.Errorf("TxHash = %q", resp.TxHash)
	}
	if resp.Amount != "0.50" {
		t.Errorf("Amount = %q", resp.Amount)
	}
}

func TestDelegationTree(t *testing.T) {
	srv := newSessionKeyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	tree, err := c.DelegationTree(context.Background(), "sk_1")
	if err != nil {
		t.Fatal(err)
	}
	if tree.KeyID != "sk_1" {
		t.Errorf("KeyID = %q", tree.KeyID)
	}
	if len(tree.Children) != 1 {
		t.Fatalf("children = %d", len(tree.Children))
	}
	if tree.Children[0].KeyID != "sk_child_1" {
		t.Errorf("child KeyID = %q", tree.Children[0].KeyID)
	}
}

func TestRotateSessionKey(t *testing.T) {
	srv := newSessionKeyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	key, err := c.RotateSessionKey(context.Background(), "0xABC", "sk_1", "0x04new...")
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != "sk_rotated" {
		t.Errorf("ID = %q", key.ID)
	}
	if key.RotatedFromID != "sk_1" {
		t.Errorf("RotatedFromID = %q", key.RotatedFromID)
	}
	if key.Permission.MaxTotal != "6.50" {
		t.Errorf("MaxTotal = %q", key.Permission.MaxTotal)
	}
}

func TestDelegationLog(t *testing.T) {
	srv := newSessionKeyServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	log, err := c.DelegationLog(context.Background(), "sk_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 {
		t.Fatalf("len = %d", len(log))
	}
	if log[0].EventType != "delegated" {
		t.Errorf("EventType = %q", log[0].EventType)
	}
}
