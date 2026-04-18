package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// complianceFakeHandler returns a mux that simulates the subset of the
// Alancoin REST API that the compliance-related MCP handlers call.
// Returned as http.Handler so newTestSetup can own the httptest server.
func complianceFakeHandler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/compliance/", func(w http.ResponseWriter, r *http.Request) {
		// readiness: GET .../readiness
		// incidents: GET .../incidents
		// ack:       POST /v1/compliance/incidents/:id/ack
		switch {
		case strings.HasSuffix(r.URL.Path, "/readiness") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"report": map[string]any{
					"scope":          "tenant_a",
					"enabledCount":   2,
					"degradedCount":  1,
					"disabledCount":  0,
					"incidents":      map[string]any{"info": 1, "warning": 2, "critical": 0, "open": 3},
					"chainHeadHash":  "abcdef1234567890",
					"chainHeadIndex": 9,
					"chainReceipts":  10,
				},
			})
		case strings.HasSuffix(r.URL.Path, "/incidents") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 1,
				"incidents": []map[string]any{
					{
						"id":         "inc_1",
						"scope":      "tenant_a",
						"source":     "forensics",
						"severity":   "warning",
						"kind":       "velocity_spike",
						"title":      "unusual velocity",
						"agentAddr":  "0xabc",
						"occurredAt": "2026-04-16T10:00:00Z",
					},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/ack") && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"incident": map[string]any{
					"id":           "inc_1",
					"acknowledged": true,
					"ackBy":        body["actor"],
				},
			})
		default:
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/v1/chains/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/head") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"head": map[string]any{
					"scope":     "tenant_a",
					"headHash":  "0123456789abcdef",
					"headIndex": 5,
					"receiptId": "rcpt_xyz",
					"updatedAt": "2026-04-16T10:00:00Z",
				},
			})
		case strings.HasSuffix(r.URL.Path, "/verify") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"report": map[string]any{
					"scope":     "tenant_a",
					"status":    "intact",
					"count":     6,
					"lastIndex": 5,
					"lastHash":  "0123456789abcdef",
				},
			})
		case strings.HasSuffix(r.URL.Path, "/bundle") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"format": "alancoin.receipts.bundle.v1",
				"manifest": map[string]any{
					"scope":        "tenant_a",
					"receiptCount": 6,
					"lowerIndex":   0,
					"upperIndex":   5,
					"merkleRoot":   "cafebabe12345678",
					"generatedAt":  "2026-04-16T10:00:00Z",
					"signature":    "deadbeef",
				},
				"receipts": []any{},
			})
		default:
			http.NotFound(w, r)
		}
	})

	return mux
}

func TestMCP_HandleGetComplianceStatus(t *testing.T) {
	handler := complianceFakeHandler(t)

	h, close := newTestSetup(handler)
	defer close()

	result, err := h.HandleGetComplianceStatus(context.Background(), makeRequest(map[string]any{"scope": "tenant_a"}))
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "tenant_a")
	assert.Contains(t, text, "3 open")
	assert.Contains(t, text, "warning=2")
	assert.Contains(t, text, "10 receipts")
}

func TestMCP_HandleListIncidents(t *testing.T) {
	handler := complianceFakeHandler(t)

	h, close := newTestSetup(handler)
	defer close()

	result, err := h.HandleListIncidents(context.Background(), makeRequest(map[string]any{
		"scope":        "tenant_a",
		"severity":     "warning",
		"only_unacked": "true",
		"limit":        float64(10),
	}))
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "1 incident")
	assert.Contains(t, text, "velocity")
}

func TestMCP_HandleAcknowledgeIncident(t *testing.T) {
	handler := complianceFakeHandler(t)

	h, close := newTestSetup(handler)
	defer close()

	result, err := h.HandleAcknowledgeIncident(context.Background(), makeRequest(map[string]any{
		"incident_id": "inc_1",
		"actor":       "analyst@example.com",
		"note":        "false positive",
	}))
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "acknowledged")
	assert.Contains(t, text, "inc_1")
}

func TestMCP_HandleAcknowledgeIncident_MissingArgs(t *testing.T) {
	h, close := newTestSetup(http.NotFoundHandler())
	defer close()

	result, err := h.HandleAcknowledgeIncident(context.Background(), makeRequest(map[string]any{
		"incident_id": "inc_1",
		// actor missing
	}))
	require.NoError(t, err)
	// Tool-level error surfaces through result.IsError=true but text still populated
	text := resultText(t, result)
	assert.Contains(t, text, "actor")
}

func TestMCP_HandleGetChainHead(t *testing.T) {
	handler := complianceFakeHandler(t)

	h, close := newTestSetup(handler)
	defer close()

	result, err := h.HandleGetChainHead(context.Background(), makeRequest(map[string]any{"scope": "tenant_a"}))
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "head index 5")
}

func TestMCP_HandleVerifyChain(t *testing.T) {
	handler := complianceFakeHandler(t)

	h, close := newTestSetup(handler)
	defer close()

	result, err := h.HandleVerifyChain(context.Background(), makeRequest(map[string]any{"scope": "tenant_a"}))
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "intact")
}

func TestMCP_HandleExportAuditBundle(t *testing.T) {
	handler := complianceFakeHandler(t)

	h, close := newTestSetup(handler)
	defer close()

	result, err := h.HandleExportAuditBundle(context.Background(), makeRequest(map[string]any{
		"scope": "tenant_a",
		"since": "2026-04-01T00:00:00Z",
	}))
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "Audit bundle for 'tenant_a'")
	assert.Contains(t, text, "6 receipts")
	// Bundle JSON is attached after the header so clients can persist it.
	assert.Contains(t, text, `"format":"alancoin.receipts.bundle.v1"`)
}
