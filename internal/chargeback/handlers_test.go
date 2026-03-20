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
