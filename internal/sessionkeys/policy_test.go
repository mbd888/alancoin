package sessionkeys

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// helper to create a minimal active session key in the store
func createTestKey(t *testing.T, store Store, ownerAddr string) *SessionKey {
	t.Helper()
	key := &SessionKey{
		ID:        "sk_test_" + ownerAddr[:6],
		OwnerAddr: ownerAddr,
		PublicKey: "0xaabbccddee0011223344aabbccddee0011223344",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:  "100.00",
			ExpiresAt: time.Now().Add(24 * time.Hour),
			AllowAny:  true,
		},
		Usage: SessionKeyUsage{
			TotalSpent:   "0",
			SpentToday:   "0",
			LastResetDay: time.Now().Format("2006-01-02"),
		},
	}
	if err := store.Create(context.Background(), key); err != nil {
		t.Fatalf("createTestKey: %v", err)
	}
	return key
}

func mustJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- Policy CRUD tests ---

func TestPolicyCreateAndGet(t *testing.T) {
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	p := NewPolicy("rate limited", "0xabc123", []Rule{
		{Type: "rate_limit", Params: mustJSON(t, RateLimitParams{MaxTransactions: 5, WindowSeconds: 60})},
	})

	if err := ps.CreatePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, err := ps.GetPolicy(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "rate limited" {
		t.Errorf("name = %q, want %q", got.Name, "rate limited")
	}
	if len(got.Rules) != 1 {
		t.Fatalf("rules len = %d, want 1", len(got.Rules))
	}
}

func TestPolicyListByOwner(t *testing.T) {
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	p1 := NewPolicy("p1", "0xabc", nil)
	p2 := NewPolicy("p2", "0xabc", nil)
	p3 := NewPolicy("p3", "0xdef", nil)

	_ = ps.CreatePolicy(ctx, p1)
	_ = ps.CreatePolicy(ctx, p2)
	_ = ps.CreatePolicy(ctx, p3)

	policies, _ := ps.ListPolicies(ctx, "0xabc")
	if len(policies) != 2 {
		t.Fatalf("got %d policies, want 2", len(policies))
	}
}

func TestPolicyUpdate(t *testing.T) {
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	p := NewPolicy("old name", "0xabc", nil)
	_ = ps.CreatePolicy(ctx, p)

	p.Name = "new name"
	p.UpdatedAt = time.Now()
	if err := ps.UpdatePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, _ := ps.GetPolicy(ctx, p.ID)
	if got.Name != "new name" {
		t.Errorf("name = %q, want %q", got.Name, "new name")
	}
}

func TestPolicyDeleteCascadesAttachments(t *testing.T) {
	ps := NewPolicyMemoryStore()
	store := NewMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc123000000000000000000000000000000000001")
	p := NewPolicy("temp", key.OwnerAddr, nil)
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	// Delete policy should cascade
	if err := ps.DeletePolicy(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	atts, _ := ps.GetAttachments(ctx, key.ID)
	if len(atts) != 0 {
		t.Errorf("attachments should be empty after policy delete, got %d", len(atts))
	}
}

// --- Attach / Detach tests ---

func TestAttachDetach(t *testing.T) {
	ps := NewPolicyMemoryStore()
	store := NewMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc123000000000000000000000000000000000002")
	p := NewPolicy("test", key.OwnerAddr, nil)
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	if err := ps.AttachPolicy(ctx, att); err != nil {
		t.Fatal(err)
	}

	// Duplicate attach should fail
	if err := ps.AttachPolicy(ctx, att); err != ErrPolicyAlreadyExists {
		t.Errorf("duplicate attach: got %v, want ErrPolicyAlreadyExists", err)
	}

	atts, _ := ps.GetAttachments(ctx, key.ID)
	if len(atts) != 1 {
		t.Fatalf("got %d attachments, want 1", len(atts))
	}

	if err := ps.DetachPolicy(ctx, key.ID, p.ID); err != nil {
		t.Fatal(err)
	}

	atts, _ = ps.GetAttachments(ctx, key.ID)
	if len(atts) != 0 {
		t.Errorf("got %d attachments after detach, want 0", len(atts))
	}
}

// --- Rule Validation tests ---

func TestValidateRulesValid(t *testing.T) {
	rules := []Rule{
		{Type: "rate_limit", Params: mustJSON(t, RateLimitParams{MaxTransactions: 10, WindowSeconds: 60})},
		{Type: "time_window", Params: mustJSON(t, TimeWindowParams{StartHour: 9, EndHour: 17, Days: []string{"monday", "friday"}})},
		{Type: "cooldown", Params: mustJSON(t, CooldownParams{MinSeconds: 5})},
		{Type: "tx_count", Params: mustJSON(t, TxCountParams{MaxCount: 100})},
	}
	if err := ValidateRules(rules); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateRulesInvalid(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
	}{
		{"negative rate", Rule{Type: "rate_limit", Params: mustJSON(t, RateLimitParams{MaxTransactions: -1, WindowSeconds: 60})}},
		{"zero window", Rule{Type: "rate_limit", Params: mustJSON(t, RateLimitParams{MaxTransactions: 10, WindowSeconds: 0})}},
		{"bad hour", Rule{Type: "time_window", Params: mustJSON(t, TimeWindowParams{StartHour: 25, EndHour: 17})}},
		{"bad day", Rule{Type: "time_window", Params: mustJSON(t, TimeWindowParams{StartHour: 9, EndHour: 17, Days: []string{"notaday"}})}},
		{"bad tz", Rule{Type: "time_window", Params: mustJSON(t, TimeWindowParams{StartHour: 9, EndHour: 17, Timezone: "Invalid/Zone"})}},
		{"zero cooldown", Rule{Type: "cooldown", Params: mustJSON(t, CooldownParams{MinSeconds: 0})}},
		{"zero txcount", Rule{Type: "tx_count", Params: mustJSON(t, TxCountParams{MaxCount: 0})}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRules([]Rule{tc.rule}); err == nil {
				t.Error("expected error for invalid rule")
			}
		})
	}
}

func TestValidateUnknownRuleTypeIgnored(t *testing.T) {
	rules := []Rule{
		{Type: "future_rule", Params: json.RawMessage(`{"anything": true}`)},
	}
	if err := ValidateRules(rules); err != nil {
		t.Errorf("unknown rule types should be ignored, got: %v", err)
	}
}

// --- Evaluation tests ---

func TestEvalRateLimit(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc123000000000000000000000000000000000003")

	p := NewPolicy("rate_limited", key.OwnerAddr, []Rule{
		{Type: "rate_limit", Params: mustJSON(t, RateLimitParams{MaxTransactions: 2, WindowSeconds: 60})},
	})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	// First two transactions: should pass
	if err := evaluatePolicies(ctx, ps, key); err != nil {
		t.Fatalf("tx 1: %v", err)
	}
	recordPolicyUsage(ctx, ps, key.ID)

	if err := evaluatePolicies(ctx, ps, key); err != nil {
		t.Fatalf("tx 2: %v", err)
	}
	recordPolicyUsage(ctx, ps, key.ID)

	// Third: should be blocked
	err := evaluatePolicies(ctx, ps, key)
	if err != ErrRateLimitExceeded {
		t.Errorf("tx 3: got %v, want ErrRateLimitExceeded", err)
	}
}

func TestEvalRateLimitWindowExpiry(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc123000000000000000000000000000000000004")

	p := NewPolicy("rate_limited", key.OwnerAddr, []Rule{
		{Type: "rate_limit", Params: mustJSON(t, RateLimitParams{MaxTransactions: 1, WindowSeconds: 1})},
	})
	_ = ps.CreatePolicy(ctx, p)

	// Attach with a window start in the past (expired)
	state := map[string]RateLimitState{
		"rate_limit": {
			WindowStart: time.Now().Add(-2 * time.Second),
			Count:       1,
		},
	}
	stateJSON, _ := json.Marshal(state)
	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: stateJSON}
	_ = ps.AttachPolicy(ctx, att)

	// Should pass because window has expired
	if err := evaluatePolicies(ctx, ps, key); err != nil {
		t.Errorf("expected pass after window expiry, got %v", err)
	}
}

