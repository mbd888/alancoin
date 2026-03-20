package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// KYACertificate represents a KYA identity certificate.
type KYACertificate struct {
	ID          string         `json:"id"`
	AgentAddr   string         `json:"agentAddr"`
	DID         string         `json:"did"`
	Org         KYAOrgBinding  `json:"org"`
	Permissions KYAPermissions `json:"permissions"`
	Reputation  KYAReputation  `json:"reputation"`
	Status      string         `json:"status"`
	Signature   string         `json:"signature"`
	IssuedAt    string         `json:"issuedAt"`
	ExpiresAt   string         `json:"expiresAt"`
	RevokedAt   *string        `json:"revokedAt,omitempty"`
}

// KYAOrgBinding is the organizational binding in a KYA certificate.
type KYAOrgBinding struct {
	TenantID     string `json:"tenantId"`
	OrgName      string `json:"orgName"`
	Department   string `json:"department,omitempty"`
	AuthorizedBy string `json:"authorizedBy"`
	AuthMethod   string `json:"authMethod"`
}

// KYAPermissions defines what the agent is authorized to do.
type KYAPermissions struct {
	MaxSpendPerTx  string   `json:"maxSpendPerTx,omitempty"`
	MaxSpendPerDay string   `json:"maxSpendPerDay,omitempty"`
	TotalBudget    string   `json:"totalBudget,omitempty"`
	AllowedAPIs    []string `json:"allowedApis,omitempty"`
}

// KYAReputation is the reputation snapshot in a KYA certificate.
type KYAReputation struct {
	TraceRankScore float64 `json:"traceRankScore"`
	TrustTier      string  `json:"trustTier"`
	DisputeRate    float64 `json:"disputeRate"`
	SuccessRate    float64 `json:"successRate"`
	TxCount        int     `json:"txCount"`
	AccountAgeDays int     `json:"accountAgeDays"`
}

// KYAIssueRequest is the request body for issuing a KYA certificate.
type KYAIssueRequest struct {
	AgentAddr   string         `json:"agentAddr"`
	Org         KYAOrgBinding  `json:"org"`
	Permissions KYAPermissions `json:"permissions"`
	ValidDays   int            `json:"validDays"`
}

// KYAComplianceReport is the EU AI Act Article 12 compliance export.
type KYAComplianceReport struct {
	CertificateID  string         `json:"certificateId"`
	AgentDID       string         `json:"agentDid"`
	Org            KYAOrgBinding  `json:"org"`
	Permissions    KYAPermissions `json:"permissions"`
	Reputation     KYAReputation  `json:"reputation"`
	Status         string         `json:"status"`
	SignatureValid bool           `json:"signatureValid"`
	Standard       string         `json:"standard"`
}

// KYAIssueCertificate issues a new KYA identity certificate.
func (c *Client) KYAIssueCertificate(ctx context.Context, req KYAIssueRequest) (*KYACertificate, error) {
	var out struct {
		Certificate KYACertificate `json:"certificate"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/kya/certificates", req, &out); err != nil {
		return nil, err
	}
	return &out.Certificate, nil
}

// KYAGetCertificate retrieves a KYA certificate by ID.
func (c *Client) KYAGetCertificate(ctx context.Context, certID string) (*KYACertificate, error) {
	var out struct {
		Certificate KYACertificate `json:"certificate"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/kya/certificates/"+certID, nil, &out); err != nil {
		return nil, err
	}
	return &out.Certificate, nil
}

// KYAVerifyCertificate verifies a certificate's validity and signature.
func (c *Client) KYAVerifyCertificate(ctx context.Context, certID string) (bool, *KYACertificate, error) {
	var out struct {
		Valid       bool           `json:"valid"`
		Certificate KYACertificate `json:"certificate"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/kya/certificates/"+certID+"/verify", nil, &out); err != nil {
		return false, nil, err
	}
	return out.Valid, &out.Certificate, nil
}

// KYARevokeCertificate revokes a certificate.
func (c *Client) KYARevokeCertificate(ctx context.Context, certID, reason string) error {
	body := map[string]string{"reason": reason}
	return c.doJSON(ctx, http.MethodPost, "/v1/kya/certificates/"+certID+"/revoke", body, nil)
}

// KYAGetByAgent retrieves the active certificate for an agent.
func (c *Client) KYAGetByAgent(ctx context.Context, agentAddr string) (*KYACertificate, error) {
	var out struct {
		Certificate KYACertificate `json:"certificate"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/kya/agents/"+agentAddr, nil, &out); err != nil {
		return nil, err
	}
	return &out.Certificate, nil
}

// KYAComplianceExport generates an EU AI Act Article 12 compliance report.
func (c *Client) KYAComplianceExport(ctx context.Context, certID string) (*KYAComplianceReport, error) {
	var out struct {
		Report KYAComplianceReport `json:"report"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/kya/certificates/"+certID+"/compliance", nil, &out); err != nil {
		return nil, err
	}
	return &out.Report, nil
}

// KYAListByTenant lists certificates for a tenant.
func (c *Client) KYAListByTenant(ctx context.Context, tenantID string, limit int) ([]KYACertificate, error) {
	path := fmt.Sprintf("/v1/kya/tenants/%s/certificates", tenantID) + buildQuery("limit", fmt.Sprintf("%d", limit))
	var out struct {
		Certificates []KYACertificate `json:"certificates"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Certificates, nil
}
