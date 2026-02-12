package escrow

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestCreateTemplate_NameValidation(t *testing.T) {
	svc, _ := newTemplateTestService()

	tests := []struct {
		name         string
		templateName string
		wantErr      bool
	}{
		{"empty name", "", true},
		{"whitespace only", "   ", true},
		{"valid name", "Valid Template", false},
		{"name with spaces trimmed", "  Template Name  ", false},
		{"max length 255", strings.Repeat("a", 255), false},
		{"exceeds max length", strings.Repeat("a", 256), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
				Name:        tt.templateName,
				CreatorAddr: "0xCreator",
				Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
				TotalAmount: "10.000000",
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error=%v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCreateTemplate_MilestoneNameValidation(t *testing.T) {
	svc, _ := newTemplateTestService()

	tests := []struct {
		name          string
		milestoneName string
		wantErr       bool
	}{
		{"empty milestone name", "", true},
		{"whitespace only", "   ", true},
		{"valid name", "Phase 1", false},
		{"max length 255", strings.Repeat("a", 255), false},
		{"exceeds max length", strings.Repeat("a", 256), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
				Name:        "Test Template",
				CreatorAddr: "0xCreator",
				Milestones:  []Milestone{{Name: tt.milestoneName, Percentage: 100}},
				TotalAmount: "10.000000",
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error=%v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCreateTemplate_MilestonePercentageBoundaries(t *testing.T) {
	svc, _ := newTemplateTestService()

	tests := []struct {
		name        string
		percentages []int
		wantErr     bool
	}{
		{"single 100%", []int{100}, false},
		{"zero percent", []int{0}, true},
		{"negative percent", []int{-10, 110}, true},
		{"over 100 percent", []int{101}, true},
		{"sum to 101", []int{51, 50}, true},
		{"sum to 99", []int{50, 49}, true},
		{"multiple sum to 100", []int{25, 25, 25, 25}, false},
		{"boundary 1% each", []int{1, 1, 98}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			milestones := make([]Milestone, len(tt.percentages))
			for i, pct := range tt.percentages {
				milestones[i] = Milestone{
					Name:       "M" + string(rune('0'+i)),
					Percentage: pct,
				}
			}

			_, err := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
				Name:        "Test",
				CreatorAddr: "0xCreator",
				Milestones:  milestones,
				TotalAmount: "100.000000",
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("percentages %v: expected error=%v, got %v", tt.percentages, tt.wantErr, err)
			}
		})
	}
}

func TestCreateTemplate_MilestoneCountLimits(t *testing.T) {
	svc, _ := newTemplateTestService()

	tests := []struct {
		name    string
		count   int
		wantErr bool
	}{
		{"zero milestones", 0, true},
		{"one milestone", 1, false},
		{"twenty milestones", 20, false},
		{"twenty-one milestones", 21, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			milestones := make([]Milestone, tt.count)
			if tt.count > 0 {
				pct := 100 / tt.count
				remainder := 100 - (pct * tt.count)
				for i := 0; i < tt.count; i++ {
					milestones[i] = Milestone{
						Name:       "M" + string(rune('0'+i)),
						Percentage: pct,
					}
				}
				if remainder > 0 {
					milestones[0].Percentage += remainder
				}
			}

			_, err := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
				Name:        "Test",
				CreatorAddr: "0xCreator",
				Milestones:  milestones,
				TotalAmount: "100.000000",
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("count %d: expected error=%v, got %v", tt.count, tt.wantErr, err)
			}
		})
	}
}

func TestCreateTemplate_AutoReleaseHoursDefault(t *testing.T) {
	svc, _ := newTemplateTestService()

	tmpl, err := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:             "Test",
		CreatorAddr:      "0xCreator",
		Milestones:       []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount:      "10.000000",
		AutoReleaseHours: 0, // Should default to 24
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if tmpl.AutoReleaseHours != 24 {
		t.Errorf("expected default 24 hours, got %d", tmpl.AutoReleaseHours)
	}
}

func TestTemplateMemoryStore_ListTemplatesByCreator(t *testing.T) {
	store := NewTemplateMemoryStore()

	// Create templates for different creators
	tmpl1 := &EscrowTemplate{
		ID:          "tmpl_1",
		Name:        "Template 1",
		CreatorAddr: "0xcreator1",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "10.000000",
	}
	tmpl2 := &EscrowTemplate{
		ID:          "tmpl_2",
		Name:        "Template 2",
		CreatorAddr: "0xcreator2",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "20.000000",
	}
	tmpl3 := &EscrowTemplate{
		ID:          "tmpl_3",
		Name:        "Template 3",
		CreatorAddr: "0xcreator1", // Same as tmpl1
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "30.000000",
	}

	store.CreateTemplate(context.Background(), tmpl1)
	store.CreateTemplate(context.Background(), tmpl2)
	store.CreateTemplate(context.Background(), tmpl3)

	// List templates for creator1 (should get 2)
	result, err := store.ListTemplatesByCreator(context.Background(), "0xcreator1", 50)
	if err != nil {
		t.Fatalf("list by creator: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 templates for creator1, got %d", len(result))
	}

	// Case-insensitive match
	result, err = store.ListTemplatesByCreator(context.Background(), "0xCREATOR1", 50)
	if err != nil {
		t.Fatalf("list by creator: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 templates for creator1 (case insensitive), got %d", len(result))
	}
}

func TestTemplateMemoryStore_CopySemantics(t *testing.T) {
	store := NewTemplateMemoryStore()

	tmpl := &EscrowTemplate{
		ID:          "tmpl_test",
		Name:        "Original",
		CreatorAddr: "0xcreator",
		Milestones: []Milestone{
			{Name: "M1", Percentage: 50},
			{Name: "M2", Percentage: 50},
		},
		TotalAmount: "100.000000",
	}
	store.CreateTemplate(context.Background(), tmpl)

	// Get template and mutate the returned copy
	retrieved, _ := store.GetTemplate(context.Background(), "tmpl_test")
	retrieved.Milestones[0].Name = "MUTATED"

	// Get again and verify original is unchanged
	fresh, _ := store.GetTemplate(context.Background(), "tmpl_test")
	if fresh.Milestones[0].Name != "M1" {
		t.Errorf("store did not return a copy; milestone was mutated to %s", fresh.Milestones[0].Name)
	}
}

func TestTemplateMilestone_CopySemantics(t *testing.T) {
	store := NewTemplateMemoryStore()

	m := &EscrowMilestone{
		EscrowID:       "esc_test",
		TemplateID:     "tmpl_test",
		MilestoneIndex: 0,
		Name:           "Original",
		Percentage:     100,
		Released:       false,
	}
	store.CreateMilestone(context.Background(), m)

	// Get milestone and mutate
	retrieved, _ := store.GetMilestone(context.Background(), "esc_test", 0)
	retrieved.Name = "MUTATED"

	// Get again and verify original is unchanged
	fresh, _ := store.GetMilestone(context.Background(), "esc_test", 0)
	if fresh.Name != "Original" {
		t.Errorf("store did not return a copy; name was mutated to %s", fresh.Name)
	}
}

func TestReleaseMilestone_ConcurrentReleases(t *testing.T) {
	svc, ml := newTemplateTestService()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Concurrent Test",
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

	// Try to release the same milestone concurrently
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.ReleaseMilestone(context.Background(), esc.ID, 0, "0xbuyer")
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Count errors (should have 9 failures for already released)
	alreadyDoneCount := 0
	for err := range errors {
		if err == ErrMilestoneAlreadyDone {
			alreadyDoneCount++
		}
	}

	if alreadyDoneCount != 9 {
		t.Errorf("expected 9 'already done' errors from concurrent releases, got %d", alreadyDoneCount)
	}

	// Verify ledger was only called once
	if len(ml.released) != 1 {
		t.Errorf("expected 1 release call to ledger, got %d", len(ml.released))
	}
}

func TestInstantiateTemplate_SameBuyerAndSeller(t *testing.T) {
	svc, _ := newTemplateTestService()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Test",
		CreatorAddr: "0xCreator",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "10.000000",
	})

	_, _, err := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xsame",
		SellerAddr: "0xsame",
	})

	if err == nil {
		t.Error("expected error when buyer and seller are the same")
	}
}