func TestEvalTimeWindowAllow(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc123000000000000000000000000000000000005")

	now := time.Now().UTC()
	p := NewPolicy("business_hours", key.OwnerAddr, []Rule{
		{Type: "time_window", Params: mustJSON(t, TimeWindowParams{
			StartHour: now.Hour(),
			EndHour:   now.Hour() + 1, // current hour is allowed
			Timezone:  "UTC",
		})},
	})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	if err := evaluatePolicies(ctx, ps, key); err != nil {
		t.Errorf("expected pass during allowed hour, got %v", err)
	}
}

func TestEvalTimeWindowBlock(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc123000000000000000000000000000000000006")

	now := time.Now().UTC()
	// Set window to hour that is NOT current
	blockedStart := (now.Hour() + 2) % 24
	blockedEnd := (now.Hour() + 3) % 24
	if blockedEnd <= blockedStart {
		blockedEnd = blockedStart + 1
		if blockedEnd > 23 {
			blockedEnd = 23
		}
	}

	p := NewPolicy("off_hours", key.OwnerAddr, []Rule{
		{Type: "time_window", Params: mustJSON(t, TimeWindowParams{
			StartHour: blockedStart,
			EndHour:   blockedEnd,
			Timezone:  "UTC",
		})},
	})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	err := evaluatePolicies(ctx, ps, key)
	if err != ErrOutsideTimeWindow {
		t.Errorf("expected ErrOutsideTimeWindow, got %v", err)
	}
}

