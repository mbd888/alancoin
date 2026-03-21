package eventbus

import (
	"encoding/json"
	"testing"
)

func TestSchemaRegistryVersioning(t *testing.T) {
	r := NewSchemaRegistry()

	type V1 struct {
		Amount string `json:"amount"`
	}
	type V2 struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}

	r.Register("test.event", 1, V1{})
	r.Register("test.event", 2, V2{})

	if r.LatestVersion("test.event") != 2 {
		t.Errorf("latest = %d, want 2", r.LatestVersion("test.event"))
	}
	if r.LatestVersion("nonexistent") != 0 {
		t.Errorf("nonexistent = %d, want 0", r.LatestVersion("nonexistent"))
	}
}

func TestSchemaRegistryMigration(t *testing.T) {
	r := NewSchemaRegistry()

	r.Register("test.event", 1, struct{}{})
	r.Register("test.event", 2, struct{}{})

	// Migration: add currency field
	r.RegisterMigration("test.event", 1, 2, func(old json.RawMessage) (json.RawMessage, error) {
		var v1 map[string]interface{}
		json.Unmarshal(old, &v1)
		v1["currency"] = "USDC"
		return json.Marshal(v1)
	})

	v1Payload, _ := json.Marshal(map[string]string{"amount": "10.00"})

	migrated, version, err := r.Migrate("test.event", 1, v1Payload)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if version != 2 {
		t.Errorf("version = %d, want 2", version)
	}

	var result map[string]string
	json.Unmarshal(migrated, &result)
	if result["currency"] != "USDC" {
		t.Errorf("currency = %q, want USDC", result["currency"])
	}
	if result["amount"] != "10.00" {
		t.Errorf("amount = %q, want 10.00 (preserved)", result["amount"])
	}
}

func TestSchemaRegistryNoMigrationNeeded(t *testing.T) {
	r := NewSchemaRegistry()
	r.Register("test.event", 1, struct{}{})

	payload, _ := json.Marshal(map[string]string{"x": "y"})

	// Already at latest version
	result, version, err := r.Migrate("test.event", 1, payload)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
	}
	if string(result) != string(payload) {
		t.Error("payload should be unchanged")
	}
}

func TestSchemaRegistryMissingMigration(t *testing.T) {
	r := NewSchemaRegistry()
	r.Register("test.event", 1, struct{}{})
	r.Register("test.event", 3, struct{}{}) // skip v2

	payload, _ := json.Marshal(map[string]string{"x": "y"})

	_, _, err := r.Migrate("test.event", 1, payload)
	if err == nil {
		t.Error("expected error for missing migration v1→v2")
	}
}

func TestSchemaRegistryMultiStepMigration(t *testing.T) {
	r := NewSchemaRegistry()
	r.Register("test.event", 1, struct{}{})
	r.Register("test.event", 2, struct{}{})
	r.Register("test.event", 3, struct{}{})

	r.RegisterMigration("test.event", 1, 2, func(old json.RawMessage) (json.RawMessage, error) {
		var m map[string]interface{}
		json.Unmarshal(old, &m)
		m["step1"] = true
		return json.Marshal(m)
	})
	r.RegisterMigration("test.event", 2, 3, func(old json.RawMessage) (json.RawMessage, error) {
		var m map[string]interface{}
		json.Unmarshal(old, &m)
		m["step2"] = true
		return json.Marshal(m)
	})

	payload, _ := json.Marshal(map[string]interface{}{"original": true})

	migrated, version, err := r.Migrate("test.event", 1, payload)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if version != 3 {
		t.Errorf("version = %d, want 3", version)
	}

	var result map[string]interface{}
	json.Unmarshal(migrated, &result)
	if result["step1"] != true || result["step2"] != true || result["original"] != true {
		t.Errorf("migration chain incomplete: %+v", result)
	}
}

func TestDefaultSchemas(t *testing.T) {
	r := NewSchemaRegistry()
	InitDefaultSchemas(r)

	if r.LatestVersion(TopicSettlement) != 1 {
		t.Errorf("settlement latest = %d, want 1", r.LatestVersion(TopicSettlement))
	}
}
