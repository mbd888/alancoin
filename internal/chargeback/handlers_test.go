package chargeback

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupChargebackRouter() (*gin.Engine, *Service) {
	svc := NewService(NewMemoryStore(), slog.Default())

	r := gin.New()
	h := NewHandler(svc)

	authed := r.Group("/v1", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xCaller")
		c.Set("authTenantID", "ten_1")
		c.Next()
	})
	h.RegisterRoutes(authed)
	h.RegisterProtectedRoutes(authed)

	return r, svc
}

func TestHandlerCreateCostCenter(t *testing.T) {
	r, _ := setupChargebackRouter()

	body, _ := json.Marshal(map[string]interface{}{
		"name":          "Claims",
		"department":    "Insurance",
		"monthlyBudget": "10000.00",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chargeback/cost-centers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		CostCenter CostCenter `json:"costCenter"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.CostCenter.Name != "Claims" {
		t.Errorf("name = %q", resp.CostCenter.Name)
	}
	if resp.CostCenter.TenantID != "ten_1" {
		t.Errorf("tenantID = %q, want ten_1 (should use caller's tenant)", resp.CostCenter.TenantID)
	}
}

func TestHandlerRecordSpend(t *testing.T) {
	r, svc := setupChargebackRouter()

	cc, _ := svc.CreateCostCenter(context.Background(), "ten_1", "Eng", "Engineering", "", "5000.00", 80)

	body, _ := json.Marshal(map[string]interface{}{
		"costCenterId": cc.ID,
		"agentAddr":    "0xAgent1",
		"amount":       "25.00",
		"serviceType":  "inference",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chargeback/spend", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerBudgetExceeded(t *testing.T) {
	r, svc := setupChargebackRouter()

	cc, _ := svc.CreateCostCenter(context.Background(), "ten_1", "Small", "R&D", "", "10.00", 80)

	// Spend the whole budget
	svc.RecordSpend(context.Background(), cc.ID, "ten_1", "0xA", "10.00", "inference", SpendOpts{})

	// Try to spend more
	body, _ := json.Marshal(map[string]interface{}{
		"costCenterId": cc.ID,
		"agentAddr":    "0xA",
		"amount":       "1.00",
		"serviceType":  "inference",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chargeback/spend", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (budget exceeded). body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerListCostCenters(t *testing.T) {
	r, svc := setupChargebackRouter()

	svc.CreateCostCenter(context.Background(), "ten_1", "A", "Dept", "", "1000.00", 80)
	svc.CreateCostCenter(context.Background(), "ten_1", "B", "Dept", "", "2000.00", 80)
	svc.CreateCostCenter(context.Background(), "ten_2", "C", "Dept", "", "3000.00", 80) // different tenant

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chargeback/cost-centers", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		CostCenters []CostCenter `json:"costCenters"`
		Count       int          `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2 (tenant isolation)", resp.Count)
	}
}

func TestHandlerGenerateReport(t *testing.T) {
	r, svc := setupChargebackRouter()

	cc, _ := svc.CreateCostCenter(context.Background(), "ten_1", "Claims", "Insurance", "", "10000.00", 80)
	svc.RecordSpend(context.Background(), cc.ID, "ten_1", "0xA", "150.00", "inference", SpendOpts{})
	svc.RecordSpend(context.Background(), cc.ID, "ten_1", "0xA", "75.00", "translation", SpendOpts{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chargeback/reports", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Report ChargebackReport `json:"report"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Report.CostCenterCount != 1 {
		t.Errorf("costCenterCount = %d, want 1", resp.Report.CostCenterCount)
	}
}

func TestHandlerGetCostCenterTenantIsolation(t *testing.T) {
	r, svc := setupChargebackRouter()

	// Create cost center for different tenant
	cc, _ := svc.CreateCostCenter(context.Background(), "ten_2", "Other", "Dept", "", "1000.00", 80)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chargeback/cost-centers/"+cc.ID, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (tenant isolation)", w.Code)
	}
}

