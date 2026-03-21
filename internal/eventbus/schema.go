package eventbus

import (
	"encoding/json"
	"fmt"
	"sync"
)

// SchemaRegistry manages versioned event payload schemas.
// Consumers declare which schema version they understand.
// The registry handles backward-compatible decoding of older versions.
//
// This prevents breaking changes when payload fields are added/removed:
//   - New fields: old consumers ignore them (JSON unmarshalling is lenient)
//   - Removed fields: consumers get zero values (safe for optional fields)
//   - Type changes: must bump major version and register migration
//
// Usage:
//
//	registry := NewSchemaRegistry()
//	registry.Register("settlement.completed", 1, SettlementPayloadV1{})
//	registry.Register("settlement.completed", 2, SettlementPayloadV2{})
//	registry.RegisterMigration("settlement.completed", 1, 2, migrateV1toV2)
type SchemaRegistry struct {
	mu         sync.RWMutex
	schemas    map[string]map[int]interface{}   // topic -> version -> zero-value example
	migrations map[string]map[int]MigrationFunc // topic -> fromVersion -> migration
	latest     map[string]int                   // topic -> latest version
}

// MigrationFunc converts a payload from one version to the next.
type MigrationFunc func(old json.RawMessage) (json.RawMessage, error)

// NewSchemaRegistry creates a new registry.
func NewSchemaRegistry() *SchemaRegistry {
	return &SchemaRegistry{
		schemas:    make(map[string]map[int]interface{}),
		migrations: make(map[string]map[int]MigrationFunc),
		latest:     make(map[string]int),
	}
}

// Register adds a schema version for a topic.
func (r *SchemaRegistry) Register(topic string, version int, example interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.schemas[topic] == nil {
		r.schemas[topic] = make(map[int]interface{})
	}
	r.schemas[topic][version] = example

	if version > r.latest[topic] {
		r.latest[topic] = version
	}
}

// RegisterMigration adds a migration function from one version to the next.
func (r *SchemaRegistry) RegisterMigration(topic string, fromVersion, toVersion int, fn MigrationFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.migrations[topic] == nil {
		r.migrations[topic] = make(map[int]MigrationFunc)
	}
	r.migrations[topic][fromVersion] = fn
}

// LatestVersion returns the latest registered version for a topic.
func (r *SchemaRegistry) LatestVersion(topic string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.latest[topic]
}

// Migrate upgrades a payload from fromVersion to the latest version.
func (r *SchemaRegistry) Migrate(topic string, fromVersion int, payload json.RawMessage) (json.RawMessage, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	current := payload
	version := fromVersion

	for version < r.latest[topic] {
		fn, ok := r.migrations[topic][version]
		if !ok {
			return current, version, fmt.Errorf("no migration from v%d for topic %q", version, topic)
		}
		migrated, err := fn(current)
		if err != nil {
			return current, version, fmt.Errorf("migration v%d→v%d failed: %w", version, version+1, err)
		}
		current = migrated
		version++
	}

	return current, version, nil
}

// VersionedEvent wraps an Event with schema version metadata.
type VersionedEvent struct {
	Event
	SchemaVersion int `json:"schemaVersion"`
}

// WrapWithVersion creates a versioned event using the latest schema version.
func (r *SchemaRegistry) WrapWithVersion(event Event) VersionedEvent {
	return VersionedEvent{
		Event:         event,
		SchemaVersion: r.LatestVersion(event.Topic),
	}
}

// --- Default schema versions for Alancoin events ---

// InitDefaultSchemas registers the current schema versions.
func InitDefaultSchemas(r *SchemaRegistry) {
	// Settlement v1 — current
	r.Register(TopicSettlement, 1, SettlementPayload{})

	// Future: when SettlementPayloadV2 is needed, register it:
	// r.Register(TopicSettlement, 2, SettlementPayloadV2{})
	// r.RegisterMigration(TopicSettlement, 1, 2, func(old json.RawMessage) (json.RawMessage, error) {
	//     var v1 SettlementPayload
	//     json.Unmarshal(old, &v1)
	//     v2 := SettlementPayloadV2{...convert...}
	//     return json.Marshal(v2)
	// })
}
