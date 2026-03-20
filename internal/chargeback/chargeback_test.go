package chargeback

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func newTestService() *Service {
	return NewService(NewMemoryStore(), slog.Default())
}

func TestCreateCostCenter(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	cc, err := svc.CreateCostCenter(ctx, "ten_1", "Claims", "Insurance Ops", "PROJ-001", "10000.00", 80)
	if err != nil {
		t.Fatalf("CreateCostCenter: %v", err)
	}
	if cc.ID == "" {
		t.Fatal("ID empty")
	}
	if cc.Name != "Claims" {
		t.Errorf("Name = %q, want Claims", cc.Name)
	}
	if cc.MonthlyBudget != "10000.00" {
		t.Errorf("Budget = %q, want 10000.00", cc.MonthlyBudget)
	}
}

func TestRecordSpend(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	cc, _ := svc.CreateCostCenter(ctx, "ten_1", "Engineering", "Engineering", "", "5000.00", 80)

	entry, err := svc.RecordSpend(ctx, cc.ID, "ten_1", "0xAgent1", "25.50", "inference", SpendOpts{
		Description: "GPT-4 summarization",
	})
	if err != nil {
		t.Fatalf("RecordSpend: %v", err)
	}
	if entry.Amount != "25.50" {
		t.Errorf("Amount = %q, want 25.50", entry.Amount)
	}
	if entry.ServiceType != "inference" {
		t.Errorf("ServiceType = %q, want inference", entry.ServiceType)
	}
}

func TestBudgetEnforcement(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	cc, _ := svc.CreateCostCenter(ctx, "ten_1", "Small Team", "R&D", "", "100.00", 80)

	// Spend 90 — should succeed
	_, err := svc.RecordSpend(ctx, cc.ID, "ten_1", "0xA1", "90.00", "inference", SpendOpts{})
	if err != nil {
		t.Fatalf("first spend: %v", err)
	}

	// Spend 20 more — should fail (total would be 110 > 100)
	_, err = svc.RecordSpend(ctx, cc.ID, "ten_1", "0xA1", "20.00", "inference", SpendOpts{})
	if err != ErrBudgetExceeded {
		t.Errorf("second spend err = %v, want ErrBudgetExceeded", err)
	}

	// Spend 10 — should succeed (total would be 100 = 100)
	_, err = svc.RecordSpend(ctx, cc.ID, "ten_1", "0xA1", "10.00", "inference", SpendOpts{})
	if err != nil {
		t.Fatalf("third spend: %v", err)
	}
}

func TestGenerateReport(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	cc1, _ := svc.CreateCostCenter(ctx, "ten_1", "Claims", "Insurance", "", "10000.00", 80)
	cc2, _ := svc.CreateCostCenter(ctx, "ten_1", "Underwriting", "Insurance", "", "8000.00", 80)

	svc.RecordSpend(ctx, cc1.ID, "ten_1", "0xA1", "150.00", "inference", SpendOpts{})
	svc.RecordSpend(ctx, cc1.ID, "ten_1", "0xA2", "75.00", "translation", SpendOpts{})
	svc.RecordSpend(ctx, cc2.ID, "ten_1", "0xA3", "200.00", "inference", SpendOpts{})

	now := time.Now()
	report, err := svc.GenerateReport(ctx, "ten_1", now.Year(), now.Month())
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}

	if report.CostCenterCount != 2 {
		t.Errorf("CostCenterCount = %d, want 2", report.CostCenterCount)
	}
	if report.TotalSpend != "425.000000" {
		t.Errorf("TotalSpend = %q, want 425.000000", report.TotalSpend)
	}
	if len(report.Summaries) != 2 {
		t.Errorf("Summaries len = %d, want 2", len(report.Summaries))
	}

	// Check Claims summary
	for _, s := range report.Summaries {
		if s.CostCenterName == "Claims" {
			if s.TotalSpend != "225.000000" {
				t.Errorf("Claims spend = %q, want 225.000000", s.TotalSpend)
			}
			if s.TxCount != 2 {
				t.Errorf("Claims txCount = %d, want 2", s.TxCount)
			}
			if s.TopService != "inference" {
				t.Errorf("Claims topService = %q, want inference", s.TopService)
			}
		}
	}
}

func TestMultipleTenants(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	svc.CreateCostCenter(ctx, "ten_1", "Team A", "Eng", "", "5000.00", 80)
	svc.CreateCostCenter(ctx, "ten_2", "Team B", "Eng", "", "5000.00", 80)

	now := time.Now()
	report1, _ := svc.GenerateReport(ctx, "ten_1", now.Year(), now.Month())
	report2, _ := svc.GenerateReport(ctx, "ten_2", now.Year(), now.Month())

	if report1.CostCenterCount != 1 {
		t.Errorf("ten_1 centers = %d, want 1", report1.CostCenterCount)
	}
	if report2.CostCenterCount != 1 {
		t.Errorf("ten_2 centers = %d, want 1", report2.CostCenterCount)
	}
}

func TestSpendWithMetadata(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	cc, _ := svc.CreateCostCenter(ctx, "ten_1", "Pipeline", "Ops", "", "50000.00", 80)

	entry, _ := svc.RecordSpend(ctx, cc.ID, "ten_1", "0xA1", "10.00", "inference", SpendOpts{
		WorkflowID:  "wf_123",
		SessionID:   "sess_456",
		Description: "Claims doc summarization",
	})

	if entry.WorkflowID != "wf_123" {
		t.Errorf("WorkflowID = %q", entry.WorkflowID)
	}
	if entry.SessionID != "sess_456" {
		t.Errorf("SessionID = %q", entry.SessionID)
	}
}
