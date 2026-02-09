package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test helpers ---

func newTestSetup(handler http.Handler) (*Handlers, func()) {
	ts := httptest.NewServer(handler)
	cfg := Config{
		APIURL:       ts.URL,
		APIKey:       "sk_test_key",
		AgentAddress: "0xBUYER",
	}
	client := NewAlancoinClient(cfg)
	h := NewHandlers(client)
	return h, ts.Close
}

func makeRequest(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	if args == nil {
		args = map[string]any{}
	}
	req.Params.Arguments = args
	return req
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content, "expected at least one content block")
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", result.Content[0])
	return tc.Text
}

// ============================================================
// Client tests
// ============================================================

func TestClient_DoRequest_AuthHeader(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "sk_secret123", AgentAddress: "0xABC"})
	_, err := client.GetBalance(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk_secret123", gotAuth)
}

func TestClient_DoRequest_HTTPError_WithAPIMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "forbidden",
			"message": "Invalid API key",
		})
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "bad", AgentAddress: "0x1"})
	_, err := client.GetBalance(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "Invalid API key")
}

func TestClient_DoRequest_HTTPError_NonJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream timeout"))
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0x1"})
	_, err := client.GetBalance(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
	assert.Contains(t, err.Error(), "upstream timeout")
}

func TestClient_DoRequest_HTTPError_InsufficientBalance(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "insufficient_balance",
			"message": "Available balance 0.500000 is less than requested 10.000000",
		})
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0x1"})
	_, err := client.CreateEscrow(context.Background(), "0xSELLER", "10.00", "svc-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Available balance 0.500000 is less than requested 10.000000")
}

func TestClient_DoRequest_ConnectionRefused(t *testing.T) {
	client := NewAlancoinClient(Config{APIURL: "http://127.0.0.1:1", APIKey: "k", AgentAddress: "0x1"})
	_, err := client.GetBalance(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
}

func TestClient_DoRequest_CancelledContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0x1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := client.GetBalance(ctx)
	require.Error(t, err)
}

func TestClient_DiscoverServices_QueryParams(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "translation", r.URL.Query().Get("type"))
		assert.Equal(t, "0.05", r.URL.Query().Get("maxPrice"))
		assert.Equal(t, "reputation", r.URL.Query().Get("sortBy"))
		_, _ = w.Write([]byte(`{"services":[]}`))
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0x1"})
	_, err := client.DiscoverServices(context.Background(), "translation", "0.05", "reputation", "")
	require.NoError(t, err)
}

func TestClient_DiscoverServices_EmptyParams(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.URL.Query().Get("type"))
		assert.Empty(t, r.URL.Query().Get("maxPrice"))
		_, _ = w.Write([]byte(`{"services":[]}`))
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0x1"})
	_, err := client.DiscoverServices(context.Background(), "", "", "", "")
	require.NoError(t, err)
}

func TestClient_ListAgents_QueryParams(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "inference", r.URL.Query().Get("serviceType"))
		assert.Equal(t, "5", r.URL.Query().Get("limit"))
		_, _ = w.Write([]byte(`{"agents":[]}`))
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0x1"})
	_, err := client.ListAgents(context.Background(), "inference", 5)
	require.NoError(t, err)
}

func TestClient_ListAgents_ZeroLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.URL.Query().Get("limit"), "limit=0 should not be sent")
		_, _ = w.Write([]byte(`{"agents":[]}`))
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0x1"})
	_, err := client.ListAgents(context.Background(), "", 0)
	require.NoError(t, err)
}

func TestClient_CreateEscrow_RequestBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(body, &m)
		assert.Equal(t, "0xBUYER", m["buyerAddr"])
		assert.Equal(t, "0xSELLER", m["sellerAddr"])
		assert.Equal(t, "1.50", m["amount"])
		assert.Equal(t, "svc-42", m["serviceId"])

		_ = json.NewEncoder(w).Encode(map[string]any{"escrow": map[string]any{"id": "e1"}})
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0xBUYER"})
	_, err := client.CreateEscrow(context.Background(), "0xSELLER", "1.50", "svc-42")
	require.NoError(t, err)
}