func TestEvalTimeWindowDayRestriction(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc123000000000000000000000000000000000007")

	now := time.Now().UTC()
	// Restrict to a day that is NOT today
	today := now.Weekday()
	blockedDay := "monday"
	if today == time.Monday {
		blockedDay = "tuesday"
	}

	p := NewPolicy("day_restricted", key.OwnerAddr, []Rule{
		{Type: "time_window", Params: mustJSON(t, TimeWindowParams{
			StartHour: 0,
			EndHour:   23,
			Days:      []string{blockedDay},
			Timezone:  "UTC",
		})},
	})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	err := evaluatePolicies(ctx, ps, key)
	if err != ErrOutsideTimeWindow {
		t.Errorf("expected ErrOutsideTimeWindow for wrong day, got %v", err)
	}
}

func TestEvalCooldownFirstTx(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc123000000000000000000000000000000000008")

	p := NewPolicy("cooldown", key.OwnerAddr, []Rule{
		{Type: "cooldown", Params: mustJSON(t, CooldownParams{MinSeconds: 10})},
	})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	// First tx (LastUsed is zero) should pass
	if err := evaluatePolicies(ctx, ps, key); err != nil {
		t.Errorf("first tx should pass, got %v", err)
	}
}

func TestEvalCooldownBlock(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc123000000000000000000000000000000000009")
	// Simulate a recent transaction
	key.Usage.LastUsed = time.Now()
	_ = store.Update(ctx, key)

	p := NewPolicy("cooldown", key.OwnerAddr, []Rule{
		{Type: "cooldown", Params: mustJSON(t, CooldownParams{MinSeconds: 60})},
	})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	// Re-read key with updated LastUsed
	key, _ = store.Get(ctx, key.ID)

	err := evaluatePolicies(ctx, ps, key)
	if err != ErrCooldownActive {
		t.Errorf("expected ErrCooldownActive, got %v", err)
	}
}

