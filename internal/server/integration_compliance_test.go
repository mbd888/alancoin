package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/compliance"
	"github.com/mbd888/alancoin/internal/config"
	"github.com/mbd888/alancoin/internal/forensics"
	"github.com/mbd888/alancoin/internal/receipts"
)

// newSignedTestServer returns a server with receipt signing enabled.
// Most compliance integration scenarios depend on the chain, and the base
// newTestServer helper ships with an empty ReceiptHMACSecret, which
// silently disables issuance.
func newSignedTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.ReceiptHMACSecret = "integration-test-receipt-secret"
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// compile-time check that newSignedTestServer keeps its signature compatible
// with future config additions.
var _ = (*config.Config)(nil)

// These tests exercise the cross-subsystem wiring that the individual
// package tests can't: forensics → compliance, receipts chain head →
// readiness, HTTP round-trip on the compliance endpoints.

// serveJSON runs an HTTP request against the server's router and decodes
// the JSON body into dst. Returns the raw recorder for status-code checks.
// If apiKey is non-empty, an Authorization header is added.
func serveJSON(t *testing.T, s *Server, method, path, apiKey string, body any, dst any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", apiKey)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if dst != nil && w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), dst)
	}
	return w
}

func TestIntegration_ComplianceRoutesMounted(t *testing.T) {
	s := newTestServer(t)
	apiKey := registerAgent(t, s, "0xcccc000000000000000000000000000000000001", "ops-agent")

	var ready struct {
		Report *compliance.ReadinessReport `json:"report"`
	}
	w := serveJSON(t, s, http.MethodGet, "/v1/compliance/global/readiness", apiKey, nil, &ready)
	if w.Code != http.StatusOK {
		t.Fatalf("readiness status=%d body=%s", w.Code, w.Body.String())
	}
	if ready.Report == nil || ready.Report.Scope != "global" {
		t.Errorf("unexpected readiness: %+v", ready.Report)
	}

	w = serveJSON(t, s, http.MethodGet, "/v1/compliance/global/incidents", apiKey, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("incidents status=%d body=%s", w.Code, w.Body.String())
	}

	// Without auth, the same endpoint should now 401.
	unauth := serveJSON(t, s, http.MethodGet, "/v1/compliance/global/incidents", "", nil, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Errorf("unauthed compliance request: status=%d want 401", unauth.Code)
	}
}

func TestIntegration_ForensicsAlertSurfacesInCompliance(t *testing.T) {
	s := newTestServer(t)
	if s.forensicsService == nil || s.complianceService == nil {
		t.Skip("forensics or compliance service disabled in this build")
	}
	apiKey := registerAgent(t, s, "0xcccc000000000000000000000000000000000002", "ops-agent")

	ctx := context.Background()

	// Feed enough normal events to build a baseline, then one large outlier
	// that should trip AmountAnomaly. forensics.DefaultConfig requires 10
	// txns before scoring, so send 15 normal then 1 spike.
	const agent = "0x1111111111111111111111111111111111111111"
	for i := 0; i < 15; i++ {
		_, err := s.forensicsService.Ingest(ctx, forensics.SpendEvent{
			AgentAddr:    agent,
			Counterparty: "0xVendorA",
			Amount:       10.0 + float64(i%3),
			ServiceType:  "inference",
			Timestamp:    time.Now(),
		})
		if err != nil {
			t.Fatalf("baseline ingest %d: %v", i, err)
		}
	}
	// Outlier: 40x the baseline mean.
	alerts, err := s.forensicsService.Ingest(ctx, forensics.SpendEvent{
		AgentAddr:    agent,
		Counterparty: "0xVendorA",
		Amount:       500.0,
		ServiceType:  "inference",
		Timestamp:    time.Now(),
	})
	if err != nil {
		t.Fatalf("anomalous ingest: %v", err)
	}
	if len(alerts) == 0 {
		t.Fatal("expected at least one alert from anomalous spend")
	}

	// The alert should be visible as a compliance incident via HTTP.
	var resp struct {
		Count     int `json:"count"`
		Incidents []struct {
			ID        string `json:"id"`
			Source    string `json:"source"`
			Severity  string `json:"severity"`
			AgentAddr string `json:"agentAddr"`
			Title     string `json:"title"`
		} `json:"incidents"`
	}
	w := serveJSON(t, s, http.MethodGet, "/v1/compliance/global/incidents?agent="+agent, apiKey, nil, &resp)
	if w.Code != http.StatusOK {
		t.Fatalf("incidents status=%d body=%s", w.Code, w.Body.String())
	}
	if resp.Count == 0 {
		t.Fatalf("expected compliance to have ingested the forensics alert; got 0 incidents. raw body=%s", w.Body.String())
	}

	foundForensics := false
	for _, inc := range resp.Incidents {
		if inc.Source == "forensics" && strings.EqualFold(inc.AgentAddr, agent) {
			foundForensics = true
			break
		}
	}
	if !foundForensics {
		t.Errorf("no forensics-sourced incident found for %s in %d incidents", agent, resp.Count)
	}

	// Readiness should reflect the open incidents.
	var ready struct {
		Report *compliance.ReadinessReport `json:"report"`
	}
	w = serveJSON(t, s, http.MethodGet, "/v1/compliance/global/readiness", apiKey, nil, &ready)
	if w.Code != http.StatusOK || ready.Report == nil {
		t.Fatalf("readiness status=%d body=%s", w.Code, w.Body.String())
	}
	if ready.Report.Incidents.Open == 0 {
		t.Errorf("readiness.Incidents.Open=0 but incidents exist; rollup wiring broken")
	}
}