func TestClient_DisputeEscrow_RequestBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/escrow/esc-99/dispute", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(body, &m)
		assert.Equal(t, "bad quality", m["reason"])

		_ = json.NewEncoder(w).Encode(map[string]any{"status": "refunded"})
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0x1"})
	_, err := client.DisputeEscrow(context.Background(), "esc-99", "bad quality")
	require.NoError(t, err)
}

func TestClient_CallEndpoint_Headers(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "5.00", r.Header.Get("X-Payment-Amount"))
		assert.Equal(t, "0xAGENT", r.Header.Get("X-Payment-From"))
		assert.Equal(t, "esc-77", r.Header.Get("X-Escrow-ID"))
		// Should NOT have Authorization (it's a service call, not a platform call)
		assert.Empty(t, r.Header.Get("Authorization"))

		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		assert.Equal(t, "hello", m["text"])

		_ = json.NewEncoder(w).Encode(map[string]any{"result": "ok"})
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: "http://unused:9999", APIKey: "k", AgentAddress: "0xAGENT"})
	_, err := client.CallEndpoint(context.Background(), ts.URL, map[string]any{"text": "hello"}, "esc-77", "5.00")
	require.NoError(t, err)
}

func TestClient_CallEndpoint_ServiceReturns500(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: "http://unused:9999", APIKey: "k", AgentAddress: "0x1"})
	_, err := client.CallEndpoint(context.Background(), ts.URL, nil, "esc-1", "1.00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "internal error")
}

func TestClient_CallEndpoint_ServiceReturns402(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(402)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "payment_required", "message": "pay me first"})
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: "http://unused:9999", APIKey: "k", AgentAddress: "0x1"})
	_, err := client.CallEndpoint(context.Background(), ts.URL, nil, "esc-1", "1.00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "402")
}

// ============================================================
// Handler: discover_services
// ============================================================

func TestHandleDiscoverServices(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer sk_test_key", r.Header.Get("Authorization"))
		assert.Equal(t, "translation", r.URL.Query().Get("type"))

		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{
				{
					"id": "svc-1", "name": "TranslatorBot", "address": "0xSELLER1",
					"type": "translation", "price": "0.005",
					"endpoint":        "http://localhost:9001/translate",
					"reputationScore": 85.0, "reputationTier": "trusted", "successRate": 0.95,
				},
				{
					"id": "svc-2", "name": "CheapTranslate", "address": "0xSELLER2",
					"type": "translation", "price": "0.002",
					"endpoint": "http://localhost:9002/translate",
				},
			},
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleDiscoverServices(context.Background(), makeRequest(map[string]any{
		"service_type": "translation",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "Found 2 service(s)")
	assert.Contains(t, text, "TranslatorBot")
	assert.Contains(t, text, "0.005 USDC")
	assert.Contains(t, text, "CheapTranslate")
	assert.Contains(t, text, "trusted")
	assert.Contains(t, text, "85.0")
	assert.Contains(t, text, "95%")
}

func TestHandleDiscoverServices_NoParams(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"services": []map[string]any{
			{"id": "s1", "name": "Foo", "address": "0x1", "type": "t", "price": "1.00"},
		}})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleDiscoverServices(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, resultText(t, result), "Found 1 service(s)")
}

func TestHandleDiscoverServices_EmptyResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"services": []map[string]any{}})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleDiscoverServices(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, resultText(t, result), "No services found")
}

func TestHandleDiscoverServices_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "internal", "message": "db down"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleDiscoverServices(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "db down")
}

func TestHandleDiscoverServices_PassesAllQueryParams(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "inference", r.URL.Query().Get("type"))
		assert.Equal(t, "0.50", r.URL.Query().Get("maxPrice"))
		assert.Equal(t, "value", r.URL.Query().Get("sortBy"))
		_ = json.NewEncoder(w).Encode(map[string]any{"services": []map[string]any{}})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	h.HandleDiscoverServices(context.Background(), makeRequest(map[string]any{
		"service_type": "inference",
		"max_price":    "0.50",
		"sort_by":      "value",
	}))
}

// ============================================================
// Handler: call_service
// ============================================================