func TestHandlerUpdateCostCenter(t *testing.T) {
	r, svc := setupChargebackRouter()

	cc, _ := svc.CreateCostCenter(context.Background(), "ten_1", "Claims", "Insurance", "", "10000.00", 80)

	newName := "Claims v2"
	newBudget := "20000.00"
	newWarn := 90
	active := false
	body, _ := json.Marshal(map[string]interface{}{
		"name":          newName,
		"monthlyBudget": newBudget,
		"warnAtPercent": newWarn,
		"active":        active,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/v1/chargeback/cost-centers/"+cc.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		CostCenter CostCenter `json:"costCenter"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.CostCenter.Name != newName {
		t.Errorf("name = %q, want %q", resp.CostCenter.Name, newName)
	}
	if resp.CostCenter.MonthlyBudget != newBudget {
		t.Errorf("monthlyBudget = %q, want %q", resp.CostCenter.MonthlyBudget, newBudget)
	}
	if resp.CostCenter.WarnAtPercent != newWarn {
		t.Errorf("warnAtPercent = %d, want %d", resp.CostCenter.WarnAtPercent, newWarn)
	}
	if resp.CostCenter.Active {
		t.Error("active should be false after update")
	}
}

func TestHandlerUpdateCostCenterNotFound(t *testing.T) {
	r, _ := setupChargebackRouter()

	body, _ := json.Marshal(map[string]interface{}{
		"name": "Doesn't matter",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/v1/chargeback/cost-centers/cc_nonexistent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerUpdateCostCenterBadRequest(t *testing.T) {
	r, svc := setupChargebackRouter()

	cc, _ := svc.CreateCostCenter(context.Background(), "ten_1", "Claims", "Insurance", "", "10000.00", 80)

	// Send invalid JSON body
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/v1/chargeback/cost-centers/"+cc.ID, bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerUpdateCostCenterTenantIsolation(t *testing.T) {
	r, svc := setupChargebackRouter()

	// Create cost center for a different tenant
	cc, _ := svc.CreateCostCenter(context.Background(), "ten_2", "Other", "Dept", "", "1000.00", 80)

	body, _ := json.Marshal(map[string]interface{}{
		"name": "Hijacked",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/v1/chargeback/cost-centers/"+cc.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (tenant isolation). body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerListSpend(t *testing.T) {
	r, svc := setupChargebackRouter()

	cc, _ := svc.CreateCostCenter(context.Background(), "ten_1", "Claims", "Insurance", "", "10000.00", 80)
	svc.RecordSpend(context.Background(), cc.ID, "ten_1", "0xA1", "100.00", "inference", SpendOpts{})
	svc.RecordSpend(context.Background(), cc.ID, "ten_1", "0xA2", "50.00", "translation", SpendOpts{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chargeback/cost-centers/"+cc.ID+"/spend", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Entries []SpendEntry `json:"entries"`
		Count   int          `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2", resp.Count)
	}
}

func TestHandlerListSpendWithDateFilter(t *testing.T) {
	r, svc := setupChargebackRouter()

	cc, _ := svc.CreateCostCenter(context.Background(), "ten_1", "Claims", "Insurance", "", "10000.00", 80)
	svc.RecordSpend(context.Background(), cc.ID, "ten_1", "0xA1", "100.00", "inference", SpendOpts{})

	// Use a future date range that excludes current spend
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chargeback/cost-centers/"+cc.ID+"/spend?from=2099-01-01T00:00:00Z&to=2099-02-01T00:00:00Z", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Entries []SpendEntry `json:"entries"`
		Count   int          `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 0 {
		t.Errorf("count = %d, want 0 (date range excludes all entries)", resp.Count)
	}
}

func TestHandlerListSpendNotFound(t *testing.T) {
	r, _ := setupChargebackRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chargeback/cost-centers/cc_nonexistent/spend", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404. body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerListSpendTenantIsolation(t *testing.T) {
	r, svc := setupChargebackRouter()

	// Create cost center for a different tenant
	cc, _ := svc.CreateCostCenter(context.Background(), "ten_2", "Other", "Dept", "", "1000.00", 80)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chargeback/cost-centers/"+cc.ID+"/spend", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (tenant isolation). body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerListSpendEmpty(t *testing.T) {
	r, svc := setupChargebackRouter()

	// Cost center with no spend entries
	cc, _ := svc.CreateCostCenter(context.Background(), "ten_1", "Empty", "Dept", "", "1000.00", 80)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/chargeback/cost-centers/"+cc.ID+"/spend", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 0 {
		t.Errorf("count = %d, want 0", resp.Count)
	}
}