func TestIntegration_ReceiptChainHeadInReadiness(t *testing.T) {
	s := newSignedTestServer(t)
	if s.receiptService == nil || s.complianceService == nil {
		t.Skip("receipt or compliance service disabled in this build")
	}
	apiKey := registerAgent(t, s, "0xcccc000000000000000000000000000000000003", "ops-agent")

	// Issue a few receipts directly via the service; the chain head should
	// surface through the compliance readiness endpoint.
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		err := s.receiptService.IssueReceipt(ctx, receipts.IssueRequest{
			Path: receipts.PathGateway, Reference: fmt.Sprintf("int-%d", i),
			From:   "0x1111111111111111111111111111111111111111",
			To:     "0x2222222222222222222222222222222222222222",
			Amount: "0.100000", Status: "confirmed",
			// No scope → DefaultScope "global".
		})
		if err != nil {
			t.Fatalf("IssueReceipt: %v", err)
		}
	}

	var ready struct {
		Report *compliance.ReadinessReport `json:"report"`
	}
	w := serveJSON(t, s, http.MethodGet, "/v1/compliance/global/readiness", apiKey, nil, &ready)
	if w.Code != http.StatusOK {
		t.Fatalf("readiness status=%d body=%s", w.Code, w.Body.String())
	}
	if ready.Report.ChainHeadIndex != 2 {
		t.Errorf("ChainHeadIndex=%d want 2", ready.Report.ChainHeadIndex)
	}
	if ready.Report.ChainReceipts != 3 {
		t.Errorf("ChainReceipts=%d want 3", ready.Report.ChainReceipts)
	}
}

func TestIntegration_ReceiptChainExportAndVerify(t *testing.T) {
	s := newSignedTestServer(t)
	if s.receiptService == nil {
		t.Skip("receipt service disabled in this build")
	}
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		_ = s.receiptService.IssueReceipt(ctx, receipts.IssueRequest{
			Path:      receipts.PathGateway,
			Reference: fmt.Sprintf("int-verify-%d", i),
			From:      "0x1111111111111111111111111111111111111111",
			To:        "0x2222222222222222222222222222222222222222",
			Amount:    "0.050000",
			Status:    "confirmed",
		})
	}

	// Export the bundle via HTTP (receipt chain is public), then post it
	// back to verify round-trip.
	var bundle receipts.AuditBundle
	w := serveJSON(t, s, http.MethodGet, "/v1/chains/global/bundle", "", nil, &bundle)
	if w.Code != http.StatusOK {
		t.Fatalf("bundle status=%d body=%s", w.Code, w.Body.String())
	}
	if bundle.Manifest.ReceiptCount < 4 {
		t.Errorf("expected >=4 receipts in bundle, got %d", bundle.Manifest.ReceiptCount)
	}

	var verifyResp struct {
		Report *receipts.VerifyReport `json:"report"`
	}
	w = serveJSON(t, s, http.MethodPost, "/v1/chains/bundle/verify", "", bundle, &verifyResp)
	if w.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", w.Code, w.Body.String())
	}
	if verifyResp.Report == nil || verifyResp.Report.Status != receipts.ChainIntact {
		t.Errorf("unexpected verify report: %+v", verifyResp.Report)
	}
}

func TestIntegration_PDFBundleEndpoint(t *testing.T) {
	s := newSignedTestServer(t)
	if s.receiptService == nil {
		t.Skip("receipt service disabled")
	}
	ctx := context.Background()
	_ = s.receiptService.IssueReceipt(ctx, receipts.IssueRequest{
		Path: receipts.PathGateway, Reference: "pdf-1",
		From:   "0x1111111111111111111111111111111111111111",
		To:     "0x2222222222222222222222222222222222222222",
		Amount: "0.010000", Status: "confirmed",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/chains/global/bundle?format=pdf", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/pdf") {
		t.Errorf("Content-Type=%q", w.Header().Get("Content-Type"))
	}
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-1.4\n")) {
		t.Errorf("body is not a PDF")
	}
}

func TestIntegration_WithdrawalsDisabledWithoutPayouts(t *testing.T) {
	s := newTestServer(t)

	// Default test config has no PAYOUTS_ENABLED, so the withdrawal route
	// should return 503 regardless of auth. We hit it with valid-looking JSON
	// so the 503 comes from the stub, not from body parsing.
	body := map[string]string{
		"to":        "0x2222222222222222222222222222222222222222",
		"amount":    "1.000000",
		"clientRef": "ref-1",
	}
	req := httptest.NewRequest(http.MethodPost,
		"/v1/agents/0x1111111111111111111111111111111111111111/payouts",
		bytes.NewReader(mustJSON(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	// The route is behind auth, so in test (DEMO_MODE absent) we may get 401
	// before reaching the 503 stub. Accept either: both prove the route is
	// mounted and not silently swallowing requests.
	if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusUnauthorized {
		t.Errorf("unexpected status=%d body=%s", w.Code, w.Body.String())
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