func TestEvalCooldownAllow(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc12300000000000000000000000000000000000a")
	// Simulate old transaction
	key.Usage.LastUsed = time.Now().Add(-120 * time.Second)
	_ = store.Update(ctx, key)

	p := NewPolicy("cooldown", key.OwnerAddr, []Rule{
		{Type: "cooldown", Params: mustJSON(t, CooldownParams{MinSeconds: 60})},
	})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	key, _ = store.Get(ctx, key.ID)
	if err := evaluatePolicies(ctx, ps, key); err != nil {
		t.Errorf("expected pass after cooldown, got %v", err)
	}
}

func TestEvalTxCountBlock(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc12300000000000000000000000000000000000b")
	key.Usage.TransactionCount = 5
	_ = store.Update(ctx, key)

	p := NewPolicy("limited", key.OwnerAddr, []Rule{
		{Type: "tx_count", Params: mustJSON(t, TxCountParams{MaxCount: 5})},
	})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	key, _ = store.Get(ctx, key.ID)
	err := evaluatePolicies(ctx, ps, key)
	if err != ErrTxCountExceeded {
		t.Errorf("expected ErrTxCountExceeded, got %v", err)
	}
}

func TestEvalTxCountAllow(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc12300000000000000000000000000000000000c")
	key.Usage.TransactionCount = 4
	_ = store.Update(ctx, key)

	p := NewPolicy("limited", key.OwnerAddr, []Rule{
		{Type: "tx_count", Params: mustJSON(t, TxCountParams{MaxCount: 5})},
	})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	key, _ = store.Get(ctx, key.ID)
	if err := evaluatePolicies(ctx, ps, key); err != nil {
		t.Errorf("expected pass (4 < 5), got %v", err)
	}
}

// --- Multiple policies on one key (all must pass) ---

func TestMultiplePoliciesAllMustPass(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc12300000000000000000000000000000000000d")

	// Policy 1: tx count limit (allows)
	p1 := NewPolicy("count_ok", key.OwnerAddr, []Rule{
		{Type: "tx_count", Params: mustJSON(t, TxCountParams{MaxCount: 100})},
	})
	_ = ps.CreatePolicy(ctx, p1)

	// Policy 2: cooldown (blocks — just transacted)
	key.Usage.LastUsed = time.Now()
	_ = store.Update(ctx, key)

	p2 := NewPolicy("cooldown_block", key.OwnerAddr, []Rule{
		{Type: "cooldown", Params: mustJSON(t, CooldownParams{MinSeconds: 3600})},
	})
	_ = ps.CreatePolicy(ctx, p2)

	// Attach both
	_ = ps.AttachPolicy(ctx, &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p1.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)})
	_ = ps.AttachPolicy(ctx, &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p2.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)})

	key, _ = store.Get(ctx, key.ID)
	err := evaluatePolicies(ctx, ps, key)
	if err != ErrCooldownActive {
		t.Errorf("expected ErrCooldownActive from second policy, got %v", err)
	}
}

// --- No policies attached: should always pass ---

func TestNoPoliciesPass(t *testing.T) {
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := &SessionKey{ID: "sk_nopol", Usage: SessionKeyUsage{TransactionCount: 999}}

	if err := evaluatePolicies(ctx, ps, key); err != nil {
		t.Errorf("expected pass with no policies, got %v", err)
	}
}

// --- Empty rules in policy: should pass ---

func TestEmptyRulesPass(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc12300000000000000000000000000000000000e")

	p := NewPolicy("empty", key.OwnerAddr, []Rule{})
	_ = ps.CreatePolicy(ctx, p)

	att := &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)}
	_ = ps.AttachPolicy(ctx, att)

	if err := evaluatePolicies(ctx, ps, key); err != nil {
		t.Errorf("expected pass with empty rules, got %v", err)
	}
}

// --- Rate limit state progression across multiple txs ---

