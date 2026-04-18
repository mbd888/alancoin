package receipts

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// AuditBundleToPDF renders a one-page PDF summary of an audit bundle.
// The returned bytes are a self-contained PDF-1.4 document using only
// standard Type1 fonts (Helvetica), so no font embedding is required.
//
// The PDF is intentionally minimal — it's a human-readable cover sheet
// over the same signed manifest that ships in the JSON bundle. Auditors
// who need byte-level verification use the JSON bundle and the
// /chains/bundle/verify endpoint; this PDF is the artifact a CISO
// forwards to executive reviewers.
func AuditBundleToPDF(bundle *AuditBundle) ([]byte, error) {
	if bundle == nil {
		return nil, fmt.Errorf("receipts: nil bundle")
	}
	m := bundle.Manifest

	lines := []string{
		"Alancoin Audit Bundle",
		"",
		fmt.Sprintf("Scope:          %s", m.Scope),
		fmt.Sprintf("Format:         %s", m.Format),
		fmt.Sprintf("Generated at:   %s", m.GeneratedAt.UTC().Format(time.RFC3339)),
		"",
		fmt.Sprintf("Time range:     %s -> %s",
			formatPDFTime(m.Since), formatPDFTime(m.Until)),
		fmt.Sprintf("Chain indices:  %d -> %d", m.LowerIndex, m.UpperIndex),
		fmt.Sprintf("Receipt count:  %d", m.ReceiptCount),
		"",
		fmt.Sprintf("First hash:     %s", m.FirstHash),
		fmt.Sprintf("Last hash:      %s", m.LastHash),
		fmt.Sprintf("Merkle root:    %s", m.MerkleRoot),
		"",
		fmt.Sprintf("Manifest HMAC:  %s", m.Signature),
		"",
		"Verification:",
		"  1. Recompute MerkleRoot over the receipts in the JSON bundle.",
		"  2. Check Manifest.Signature via POST /v1/chains/bundle/verify.",
		"  3. Walk each receipt's (PayloadHash, PrevHash) link for continuity.",
	}

	return buildPDF(lines)
}

// formatPDFTime renders zero-time as the sentinel "(open)" so the PDF is
// unambiguous when callers pass an open-ended window.
func formatPDFTime(t time.Time) string {
	if t.IsZero() {
		return "(open)"
	}
	return t.UTC().Format(time.RFC3339)
}

// buildPDF writes a valid single-page PDF-1.4 with the given lines drawn
// in a monospace-friendly layout using the standard Helvetica font.
//
// Structure:
//
//	1 0 obj — Catalog
//	2 0 obj — Pages
//	3 0 obj — Page
//	4 0 obj — Content stream
//	5 0 obj — Font
//
// Byte offsets are recorded as objects are written so the xref table
// points at the right spot. The PDF parser doesn't care about whitespace
// so we use \n throughout.
func buildPDF(lines []string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	// Binary comment recommended by the spec so the file is identified as binary.
	buf.WriteString("%\xe2\xe3\xcf\xd3\n")

	offsets := make(map[int]int, 5) // object number → offset

	writeObj := func(n int, body string) {
		offsets[n] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
	}

	// 1: Catalog
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")

	// 2: Pages
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")

	// 3: Page — letter size (612 x 792 points)
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
		"/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")

	// 4: Content stream
	content := buildContentStream(lines)
	streamBody := fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
	writeObj(4, streamBody)

	// 5: Font — standard Type1, no embedding required.
	writeObj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	// xref
	xrefOffset := buf.Len()
	buf.WriteString("xref\n")
	buf.WriteString("0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}

	// trailer
	buf.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOffset)
	buf.WriteString("%%EOF\n")

	return buf.Bytes(), nil
}

// buildContentStream returns a PDF content stream that draws the given
// lines as 11-point Helvetica starting at the top of the page.
//
// Layout: one line per string, 14-point leading, 72-point left margin,
// starts at y=740 so the first line sits near the top.
func buildContentStream(lines []string) string {
	var sb strings.Builder
	sb.WriteString("BT\n")
	sb.WriteString("/F1 11 Tf\n")
	sb.WriteString("14 TL\n")
	sb.WriteString("72 740 Td\n")
	for i, line := range lines {
		if i > 0 {
			sb.WriteString("T*\n")
		}
		sb.WriteString("(")
		sb.WriteString(escapePDFString(line))
		sb.WriteString(") Tj\n")
	}
	sb.WriteString("ET")
	return sb.String()
}

// escapePDFString escapes the three characters PDF string literals care
// about. Everything else in ASCII is safe. We do not attempt to handle
// non-ASCII input; audit artifacts are always ASCII hex / known labels.
func escapePDFString(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '(':
			sb.WriteString(`\(`)
		case ')':
			sb.WriteString(`\)`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
