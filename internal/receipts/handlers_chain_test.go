package receipts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupChainRouter(t *testing.T) (*gin.Engine, *Service) {
	t.Helper()
	svc := newTestService()
	r := gin.New()
	h := NewHandler(svc)
	h.RegisterRoutes(r.Group("/v1"))
	return r, svc
}

func TestHandlerGetChainHead_Empty(t *testing.T) {
	r, _ := setupChainRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chains/tenant_a/head", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Head *ChainHead `json:"head"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Head == nil {
		t.Fatal("expected head in response")
	}
	if resp.Head.HeadIndex != -1 || resp.Head.HeadHash != "" {
		t.Errorf("empty chain head wrong: index=%d hash=%q", resp.Head.HeadIndex, resp.Head.HeadHash)
	}
}

func TestHandlerGetChainHead_Populated(t *testing.T) {
	r, svc := setupChainRouter(t)
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chains/tenant_a/head", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Head *ChainHead `json:"head"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Head.HeadIndex != 2 {
		t.Errorf("expected headIndex=2, got %d", resp.Head.HeadIndex)
	}
	if resp.Head.HeadHash == "" {
		t.Error("expected non-empty headHash")
	}
}

func TestHandlerVerifyChain_Intact(t *testing.T) {
	r, svc := setupChainRouter(t)
	for i := 0; i < 4; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chains/tenant_a/verify", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Report *VerifyReport `json:"report"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Report.Status != ChainIntact {
		t.Errorf("expected ChainIntact, got %s", resp.Report.Status)
	}
}

func TestHandlerVerifyChain_BrokenReturns409(t *testing.T) {
	r, svc := setupChainRouter(t)
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}

	// Tamper in-place via the concrete store
	store := svc.store.(*MemoryStore)
	store.mu.Lock()
	for _, rc := range store.receipts {
		if rc.ChainIndex == 1 {
			rc.Amount = "999.000000"
		}
	}
	store.mu.Unlock()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chains/tenant_a/verify", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for broken chain, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerExportBundle_RoundTrip(t *testing.T) {
	r, svc := setupChainRouter(t)
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chains/tenant_a/bundle", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
	var bundle AuditBundle
	if err := json.Unmarshal(w.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if bundle.Manifest.ReceiptCount != 3 {
		t.Errorf("expected 3 receipts, got %d", bundle.Manifest.ReceiptCount)
	}

	// Round-trip through the verify endpoint.
	body, _ := json.Marshal(bundle)
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/chains/bundle/verify", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("verify status=%d, body=%s", w2.Code, w2.Body.String())
	}
}

func TestHandlerVerifyBundle_DetectsReceiptTamper(t *testing.T) {
	r, svc := setupChainRouter(t)
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chains/tenant_a/bundle", nil)
	r.ServeHTTP(w, req)

	var bundle AuditBundle
	_ = json.Unmarshal(w.Body.Bytes(), &bundle)

	bundle.Receipts[1].Amount = "123.456789"

	body, _ := json.Marshal(bundle)
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/chains/bundle/verify", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("expected 409 for tampered bundle, got %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestHandlerExportBundle_BadTimeFormat(t *testing.T) {
	r, _ := setupChainRouter(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chains/tenant_a/bundle?since=not-a-time", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