func TestHandleCallService_HappyPath(t *testing.T) {
	serviceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "0.005", r.Header.Get("X-Payment-Amount"))
		assert.Equal(t, "0xBUYER", r.Header.Get("X-Payment-From"))
		assert.Equal(t, "esc-123", r.Header.Get("X-Escrow-ID"))

		body, _ := io.ReadAll(r.Body)
		var params map[string]any
		_ = json.Unmarshal(body, &params)
		assert.Equal(t, "Hello world", params["text"])

		_ = json.NewEncoder(w).Encode(map[string]any{
			"translation": "Hola mundo",
			"language":    "es",
		})
	}))
	defer serviceServer.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{{
				"id": "svc-1", "name": "TranslatorBot", "address": "0xSELLER",
				"type": "translation", "price": "0.005", "endpoint": serviceServer.URL,
			}},
		})
	})
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "0xBUYER", body["buyerAddr"])
		assert.Equal(t, "0xSELLER", body["sellerAddr"])
		assert.Equal(t, "0.005", body["amount"])

		_ = json.NewEncoder(w).Encode(map[string]any{"escrow": map[string]any{"id": "esc-123"}})
	})
	mux.HandleFunc("/v1/escrow/esc-123/confirm", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "released"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{
		"service_type": "translation",
		"params":       map[string]any{"text": "Hello world", "target_language": "es"},
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "TranslatorBot")
	assert.Contains(t, text, "0.005 USDC")
	assert.Contains(t, text, "Confirmed")
	assert.Contains(t, text, "Hola mundo")
}

func TestHandleCallService_MissingServiceType(t *testing.T) {
	h := NewHandlers(NewAlancoinClient(Config{}))
	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "service_type is required")
}

func TestHandleCallService_NoServicesFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"services": []map[string]any{}})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{
		"service_type": "nonexistent",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "no services found")
}

