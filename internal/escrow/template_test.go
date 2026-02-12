package escrow

import (
	"context"
	"testing"
)

func newTemplateTestService() (*TemplateService, *mockLedger) {
	ts := NewTemplateMemoryStore()
	es := NewMemoryStore()
	ml := newMockLedger()
	svc := NewTemplateService(ts, es, ml)
	return svc, ml
}

func TestCreateTemplate_Valid(t *testing.T) {
	svc, _ := newTemplateTestService()

	tmpl, err := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Web Development",
		CreatorAddr: "0xCreator",
		Milestones: []Milestone{
			{Name: "Design", Percentage: 30, Description: "UI/UX design"},
			{Name: "Development", Percentage: 50, Description: "Implementation"},
			{Name: "Testing", Percentage: 20, Description: "QA"},
		},
		TotalAmount:      "100.000000",
		AutoReleaseHours: 48,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if tmpl.Name != "Web Development" {
		t.Errorf("expected name 'Web Development', got %q", tmpl.Name)
	}
	if tmpl.TotalAmount != "100.000000" {
		t.Errorf("expected 100.000000, got %s", tmpl.TotalAmount)
	}
	if len(tmpl.Milestones) != 3 {
		t.Errorf("expected 3 milestones, got %d", len(tmpl.Milestones))
	}
	if tmpl.AutoReleaseHours != 48 {
		t.Errorf("expected 48h auto release, got %d", tmpl.AutoReleaseHours)
	}
}

func TestCreateTemplate_PercentageSumValidation(t *testing.T) {
	svc, _ := newTemplateTestService()

	_, err := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Bad Template",
		CreatorAddr: "0xCreator",
		Milestones: []Milestone{
			{Name: "Step 1", Percentage: 30},
			{Name: "Step 2", Percentage: 50},
		},
		TotalAmount: "100.000000",
	})
	if err == nil {
		t.Fatal("expected error for percentages not summing to 100")
	}
}

func TestCreateTemplate_EmptyMilestones(t *testing.T) {
	svc, _ := newTemplateTestService()

	_, err := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Empty",
		CreatorAddr: "0xCreator",
		Milestones:  []Milestone{},
		TotalAmount: "100.000000",
	})
	if err == nil {
		t.Fatal("expected error for empty milestones")
	}
}

func TestCreateTemplate_InvalidAmount(t *testing.T) {
	svc, _ := newTemplateTestService()

	_, err := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Bad Amount",
		CreatorAddr: "0xCreator",
		Milestones: []Milestone{
			{Name: "Step 1", Percentage: 100},
		},
		TotalAmount: "0.000000",
	})
	if err == nil {
		t.Fatal("expected error for zero amount")
	}
}

func TestInstantiateTemplate(t *testing.T) {
	svc, ml := newTemplateTestService()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Service Agreement",
		CreatorAddr: "0xCreator",
		Milestones: []Milestone{
			{Name: "Phase 1", Percentage: 40},
			{Name: "Phase 2", Percentage: 60},
		},
		TotalAmount:      "50.000000",
		AutoReleaseHours: 24,
	})

	esc, milestones, err := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xBuyer",
		SellerAddr: "0xSeller",
	})
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}

	if esc.Amount != "50.000000" {
		t.Errorf("expected amount 50.000000, got %s", esc.Amount)
	}
	if esc.Status != StatusPending {
		t.Errorf("expected status pending, got %s", esc.Status)
	}
	if len(milestones) != 2 {
		t.Fatalf("expected 2 milestones, got %d", len(milestones))
	}
	if milestones[0].Percentage != 40 {
		t.Errorf("expected first milestone 40%%, got %d%%", milestones[0].Percentage)
	}
	if milestones[1].Percentage != 60 {
		t.Errorf("expected second milestone 60%%, got %d%%", milestones[1].Percentage)
	}

	// Verify funds were locked
	if len(ml.locked) != 1 {
		t.Errorf("expected 1 escrow lock, got %d", len(ml.locked))
	}
}

