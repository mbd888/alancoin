package ledger

import (
	"context"
	"testing"
	"time"
)

func TestAlertChecker_LowBalance(t *testing.T) {
	ctx := context.Background()
	alertStore := NewMemoryAlertStore()
	checker := NewAlertChecker(alertStore)

	agent := "0xaaaa"

	_ = alertStore.CreateConfig(ctx, &AlertConfig{
		AgentAddr: agent,
		AlertType: "low_balance",
		Threshold: "5.000000",
		Enabled:   true,
	})

	// Balance above threshold — no alert
	bal := &Balance{
		AgentAddr:   agent,
		Available:   "10.000000",
		CreditLimit: "0",
		CreditUsed:  "0",
	}
	checker.Check(ctx, agent, bal, "spend", "1.000000")
	time.Sleep(10 * time.Millisecond) // webhooks run async but Check is sync for store

	alerts := alertStore.Alerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts, got %d", len(alerts))
	}

	// Balance at threshold — should trigger
	bal.Available = "5.000000"
	checker.Check(ctx, agent, bal, "spend", "5.000000")

	alerts = alertStore.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].AlertType != "low_balance" {
		t.Errorf("expected alertType 'low_balance', got %q", alerts[0].AlertType)
	}
}

func TestAlertChecker_LargeTx(t *testing.T) {
	ctx := context.Background()
	alertStore := NewMemoryAlertStore()
	checker := NewAlertChecker(alertStore)

	agent := "0xbbbb"

	_ = alertStore.CreateConfig(ctx, &AlertConfig{
		AgentAddr: agent,
		AlertType: "large_tx",
		Threshold: "100.000000",
		Enabled:   true,
	})

	bal := &Balance{
		AgentAddr:   agent,
		Available:   "500.000000",
		CreditLimit: "0",
		CreditUsed:  "0",
	}

	// Small tx — no alert
	checker.Check(ctx, agent, bal, "spend", "50.000000")
	if len(alertStore.Alerts()) != 0 {
		t.Fatal("expected no alerts for small tx")
	}

	// Large tx — alert
	checker.Check(ctx, agent, bal, "spend", "150.000000")
	alerts := alertStore.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].AlertType != "large_tx" {
		t.Errorf("expected 'large_tx', got %q", alerts[0].AlertType)
	}
}

func TestAlertChecker_CreditHigh(t *testing.T) {
	ctx := context.Background()
	alertStore := NewMemoryAlertStore()
	checker := NewAlertChecker(alertStore)

	agent := "0xcccc"

	_ = alertStore.CreateConfig(ctx, &AlertConfig{
		AgentAddr: agent,
		AlertType: "credit_high",
		Threshold: "80", // 80% utilization
		Enabled:   true,
	})

	bal := &Balance{
		AgentAddr:   agent,
		Available:   "0.000000",
		CreditLimit: "100.000000",
		CreditUsed:  "50.000000", // 50% — below threshold
	}

	checker.Check(ctx, agent, bal, "spend", "1.000000")
	if len(alertStore.Alerts()) != 0 {
		t.Fatal("expected no alerts at 50% credit utilization")
	}

	// 90% — above threshold
	bal.CreditUsed = "90.000000"
	checker.Check(ctx, agent, bal, "spend", "1.000000")
	alerts := alertStore.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].AlertType != "credit_high" {
		t.Errorf("expected 'credit_high', got %q", alerts[0].AlertType)
	}
}

func TestAlertChecker_DisabledConfig(t *testing.T) {
	ctx := context.Background()
	alertStore := NewMemoryAlertStore()
	checker := NewAlertChecker(alertStore)

	agent := "0xdddd"

	_ = alertStore.CreateConfig(ctx, &AlertConfig{
		AgentAddr: agent,
		AlertType: "low_balance",
		Threshold: "50.000000",
		Enabled:   true,
	})

	// Get config ID
	configs, _ := alertStore.GetConfigs(ctx, agent)
	configID := configs[0].ID

	// Disable the config
	_ = alertStore.DeleteConfig(ctx, configID)

	// Should not trigger
	bal := &Balance{
		AgentAddr:   agent,
		Available:   "1.000000",
		CreditLimit: "0",
		CreditUsed:  "0",
	}
	checker.Check(ctx, agent, bal, "spend", "1.000000")
	if len(alertStore.Alerts()) != 0 {
		t.Fatal("expected no alerts from disabled config")
	}
}

func TestAlertStore_GetAlerts(t *testing.T) {
	ctx := context.Background()
	alertStore := NewMemoryAlertStore()

	for i := 0; i < 5; i++ {
		_ = alertStore.CreateAlert(ctx, &Alert{
			AgentAddr: "0xA",
			AlertType: "low_balance",
			Message:   "test",
		})
	}

	alerts, err := alertStore.GetAlerts(ctx, "0xA", 3)
	if err != nil {
		t.Fatalf("GetAlerts failed: %v", err)
	}
	if len(alerts) != 3 {
		t.Errorf("expected 3 alerts (limit), got %d", len(alerts))
	}
}

func TestAlertChecker_Integration(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	alertStore := NewMemoryAlertStore()
	checker := NewAlertChecker(alertStore)
	l := New(store).WithAlertChecker(checker)

	agent := "0x1234567890123456789012345678901234567890"

	_ = alertStore.CreateConfig(ctx, &AlertConfig{
		AgentAddr: agent,
		AlertType: "low_balance",
		Threshold: "5.000000",
		Enabled:   true,
	})

	_ = l.Deposit(ctx, agent, "10.000000", "0xtx1")

	// Spend to bring balance below threshold
	_ = l.Spend(ctx, agent, "6.000000", "sk_1")

	// Allow goroutine to fire
	time.Sleep(50 * time.Millisecond)

	alerts := alertStore.Alerts()
	if len(alerts) == 0 {
		t.Error("expected at least one alert after balance drop")
	}
}
