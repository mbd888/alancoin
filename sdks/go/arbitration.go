package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// ArbitrationCase represents a dispute arbitration case.
type ArbitrationCase struct {
	ID             string  `json:"id"`
	EscrowID       string  `json:"escrowId"`
	BuyerAddr      string  `json:"buyerAddr"`
	SellerAddr     string  `json:"sellerAddr"`
	DisputedAmount string  `json:"disputedAmount"`
	Reason         string  `json:"reason"`
	Status         string  `json:"status"`
	ArbiterAddr    string  `json:"arbiterAddr,omitempty"`
	Outcome        string  `json:"outcome,omitempty"`
	SplitPct       int     `json:"splitPct,omitempty"`
	Decision       string  `json:"decision,omitempty"`
	Fee            string  `json:"fee"`
	AutoResolvable bool    `json:"autoResolvable"`
	FiledAt        string  `json:"filedAt"`
	ResolvedAt     *string `json:"resolvedAt,omitempty"`
}

// ArbitrationFileCase files a new arbitration case.
func (c *Client) ArbitrationFileCase(ctx context.Context, escrowID, buyerAddr, sellerAddr, amount, reason string, contractID string) (*ArbitrationCase, error) {
	var out struct {
		Case ArbitrationCase `json:"case"`
	}
	body := map[string]string{
		"escrowId":   escrowID,
		"buyerAddr":  buyerAddr,
		"sellerAddr": sellerAddr,
		"amount":     amount,
		"reason":     reason,
	}
	if contractID != "" {
		body["contractId"] = contractID
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/arbitration/cases", body, &out); err != nil {
		return nil, err
	}
	return &out.Case, nil
}

// ArbitrationGetCase retrieves an arbitration case by ID.
func (c *Client) ArbitrationGetCase(ctx context.Context, caseID string) (*ArbitrationCase, error) {
	var out struct {
		Case ArbitrationCase `json:"case"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/arbitration/cases/"+caseID, nil, &out); err != nil {
		return nil, err
	}
	return &out.Case, nil
}

// ArbitrationAutoResolve attempts auto-resolution via behavioral contract.
func (c *Client) ArbitrationAutoResolve(ctx context.Context, caseID string, contractPassed bool) (bool, error) {
	var out struct {
		Resolved bool `json:"resolved"`
	}
	body := map[string]bool{"contractPassed": contractPassed}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/arbitration/cases/"+caseID+"/auto-resolve", body, &out); err != nil {
		return false, err
	}
	return out.Resolved, nil
}

// ArbitrationSubmitEvidence adds evidence to a case.
func (c *Client) ArbitrationSubmitEvidence(ctx context.Context, caseID, submittedBy, role, evidenceType, content string) error {
	body := map[string]string{
		"submittedBy": submittedBy,
		"role":        role,
		"type":        evidenceType,
		"content":     content,
	}
	return c.doJSON(ctx, http.MethodPost, "/v1/arbitration/cases/"+caseID+"/evidence", body, nil)
}

// ArbitrationListOpen lists open arbitration cases.
func (c *Client) ArbitrationListOpen(ctx context.Context, limit int) ([]ArbitrationCase, error) {
	var out struct {
		Cases []ArbitrationCase `json:"cases"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/arbitration/cases?limit="+itoa(limit), nil, &out); err != nil {
		return nil, err
	}
	return out.Cases, nil
}

func itoa(i int) string {
	if i <= 0 {
		return "50"
	}
	return fmt.Sprintf("%d", i)
}
