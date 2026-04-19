package receipts

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuditBundleToPDF_ShapeAndContents(t *testing.T) {
	svc := newTestService()
	for i := 0; i < 3; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("r%d", i))
	}
	bundle, err := svc.ExportBundle(context.Background(), "tenant_a", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}

	pdf, err := AuditBundleToPDF(bundle)
	if err != nil {
		t.Fatalf("AuditBundleToPDF: %v", err)
	}

	// Header + EOF markers that every PDF reader sniffs.
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) {
		t.Fatalf("missing PDF header, first 32 bytes: %q", pdf[:32])
	}
	if !bytes.Contains(pdf, []byte("%%EOF")) {
		t.Fatal("missing end-of-file terminator")
	}

	// Required structural objects must all be present.
	for _, needle := range []string{
		"/Type /Catalog",
		"/Type /Pages",
		"/Type /Page",
		"/Type /Font",
		"/BaseFont /Helvetica",
		"xref",
		"trailer",
		"startxref",
	} {
		if !bytes.Contains(pdf, []byte(needle)) {
			t.Errorf("PDF missing %q", needle)
		}
	}

	// Manifest values must be rendered into the content stream.
	for _, needle := range []string{
		"Alancoin Audit Bundle",
		bundle.Manifest.Scope,
		bundle.Manifest.MerkleRoot,
	} {
		if !bytes.Contains(pdf, []byte(needle)) {
			t.Errorf("PDF missing manifest value %q", needle)
		}
	}
}

func TestAuditBundleToPDF_EscapesParensAndBackslashes(t *testing.T) {
	// Construct a bundle with values that need escaping in a PDF literal.
	bundle := &AuditBundle{
		Format: BundleFormat,
		Manifest: BundleManifest{
			Scope:       `a(b)c\d`,
			Format:      BundleFormat,
			Signature:   "sig",
			MerkleRoot:  "root",
			GeneratedAt: time.Unix(0, 0).UTC(),
		},
	}
	pdf, err := AuditBundleToPDF(bundle)
	if err != nil {
		t.Fatalf("AuditBundleToPDF: %v", err)
	}
	// The escaped string `a\(b\)c\\d` must appear in the content stream.
	if !bytes.Contains(pdf, []byte(`a\(b\)c\\d`)) {
		t.Errorf("PDF did not escape reserved chars; looked for a\\(b\\)c\\\\d")
	}
	// The raw (unescaped) form must NOT appear as-is inside a text literal,
	// otherwise Acrobat would trip on the unmatched '(' / ')' pair.
	if bytes.Contains(pdf, []byte(`a(b)c\d`)) {
		t.Error("PDF contains unescaped reserved chars in a string literal")
	}
}

func TestExportBundle_PDFEndpoint(t *testing.T) {
	r, svc := setupChainRouter(t)
	for i := 0; i < 2; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("p%d", i))
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chains/tenant_a/bundle?format=pdf", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/pdf") {
		t.Errorf("Content-Type=%q want application/pdf", ct)
	}
	disp := w.Header().Get("Content-Disposition")
	if !strings.Contains(disp, "attachment") || !strings.HasSuffix(strings.Split(disp, `"`)[1], ".pdf") {
		t.Errorf("Content-Disposition=%q", disp)
	}
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-1.4\n")) {
		t.Errorf("body not a PDF, first 16 bytes: %q", w.Body.Bytes()[:16])
	}
}

func TestExportBundle_JSONStillDefault(t *testing.T) {
	r, svc := setupChainRouter(t)
	for i := 0; i < 2; i++ {
		issue(t, svc, "tenant_a", fmt.Sprintf("j%d", i))
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chains/tenant_a/bundle", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type=%q want json", ct)
	}
}