func TestInstantiateTemplate_NotFound(t *testing.T) {
	svc, _ := newTemplateTestService()

	_, _, err := svc.InstantiateTemplate(context.Background(), "tmpl_nonexistent", InstantiateRequest{
		BuyerAddr:  "0xBuyer",
		SellerAddr: "0xSeller",
	})
	if err != ErrTemplateNotFound {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestReleaseMilestone(t *testing.T) {
	svc, ml := newTemplateTestService()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Milestone Test",
		CreatorAddr: "0xCreator",
		Milestones: []Milestone{
			{Name: "Phase 1", Percentage: 40},
			{Name: "Phase 2", Percentage: 60},
		},
		TotalAmount:      "100.000000",
		AutoReleaseHours: 24,
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	// Release first milestone (40% of 100 = 40)
	m, err := svc.ReleaseMilestone(context.Background(), esc.ID, 0, "0xbuyer")
	if err != nil {
		t.Fatalf("release milestone 0: %v", err)
	}
	if !m.Released {
		t.Error("expected milestone to be released")
	}
	if m.ReleasedAmount != "40.000000" {
		t.Errorf("expected released amount 40.000000, got %s", m.ReleasedAmount)
	}

	// Verify ledger release was called
	if len(ml.released) != 1 {
		t.Errorf("expected 1 release call, got %d", len(ml.released))
	}

	// Release second milestone (60% of 100 = 60) → should mark escrow as released
	m2, err := svc.ReleaseMilestone(context.Background(), esc.ID, 1, "0xbuyer")
	if err != nil {
		t.Fatalf("release milestone 1: %v", err)
	}
	if m2.ReleasedAmount != "60.000000" {
		t.Errorf("expected released amount 60.000000, got %s", m2.ReleasedAmount)
	}

	// Escrow should now be released
	finalEsc, _ := svc.escrowStore.Get(context.Background(), esc.ID)
	if finalEsc.Status != StatusReleased {
		t.Errorf("expected escrow status released after all milestones, got %s", finalEsc.Status)
	}
}

func TestReleaseMilestone_DoubleRelease(t *testing.T) {
	svc, _ := newTemplateTestService()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Double Release Test",
		CreatorAddr: "0xCreator",
		Milestones: []Milestone{
			{Name: "Phase 1", Percentage: 50},
			{Name: "Phase 2", Percentage: 50},
		},
		TotalAmount: "100.000000",
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	// Release once
	_, err := svc.ReleaseMilestone(context.Background(), esc.ID, 0, "0xbuyer")
	if err != nil {
		t.Fatalf("first release: %v", err)
	}

	// Try to release again
	_, err = svc.ReleaseMilestone(context.Background(), esc.ID, 0, "0xbuyer")
	if err != ErrMilestoneAlreadyDone {
		t.Errorf("expected ErrMilestoneAlreadyDone, got %v", err)
	}
}

func TestReleaseMilestone_UnauthorizedSeller(t *testing.T) {
	svc, _ := newTemplateTestService()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Auth Test",
		CreatorAddr: "0xCreator",
		Milestones: []Milestone{
			{Name: "Phase 1", Percentage: 100},
		},
		TotalAmount: "100.000000",
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	// Seller should not be able to release
	_, err := svc.ReleaseMilestone(context.Background(), esc.ID, 0, "0xseller")
	if err != ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized for seller, got %v", err)
	}
}

func TestListMilestones(t *testing.T) {
	svc, _ := newTemplateTestService()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "List Test",
		CreatorAddr: "0xCreator",
		Milestones: []Milestone{
			{Name: "A", Percentage: 25},
			{Name: "B", Percentage: 25},
			{Name: "C", Percentage: 25},
			{Name: "D", Percentage: 25},
		},
		TotalAmount: "200.000000",
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	milestones, err := svc.ListMilestones(context.Background(), esc.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(milestones) != 4 {
		t.Errorf("expected 4 milestones, got %d", len(milestones))
	}
	// Verify order
	for i, m := range milestones {
		if m.MilestoneIndex != i {
			t.Errorf("expected index %d, got %d", i, m.MilestoneIndex)
		}
	}
}

func TestGetTemplate_NotFound(t *testing.T) {
	svc, _ := newTemplateTestService()

	_, err := svc.GetTemplate(context.Background(), "tmpl_nonexistent")
	if err != ErrTemplateNotFound {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestListTemplates(t *testing.T) {
	svc, _ := newTemplateTestService()

	svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Template A",
		CreatorAddr: "0xCreator",
		Milestones:  []Milestone{{Name: "Step", Percentage: 100}},
		TotalAmount: "10.000000",
	})
	svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Template B",
		CreatorAddr: "0xCreator",
		Milestones:  []Milestone{{Name: "Step", Percentage: 100}},
		TotalAmount: "20.000000",
	})

	templates, err := svc.ListTemplates(context.Background(), 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(templates) != 2 {
		t.Errorf("expected 2 templates, got %d", len(templates))
	}
}
