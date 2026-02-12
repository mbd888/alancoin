package escrow

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupTemplateRouter() (*gin.Engine, *TemplateService) {
	gin.SetMode(gin.TestMode)

	ts := NewTemplateMemoryStore()
	es := NewMemoryStore()
	ml := newMockLedger()
	svc := NewTemplateService(ts, es, ml)
	handler := NewTemplateHandler(svc)

	r := gin.New()
	v1 := r.Group("/v1")
	handler.RegisterRoutes(v1)

	// Mock auth middleware
	authGroup := v1.Group("")
	authGroup.Use(func(c *gin.Context) {
		if addr := c.GetHeader("X-Agent-Address"); addr != "" {
			c.Set("authAgentAddr", addr)
		}
		c.Next()
	})
	handler.RegisterProtectedRoutes(authGroup)

	return r, svc
}

func TestTemplateHandler_CreateTemplate(t *testing.T) {
	router, _ := setupTemplateRouter()

	body, _ := json.Marshal(CreateTemplateRequest{
		Name:        "Web Development",
		CreatorAddr: "0xcreator",
		Milestones: []Milestone{
			{Name: "Design", Percentage: 30},
			{Name: "Development", Percentage: 70},
		},
		TotalAmount:      "100.000000",
		AutoReleaseHours: 48,
	})

	req := httptest.NewRequest("POST", "/v1/escrow/templates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xcreator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Template struct {
			ID               string      `json:"id"`
			Name             string      `json:"name"`
			Milestones       []Milestone `json:"milestones"`
			AutoReleaseHours int         `json:"autoReleaseHours"`
		} `json:"template"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Template.Name != "Web Development" {
		t.Errorf("expected name 'Web Development', got %s", resp.Template.Name)
	}
	if len(resp.Template.Milestones) != 2 {
		t.Errorf("expected 2 milestones, got %d", len(resp.Template.Milestones))
	}
	if resp.Template.AutoReleaseHours != 48 {
		t.Errorf("expected 48h auto release, got %d", resp.Template.AutoReleaseHours)
	}
}

func TestTemplateHandler_CreateTemplate_Unauthorized(t *testing.T) {
	router, _ := setupTemplateRouter()

	body, _ := json.Marshal(CreateTemplateRequest{
		Name:        "Test",
		CreatorAddr: "0xcreator",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "10.000000",
	})

	req := httptest.NewRequest("POST", "/v1/escrow/templates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xattacker") // Different from creatorAddr
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403, got %d", w.Code)
	}
}

func TestTemplateHandler_CreateTemplate_InvalidPercentages(t *testing.T) {
	router, _ := setupTemplateRouter()

	body, _ := json.Marshal(CreateTemplateRequest{
		Name:        "Bad Template",
		CreatorAddr: "0xcreator",
		Milestones: []Milestone{
			{Name: "M1", Percentage: 50},
			{Name: "M2", Percentage: 30}, // Sum = 80, not 100
		},
		TotalAmount: "10.000000",
	})

	req := httptest.NewRequest("POST", "/v1/escrow/templates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xcreator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "milestones_invalid" {
		t.Errorf("expected error 'milestones_invalid', got %s", resp["error"])
	}
}

func TestTemplateHandler_GetTemplate(t *testing.T) {
	router, svc := setupTemplateRouter()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Service Agreement",
		CreatorAddr: "0xcreator",
		Milestones:  []Milestone{{Name: "Completion", Percentage: 100}},
		TotalAmount: "50.000000",
	})

	req := httptest.NewRequest("GET", "/v1/escrow/templates/"+tmpl.ID, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Template struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"template"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Template.ID != tmpl.ID {
		t.Errorf("expected ID %s, got %s", tmpl.ID, resp.Template.ID)
	}
}

func TestTemplateHandler_GetTemplate_NotFound(t *testing.T) {
	router, _ := setupTemplateRouter()

	req := httptest.NewRequest("GET", "/v1/escrow/templates/tmpl_nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", w.Code)
	}
}

func TestTemplateHandler_ListTemplates(t *testing.T) {
	router, svc := setupTemplateRouter()

	svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Template 1",
		CreatorAddr: "0xcreator",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "10.000000",
	})
	svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Template 2",
		CreatorAddr: "0xcreator",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "20.000000",
	})

	req := httptest.NewRequest("GET", "/v1/escrow/templates?limit=50", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Templates []map[string]interface{} `json:"templates"`
		Count     int                      `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 2 {
		t.Errorf("expected 2 templates, got %d", resp.Count)
	}
}