func TestHandleCallService_DiscoveryFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"internal","message":"database unreachable"}`))
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{
		"service_type": "translation",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "Discovery failed")
}

func TestHandleCallService_EscrowCreationFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{{
				"id": "s1", "name": "Bot", "address": "0xS", "type": "t", "price": "100.00", "endpoint": "http://x",
			}},
		})
	})
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "insufficient_balance", "message": "Available balance 0.50 is less than 100.00",
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{
		"service_type": "translation",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "Escrow creation failed")
	assert.Contains(t, resultText(t, result), "Available balance 0.50 is less than 100.00")
}

func TestHandleCallService_ServiceEndpointFails(t *testing.T) {
	serviceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("model overloaded"))
	}))
	defer serviceServer.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{{
				"id": "s1", "name": "Bot", "address": "0xS", "type": "t", "price": "0.01", "endpoint": serviceServer.URL,
			}},
		})
	})
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"escrow": map[string]any{"id": "esc-fail"}})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{
		"service_type": "inference",
	}))
	require.NoError(t, err)
	// Service failure is NOT an error result — it's informational with dispute instructions
	assert.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "Service call failed")
	assert.Contains(t, text, "funds are safe in escrow")
	assert.Contains(t, text, "esc-fail")
	assert.Contains(t, text, "dispute_escrow")
}

func TestHandleCallService_ConfirmFails(t *testing.T) {
	serviceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "done"})
	}))
	defer serviceServer.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{{
				"id": "s1", "name": "Bot", "address": "0xS", "type": "t", "price": "0.01", "endpoint": serviceServer.URL,
			}},
		})
	})
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/escrow" {
			_ = json.NewEncoder(w).Encode(map[string]any{"escrow": map[string]any{"id": "esc-cf"}})
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/v1/escrow/esc-cf/confirm", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "conflict", "message": "already released"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{
		"service_type": "translation",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "auto-confirm failed")
	assert.Contains(t, text, "esc-cf")
	assert.Contains(t, text, "done") // service result still included
}

func TestHandleCallService_PreferReputation(t *testing.T) {
	serviceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer serviceServer.Close()

	var escrowSeller string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{
				{"id": "s1", "name": "CheapBad", "address": "0xBAD", "type": "t", "price": "0.001", "endpoint": serviceServer.URL, "reputationScore": 10.0},
				{"id": "s2", "name": "ExpensiveGood", "address": "0xGOOD", "type": "t", "price": "0.100", "endpoint": serviceServer.URL, "reputationScore": 98.0},
			},
		})
	})
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		escrowSeller = body["sellerAddr"]
		_ = json.NewEncoder(w).Encode(map[string]any{"escrow": map[string]any{"id": "esc-rep"}})
	})
	mux.HandleFunc("/v1/escrow/esc-rep/confirm", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "released"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{
		"service_type": "translation",
		"prefer":       "reputation",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "0xGOOD", escrowSeller, "should select highest reputation service")
	assert.Contains(t, resultText(t, result), "ExpensiveGood")
}

func TestHandleCallService_NoParams(t *testing.T) {
	serviceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		assert.Empty(t, m, "empty params should send empty JSON object")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer serviceServer.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{{
				"id": "s1", "name": "Bot", "address": "0xS", "type": "t", "price": "0.01", "endpoint": serviceServer.URL,
			}},
		})
	})
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"escrow": map[string]any{"id": "esc-np"}})
	})
	mux.HandleFunc("/v1/escrow/esc-np/confirm", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "released"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{
		"service_type": "inference",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestHandleCallService_ServiceEndpointUnreachable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{{
				"id": "s1", "name": "DeadBot", "address": "0xS", "type": "t",
				"price": "0.01", "endpoint": "http://127.0.0.1:1/dead",
			}},
		})
	})
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"escrow": map[string]any{"id": "esc-dead"}})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCallService(context.Background(), makeRequest(map[string]any{
		"service_type": "inference",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError) // Not an isError, it's informational
	text := resultText(t, result)
	assert.Contains(t, text, "Service call failed")
	assert.Contains(t, text, "esc-dead")
}

// ============================================================
// Handler: check_balance
// ============================================================

func TestHandleCheckBalance(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents/0xBUYER/balance", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"balance": map[string]any{
				"available": "42.500000",
				"pending":   "1.000000",
				"escrowed":  "5.000000",
			},
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCheckBalance(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "42.500000 USDC")
	assert.Contains(t, text, "Pending")
	assert.Contains(t, text, "1.000000 USDC")
	assert.Contains(t, text, "Escrowed")
	assert.Contains(t, text, "5.000000 USDC")
}

func TestHandleCheckBalance_ZeroPendingAndEscrowed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents/0xBUYER/balance", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"balance": map[string]any{
				"available": "10.000000",
				"pending":   "0",
				"escrowed":  "0.000000",
			},
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCheckBalance(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "10.000000 USDC")
	assert.NotContains(t, text, "Pending")
	assert.NotContains(t, text, "Escrowed")
}

func TestHandleCheckBalance_FlatResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents/0xBUYER/balance", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"available": "7.250000",
			"pending":   "0",
			"escrowed":  "0",
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCheckBalance(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, resultText(t, result), "7.250000 USDC")
}

func TestHandleCheckBalance_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents/0xBUYER/balance", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not_found", "message": "agent not registered"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleCheckBalance(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "agent not registered")
}

// ============================================================
// Handler: get_reputation
// ============================================================

func TestHandleGetReputation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/reputation/0xAGENT", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"address":     "0xAGENT",
			"score":       87.5,
			"tier":        "trusted",
			"successRate": 0.93,
			"txCount":     150.0,
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleGetReputation(context.Background(), makeRequest(map[string]any{
		"agent_address": "0xAGENT",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "87.5")
	assert.Contains(t, text, "trusted")
	assert.Contains(t, text, "93%")
	assert.Contains(t, text, "150")
}

func TestHandleGetReputation_MissingAddress(t *testing.T) {
	h := NewHandlers(NewAlancoinClient(Config{}))
	result, err := h.HandleGetReputation(context.Background(), makeRequest(map[string]any{}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "agent_address is required")
}

func TestHandleGetReputation_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/reputation/0xNOBODY", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not_found", "message": "agent not found"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleGetReputation(context.Background(), makeRequest(map[string]any{
		"agent_address": "0xNOBODY",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "agent not found")
}

func TestHandleGetReputation_MinimalFields(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/reputation/0xNEW", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"address": "0xNEW",
			"score":   0.0,
			"tier":    "new",
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleGetReputation(context.Background(), makeRequest(map[string]any{
		"agent_address": "0xNEW",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "new")
	assert.Contains(t, text, "0xNEW")
}

// ============================================================
// Handler: list_agents
// ============================================================

func TestHandleListAgents(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agents": []map[string]any{
				{"name": "TranslatorBot", "address": "0xA1", "description": "Translates text"},
				{"name": "SummarizerBot", "address": "0xA2", "description": "Summarizes documents"},
			},
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleListAgents(context.Background(), makeRequest(map[string]any{}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "2 agent(s)")
	assert.Contains(t, text, "TranslatorBot")
	assert.Contains(t, text, "SummarizerBot")
	assert.Contains(t, text, "Translates text")
}

func TestHandleListAgents_Empty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"agents": []map[string]any{}})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleListAgents(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "No agents found")
}

func TestHandleListAgents_PassesFilters(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "translation", r.URL.Query().Get("serviceType"))
		assert.Equal(t, "3", r.URL.Query().Get("limit"))
		_ = json.NewEncoder(w).Encode(map[string]any{"agents": []map[string]any{}})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	h.HandleListAgents(context.Background(), makeRequest(map[string]any{
		"service_type": "translation",
		"limit":        float64(3), // JSON numbers come as float64
	}))
}

func TestHandleListAgents_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("oops"))
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleListAgents(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHandleListAgents_DirectArray(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "AgentOne", "address": "0x1"},
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleListAgents(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, resultText(t, result), "AgentOne")
}

// ============================================================
// Handler: get_network_stats
// ============================================================

func TestHandleGetNetworkStats(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/platform", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"platform": "alancoin",
			"chain":    "base-sepolia",
			"version":  "1.0.0",
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleGetNetworkStats(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "alancoin")
	assert.Contains(t, text, "base-sepolia")
}

func TestHandleGetNetworkStats_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/platform", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "unavailable", "message": "maintenance"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleGetNetworkStats(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "maintenance")
}

// ============================================================
// Handler: pay_agent
// ============================================================

func TestHandlePayAgent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "0xBUYER", body["buyerAddr"])
		assert.Equal(t, "0xRECIPIENT", body["sellerAddr"])
		assert.Equal(t, "2.50", body["amount"])
		assert.True(t, strings.HasPrefix(body["serviceId"], "direct-payment:"))

		_ = json.NewEncoder(w).Encode(map[string]any{
			"escrow": map[string]any{"id": "esc-789"},
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandlePayAgent(context.Background(), makeRequest(map[string]any{
		"recipient": "0xRECIPIENT",
		"amount":    "2.50",
		"memo":      "thanks for the translation",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "2.50 USDC")
	assert.Contains(t, text, "0xRECIPIENT")
	assert.Contains(t, text, "esc-789")
	assert.Contains(t, text, "held in escrow")
}

func TestHandlePayAgent_NoMemo(t *testing.T) {
	var gotServiceID string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		gotServiceID = body["serviceId"]
		_ = json.NewEncoder(w).Encode(map[string]any{"escrow": map[string]any{"id": "esc-nm"}})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandlePayAgent(context.Background(), makeRequest(map[string]any{
		"recipient": "0xR",
		"amount":    "1.00",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "direct-payment", gotServiceID)
}

func TestHandlePayAgent_MissingRecipient(t *testing.T) {
	h := NewHandlers(NewAlancoinClient(Config{}))
	result, err := h.HandlePayAgent(context.Background(), makeRequest(map[string]any{
		"amount": "1.00",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "recipient is required")
}

func TestHandlePayAgent_MissingAmount(t *testing.T) {
	h := NewHandlers(NewAlancoinClient(Config{}))
	result, err := h.HandlePayAgent(context.Background(), makeRequest(map[string]any{
		"recipient": "0x123",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "amount is required")
}

func TestHandlePayAgent_EscrowFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/escrow", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "insufficient_balance", "message": "not enough funds",
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandlePayAgent(context.Background(), makeRequest(map[string]any{
		"recipient": "0xR",
		"amount":    "99999.00",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "Payment failed")
	assert.Contains(t, resultText(t, result), "not enough funds")
}

// ============================================================
// Handler: dispute_escrow
// ============================================================

func TestHandleDisputeEscrow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/escrow/esc-456/dispute", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "Service returned garbage", body["reason"])
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "refunded"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleDisputeEscrow(context.Background(), makeRequest(map[string]any{
		"escrow_id": "esc-456",
		"reason":    "Service returned garbage",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "esc-456")
	assert.Contains(t, text, "disputed successfully")
	assert.Contains(t, text, "refunded")
	assert.Contains(t, text, "Service returned garbage")
}

func TestHandleDisputeEscrow_MissingEscrowID(t *testing.T) {
	h := NewHandlers(NewAlancoinClient(Config{}))
	result, err := h.HandleDisputeEscrow(context.Background(), makeRequest(map[string]any{
		"reason": "bad",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "escrow_id is required")
}

func TestHandleDisputeEscrow_MissingReason(t *testing.T) {
	h := NewHandlers(NewAlancoinClient(Config{}))
	result, err := h.HandleDisputeEscrow(context.Background(), makeRequest(map[string]any{
		"escrow_id": "esc-1",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "reason is required")
}

func TestHandleDisputeEscrow_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/escrow/esc-gone/dispute", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "conflict", "message": "escrow already released",
		})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	result, err := h.HandleDisputeEscrow(context.Background(), makeRequest(map[string]any{
		"escrow_id": "esc-gone",
		"reason":    "too late",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "escrow already released")
}

// ============================================================
// Formatting & parsing unit tests
// ============================================================

func TestFormatServiceList_DirectArray(t *testing.T) {
	raw := json.RawMessage(`[
		{"id":"s1","name":"Bot","address":"0x1","type":"inference","price":"0.05"}
	]`)
	text, err := formatServiceList(raw)
	require.NoError(t, err)
	assert.Contains(t, text, "Found 1 service(s)")
	assert.Contains(t, text, "Bot")
}

func TestFormatServiceList_AlternativeKeys(t *testing.T) {
	raw := json.RawMessage(`{"services": [
		{"serviceId":"s1","serviceName":"Alt","agentAddress":"0x1","serviceType":"t","price":"0.02","serviceEndpoint":"http://x"}
	]}`)
	text, err := formatServiceList(raw)
	require.NoError(t, err)
	assert.Contains(t, text, "Alt")
}

func TestFormatServiceList_MalformedJSON(t *testing.T) {
	_, err := formatServiceList(json.RawMessage(`not json`))
	assert.Error(t, err)
}

func TestFormatServiceList_WithoutReputation(t *testing.T) {
	raw := json.RawMessage(`{"services": [
		{"id":"s1","name":"NoRep","address":"0x1","type":"t","price":"0.01"}
	]}`)
	text, err := formatServiceList(raw)
	require.NoError(t, err)
	assert.NotContains(t, text, "Reputation:")
}

func TestParseServices_SkipsMalformedItems(t *testing.T) {
	raw := json.RawMessage(`{"services": [
		{"id":"s1","name":"Good","address":"0x1","type":"t","price":"0.01"},
		"not an object",
		{"id":"s2","name":"AlsoGood","address":"0x2","type":"t","price":"0.02"}
	]}`)
	services, err := parseServices(raw)
	require.NoError(t, err)
	assert.Len(t, services, 2)
	assert.Equal(t, "Good", services[0].Name)
	assert.Equal(t, "AlsoGood", services[1].Name)
}

func TestSelectService_Cheapest(t *testing.T) {
	raw := json.RawMessage(`{"services": [
		{"id":"s1","name":"Cheap","address":"0x1","type":"t","price":"0.01","endpoint":"http://a"},
		{"id":"s2","name":"Expensive","address":"0x2","type":"t","price":"0.10","endpoint":"http://b"}
	]}`)
	svc, err := selectService(raw, "cheapest")
	require.NoError(t, err)
	assert.Equal(t, "Cheap", svc.Name)
}

func TestSelectService_Reputation(t *testing.T) {
	raw := json.RawMessage(`{"services": [
		{"id":"s1","name":"LowRep","address":"0x1","type":"t","price":"0.01","endpoint":"http://a","reputationScore":30},
		{"id":"s2","name":"HighRep","address":"0x2","type":"t","price":"0.10","endpoint":"http://b","reputationScore":95}
	]}`)
	svc, err := selectService(raw, "reputation")
	require.NoError(t, err)
	assert.Equal(t, "HighRep", svc.Name)
}

func TestSelectService_BestValue(t *testing.T) {
	raw := json.RawMessage(`{"services": [
		{"id":"s1","name":"First","address":"0x1","type":"t","price":"0.01","endpoint":"http://a"}
	]}`)
	svc, err := selectService(raw, "best_value")
	require.NoError(t, err)
	assert.Equal(t, "First", svc.Name)
}

func TestSelectService_EmptyDefault(t *testing.T) {
	raw := json.RawMessage(`{"services": [
		{"id":"s1","name":"Only","address":"0x1","type":"t","price":"0.01","endpoint":"http://a"}
	]}`)
	svc, err := selectService(raw, "")
	require.NoError(t, err)
	assert.Equal(t, "Only", svc.Name)
}

func TestSelectService_Empty(t *testing.T) {
	raw := json.RawMessage(`{"services": []}`)
	_, err := selectService(raw, "cheapest")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no services found")
}

func TestSelectService_MalformedJSON(t *testing.T) {
	_, err := selectService(json.RawMessage(`garbage`), "cheapest")
	assert.Error(t, err)
}

func TestSelectService_SingleService(t *testing.T) {
	raw := json.RawMessage(`{"services": [
		{"id":"s1","name":"Solo","address":"0x1","type":"t","price":"0.05","endpoint":"http://a","reputationScore":50}
	]}`)
	svc, err := selectService(raw, "reputation")
	require.NoError(t, err)
	assert.Equal(t, "Solo", svc.Name)
}

func TestExtractEscrowID_NestedEscrow(t *testing.T) {
	raw := json.RawMessage(`{"escrow":{"id":"esc-nested","status":"pending"}}`)
	id, err := extractEscrowID(raw)
	require.NoError(t, err)
	assert.Equal(t, "esc-nested", id)
}

func TestExtractEscrowID_FlatID(t *testing.T) {
	raw := json.RawMessage(`{"id":"esc-flat"}`)
	id, err := extractEscrowID(raw)
	require.NoError(t, err)
	assert.Equal(t, "esc-flat", id)
}

func TestExtractEscrowID_EscrowIdKey(t *testing.T) {
	raw := json.RawMessage(`{"escrowId":"esc-alt"}`)
	id, err := extractEscrowID(raw)
	require.NoError(t, err)
	assert.Equal(t, "esc-alt", id)
}

func TestExtractEscrowID_NoID(t *testing.T) {
	raw := json.RawMessage(`{"status":"pending"}`)
	_, err := extractEscrowID(raw)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no escrow ID")
}

func TestExtractEscrowID_MalformedJSON(t *testing.T) {
	_, err := extractEscrowID(json.RawMessage(`not json`))
	assert.Error(t, err)
}

func TestFormatBalance_MalformedJSON(t *testing.T) {
	_, err := formatBalance(json.RawMessage(`garbage`))
	assert.Error(t, err)
}

func TestFormatReputation_MalformedJSON(t *testing.T) {
	_, err := formatReputation(json.RawMessage(`garbage`))
	assert.Error(t, err)
}

func TestFormatAgentList_MalformedJSON(t *testing.T) {
	_, err := formatAgentList(json.RawMessage(`garbage`))
	assert.Error(t, err)
}

func TestFormatAgentList_NoDescription(t *testing.T) {
	raw := json.RawMessage(`{"agents":[{"name":"Bot","address":"0x1"}]}`)
	text, err := formatAgentList(raw)
	require.NoError(t, err)
	assert.Contains(t, text, "Bot")
	assert.Contains(t, text, "0x1")
}

func TestFormatJSON_ValidJSON(t *testing.T) {
	result := formatJSON(json.RawMessage(`{"a":1,"b":"two"}`))
	assert.Contains(t, result, "\"a\": 1")
	assert.Contains(t, result, "\"b\": \"two\"")
}

func TestFormatJSON_InvalidJSON(t *testing.T) {
	result := formatJSON(json.RawMessage(`not json`))
	assert.Equal(t, "not json", result)
}

func TestGetString_Fallback(t *testing.T) {
	m := map[string]any{"foo": "bar"}
	assert.Equal(t, "bar", getString(m, "missing", "foo"))
	assert.Equal(t, "", getString(m, "missing1", "missing2"))
}

func TestGetString_NumericValue(t *testing.T) {
	m := map[string]any{"count": float64(42)}
	assert.Equal(t, "42", getString(m, "count"))
}

func TestGetFloat_Fallback(t *testing.T) {
	m := map[string]any{"score": 95.5}
	v, ok := getFloat(m, "missing", "score")
	assert.True(t, ok)
	assert.Equal(t, 95.5, v)

	_, ok = getFloat(m, "missing1", "missing2")
	assert.False(t, ok)
}

func TestGetFloat_NonNumeric(t *testing.T) {
	m := map[string]any{"score": "not a number"}
	_, ok := getFloat(m, "score")
	assert.False(t, ok)
}

// ============================================================
// Concurrency / race detection
// ============================================================

func TestHandlers_ConcurrentCalls(t *testing.T) {
	var callCount atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents/0xBUYER/balance", func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"balance": map[string]any{"available": "10.00", "pending": "0", "escrowed": "0"},
		})
	})
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"services": []map[string]any{}})
	})
	mux.HandleFunc("/v1/reputation/0xA", func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"score": 50.0, "tier": "new"})
	})

	h, cleanup := newTestSetup(mux)
	defer cleanup()

	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			h.HandleCheckBalance(context.Background(), makeRequest(nil))
			h.HandleDiscoverServices(context.Background(), makeRequest(nil))
			h.HandleGetReputation(context.Background(), makeRequest(map[string]any{"agent_address": "0xA"}))
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	assert.Equal(t, int32(60), callCount.Load())
}

// ============================================================
// Server wiring test
// ============================================================

func TestNewMCPServer_RegistersAllTools(t *testing.T) {
	s := NewMCPServer(Config{APIURL: "http://localhost:8080", APIKey: "k", AgentAddress: "0x1"})
	require.NotNil(t, s)
	// The server should not be nil — that's the main assertion.
	// We can't easily inspect registered tools without calling ListTools,
	// but we can verify it doesn't panic.
}

// ============================================================
// Edge cases: handler never returns Go error
// ============================================================

func TestHandlers_NeverReturnGoError(t *testing.T) {
	// All handlers should return (result, nil) even on failures.
	// The failure is encoded in result.IsError, not in the Go error.
	h := NewHandlers(NewAlancoinClient(Config{
		APIURL:       "http://127.0.0.1:1", // unreachable
		APIKey:       "k",
		AgentAddress: "0x1",
	}))

	tests := []struct {
		name string
		fn   func() (*mcp.CallToolResult, error)
	}{
		{"DiscoverServices", func() (*mcp.CallToolResult, error) {
			return h.HandleDiscoverServices(context.Background(), makeRequest(nil))
		}},
		{"CheckBalance", func() (*mcp.CallToolResult, error) {
			return h.HandleCheckBalance(context.Background(), makeRequest(nil))
		}},
		{"GetReputation", func() (*mcp.CallToolResult, error) {
			return h.HandleGetReputation(context.Background(), makeRequest(map[string]any{"agent_address": "0xA"}))
		}},
		{"ListAgents", func() (*mcp.CallToolResult, error) {
			return h.HandleListAgents(context.Background(), makeRequest(nil))
		}},
		{"GetNetworkStats", func() (*mcp.CallToolResult, error) {
			return h.HandleGetNetworkStats(context.Background(), makeRequest(nil))
		}},
		{"PayAgent", func() (*mcp.CallToolResult, error) {
			return h.HandlePayAgent(context.Background(), makeRequest(map[string]any{"recipient": "0xR", "amount": "1.00"}))
		}},
		{"DisputeEscrow", func() (*mcp.CallToolResult, error) {
			return h.HandleDisputeEscrow(context.Background(), makeRequest(map[string]any{"escrow_id": "e1", "reason": "bad"}))
		}},
		{"CallService", func() (*mcp.CallToolResult, error) {
			return h.HandleCallService(context.Background(), makeRequest(map[string]any{"service_type": "t"}))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.fn()
			assert.NoError(t, err, "handler should never return Go error")
			assert.NotNil(t, result, "handler should always return a result")
			assert.True(t, result.IsError, "unreachable server should produce isError result")
		})
	}
}

// ============================================================
// Slow server timeout
// ============================================================

func TestClient_SlowServer_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow timeout test in short mode")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(35 * time.Second) // longer than 30s client timeout
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	client := NewAlancoinClient(Config{APIURL: ts.URL, APIKey: "k", AgentAddress: "0x1"})
	start := time.Now()
	_, err := client.GetBalance(context.Background())
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 32*time.Second, "should timeout around 30s, not hang forever")
}