func TestRateLimitStateProgression(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	key := createTestKey(t, store, "0xabc12300000000000000000000000000000000000f")

	p := NewPolicy("rate5", key.OwnerAddr, []Rule{
		{Type: "rate_limit", Params: mustJSON(t, RateLimitParams{MaxTransactions: 5, WindowSeconds: 3600})},
	})
	_ = ps.CreatePolicy(ctx, p)
	_ = ps.AttachPolicy(ctx, &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)})

	for i := 1; i <= 5; i++ {
		if err := evaluatePolicies(ctx, ps, key); err != nil {
			t.Fatalf("tx %d should pass, got %v", i, err)
		}
		recordPolicyUsage(ctx, ps, key.ID)
	}

	// 6th should fail
	err := evaluatePolicies(ctx, ps, key)
	if err != ErrRateLimitExceeded {
		t.Errorf("tx 6: expected ErrRateLimitExceeded, got %v", err)
	}

	// Verify state was persisted correctly
	atts, _ := ps.GetAttachments(ctx, key.ID)
	if len(atts) != 1 {
		t.Fatal("expected 1 attachment")
	}
	state := parseRuleState(atts[0].RuleState)
	rs := state["rate_limit"]
	if rs.Count != 5 {
		t.Errorf("rate_limit count = %d, want 5", rs.Count)
	}
}

// --- Integration: manager.validateTransaction calls policy engine ---

func TestManagerValidateCallsPolicies(t *testing.T) {
	store := NewMemoryStore()
	ps := NewPolicyMemoryStore()
	ctx := context.Background()

	mgr := NewManager(store, nil, ps)

	key := &SessionKey{
		ID:        "sk_mgr_test",
		OwnerAddr: "0xowner",
		PublicKey: "0xaabbccddee0011223344aabbccddee0011223344",
		CreatedAt: time.Now(),
		Permission: Permission{
			MaxTotal:  "100.00",
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
		},
		Usage: SessionKeyUsage{
			TotalSpent:       "0",
			SpentToday:       "0",
			LastResetDay:     time.Now().Format("2006-01-02"),
			TransactionCount: 10,
		},
	}
	_ = store.Create(ctx, key)

	// Attach a policy that blocks (tx count exceeded)
	p := NewPolicy("block", key.OwnerAddr, []Rule{
		{Type: "tx_count", Params: mustJSON(t, TxCountParams{MaxCount: 5})},
	})
	_ = ps.CreatePolicy(ctx, p)
	_ = ps.AttachPolicy(ctx, &PolicyAttachment{SessionKeyID: key.ID, PolicyID: p.ID, AttachedAt: time.Now(), RuleState: []byte(`{}`)})

	err := mgr.Validate(ctx, key.ID, "0x1234567890123456789012345678901234567890", "1.00", "")
	if err != ErrTxCountExceeded {
		t.Errorf("expected ErrTxCountExceeded from policy, got %v", err)
	}
}

// --- Overnight time window test ---

func TestEvalTimeWindowOvernight(t *testing.T) {
	rule := Rule{
		Type:   "time_window",
		Params: mustJSON(t, TimeWindowParams{StartHour: 22, EndHour: 6, Timezone: "UTC"}),
	}

	// 23:00 UTC should be allowed (between 22 and 6)
	at := time.Date(2025, 1, 1, 23, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, at); err != nil {
		t.Errorf("23:00 should be allowed in 22-6 window, got %v", err)
	}

	// 3:00 UTC should be allowed
	at = time.Date(2025, 1, 1, 3, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, at); err != nil {
		t.Errorf("3:00 should be allowed in 22-6 window, got %v", err)
	}

	// 12:00 UTC should be blocked
	at = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := evalTimeWindow(rule, at); err != ErrOutsideTimeWindow {
		t.Errorf("12:00 should be blocked in 22-6 window, got %v", err)
	}
}