func TestTemplateHandler_InstantiateTemplate(t *testing.T) {
	router, svc := setupTemplateRouter()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Service Agreement",
		CreatorAddr: "0xcreator",
		Milestones: []Milestone{
			{Name: "Phase 1", Percentage: 50},
			{Name: "Phase 2", Percentage: 50},
		},
		TotalAmount: "100.000000",
	})

	body, _ := json.Marshal(InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	req := httptest.NewRequest("POST", "/v1/escrow/templates/"+tmpl.ID+"/instantiate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xbuyer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Escrow     map[string]interface{}   `json:"escrow"`
		Milestones []map[string]interface{} `json:"milestones"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Escrow["amount"] != "100.000000" {
		t.Errorf("expected amount 100.000000, got %v", resp.Escrow["amount"])
	}
	if len(resp.Milestones) != 2 {
		t.Errorf("expected 2 milestones, got %d", len(resp.Milestones))
	}
}

func TestTemplateHandler_InstantiateTemplate_Unauthorized(t *testing.T) {
	router, svc := setupTemplateRouter()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Test",
		CreatorAddr: "0xcreator",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "50.000000",
	})

	body, _ := json.Marshal(InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	req := httptest.NewRequest("POST", "/v1/escrow/templates/"+tmpl.ID+"/instantiate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", "0xattacker") // Not the buyer
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403, got %d", w.Code)
	}
}

func TestTemplateHandler_ReleaseMilestone(t *testing.T) {
	router, svc := setupTemplateRouter()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Milestone Test",
		CreatorAddr: "0xcreator",
		Milestones: []Milestone{
			{Name: "Phase 1", Percentage: 40},
			{Name: "Phase 2", Percentage: 60},
		},
		TotalAmount: "100.000000",
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/milestones/0/release", nil)
	req.Header.Set("X-Agent-Address", "0xbuyer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Milestone struct {
			Released       bool   `json:"released"`
			ReleasedAmount string `json:"releasedAmount"`
		} `json:"milestone"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if !resp.Milestone.Released {
		t.Error("expected milestone to be released")
	}
	if resp.Milestone.ReleasedAmount != "40.000000" {
		t.Errorf("expected 40.000000, got %s", resp.Milestone.ReleasedAmount)
	}
}

func TestTemplateHandler_ReleaseMilestone_InvalidIndex(t *testing.T) {
	router, svc := setupTemplateRouter()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Test",
		CreatorAddr: "0xcreator",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "50.000000",
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/milestones/abc/release", nil)
	req.Header.Set("X-Agent-Address", "0xbuyer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for invalid index, got %d", w.Code)
	}
}

func TestTemplateHandler_ReleaseMilestone_Unauthorized(t *testing.T) {
	router, svc := setupTemplateRouter()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Test",
		CreatorAddr: "0xcreator",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "50.000000",
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	req := httptest.NewRequest("POST", "/v1/escrow/"+esc.ID+"/milestones/0/release", nil)
	req.Header.Set("X-Agent-Address", "0xseller") // Seller can't release
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403, got %d", w.Code)
	}
}

func TestTemplateHandler_ListMilestones(t *testing.T) {
	router, svc := setupTemplateRouter()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Multi-Milestone",
		CreatorAddr: "0xcreator",
		Milestones: []Milestone{
			{Name: "A", Percentage: 25},
			{Name: "B", Percentage: 25},
			{Name: "C", Percentage: 50},
		},
		TotalAmount: "100.000000",
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	req := httptest.NewRequest("GET", "/v1/escrow/"+esc.ID+"/milestones", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Milestones []map[string]interface{} `json:"milestones"`
		Count      int                      `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Count != 3 {
		t.Errorf("expected 3 milestones, got %d", resp.Count)
	}
}
