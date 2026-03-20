package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// CostCenter represents an internal billing unit.
type CostCenter struct {
	ID            string `json:"id"`
	TenantID      string `json:"tenantId"`
	Name          string `json:"name"`
	Department    string `json:"department"`
	ProjectCode   string `json:"projectCode,omitempty"`
	MonthlyBudget string `json:"monthlyBudget"`
	WarnAtPercent int    `json:"warnAtPercent"`
	Active        bool   `json:"active"`
	CreatedAt     string `json:"createdAt"`
}

// SpendEntry records a cost attribution event.
type SpendEntry struct {
	ID           string `json:"id"`
	CostCenterID string `json:"costCenterId"`
	AgentAddr    string `json:"agentAddr"`
	Amount       string `json:"amount"`
	ServiceType  string `json:"serviceType"`
	WorkflowID   string `json:"workflowId,omitempty"`
	SessionID    string `json:"sessionId,omitempty"`
	Description  string `json:"description,omitempty"`
	Timestamp    string `json:"timestamp"`
}

// ChargebackReport is a tenant-level monthly cost report.
type ChargebackReport struct {
	TenantID        string          `json:"tenantId"`
	Period          string          `json:"period"`
	GeneratedAt     string          `json:"generatedAt"`
	TotalSpend      string          `json:"totalSpend"`
	CostCenterCount int             `json:"costCenterCount"`
	Summaries       []PeriodSummary `json:"summaries"`
}

// PeriodSummary is a cost center's spend for a period.
type PeriodSummary struct {
	CostCenterID   string  `json:"costCenterId"`
	CostCenterName string  `json:"costCenterName"`
	Department     string  `json:"department"`
	Period         string  `json:"period"`
	TotalSpend     string  `json:"totalSpend"`
	TxCount        int     `json:"txCount"`
	TopService     string  `json:"topService"`
	BudgetUsedPct  float64 `json:"budgetUsedPct"`
}

// ChargebackCreateCostCenter creates a new cost center.
func (c *Client) ChargebackCreateCostCenter(ctx context.Context, name, department, monthlyBudget string) (*CostCenter, error) {
	var out struct {
		CostCenter CostCenter `json:"costCenter"`
	}
	body := map[string]interface{}{
		"name":          name,
		"department":    department,
		"monthlyBudget": monthlyBudget,
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/chargeback/cost-centers", body, &out); err != nil {
		return nil, err
	}
	return &out.CostCenter, nil
}

// ChargebackListCostCenters lists cost centers for the caller's tenant.
func (c *Client) ChargebackListCostCenters(ctx context.Context) ([]CostCenter, error) {
	var out struct {
		CostCenters []CostCenter `json:"costCenters"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/chargeback/cost-centers", nil, &out); err != nil {
		return nil, err
	}
	return out.CostCenters, nil
}

// ChargebackRecordSpend records a spend event against a cost center.
func (c *Client) ChargebackRecordSpend(ctx context.Context, costCenterID, agentAddr, amount, serviceType string) (*SpendEntry, error) {
	var out struct {
		Entry SpendEntry `json:"entry"`
	}
	body := map[string]string{
		"costCenterId": costCenterID,
		"agentAddr":    agentAddr,
		"amount":       amount,
		"serviceType":  serviceType,
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/chargeback/spend", body, &out); err != nil {
		return nil, err
	}
	return &out.Entry, nil
}

// ChargebackGenerateReport generates a monthly chargeback report.
func (c *Client) ChargebackGenerateReport(ctx context.Context, year, month int) (*ChargebackReport, error) {
	var out struct {
		Report ChargebackReport `json:"report"`
	}
	path := "/v1/chargeback/reports" + buildQuery("year", fmt.Sprintf("%d", year), "month", fmt.Sprintf("%d", month))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Report, nil
}