func TestReleaseMilestone_MilestoneNotFound(t *testing.T) {
	svc, _ := newTemplateTestService()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Test",
		CreatorAddr: "0xCreator",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "10.000000",
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	// Try to release non-existent milestone index
	_, err := svc.ReleaseMilestone(context.Background(), esc.ID, 999, "0xbuyer")
	if err != ErrMilestoneNotFound {
		t.Errorf("expected ErrMilestoneNotFound, got %v", err)
	}
}

func TestReleaseMilestone_InvalidEscrowStatus(t *testing.T) {
	svc, _ := newTemplateTestService()

	tmpl, _ := svc.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:        "Test",
		CreatorAddr: "0xCreator",
		Milestones:  []Milestone{{Name: "M1", Percentage: 100}},
		TotalAmount: "10.000000",
	})

	esc, _, _ := svc.InstantiateTemplate(context.Background(), tmpl.ID, InstantiateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
	})

	// Change escrow status to released
	e, _ := svc.escrowStore.Get(context.Background(), esc.ID)
	e.Status = StatusReleased
	svc.escrowStore.Update(context.Background(), e)

	// Try to release milestone on released escrow
	_, err := svc.ReleaseMilestone(context.Background(), esc.ID, 0, "0xbuyer")
	if err != ErrInvalidStatus {
		t.Errorf("expected ErrInvalidStatus, got %v", err)
	}
}
