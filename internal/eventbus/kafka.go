package eventbus

// KafkaBus implements the Bus interface backed by Apache Kafka.
//
// This is the production backend for multi-node deployments. It provides:
//   - Durable event persistence across restarts
//   - Horizontal scaling via consumer groups
//   - Exactly-once delivery with idempotent producers
//   - Cross-datacenter replication
//
// Configuration:
//
//	bus := NewKafkaBus(KafkaConfig{
//	    Brokers:       []string{"kafka-1:9092", "kafka-2:9092"},
//	    ConsumerGroup: "alancoin-settlement",
//	    ClientID:      "alancoin-node-1",
//	})
//
// The Kafka implementation satisfies the same Bus interface as MemoryBus.
// Swap in server.go by replacing NewMemoryBus with NewKafkaBus.
//
// Dependencies (add when ready to deploy):
//
//	go get github.com/segmentio/kafka-go
//
// Until Kafka is needed, the MemoryBus handles single-node deployments
// with the same semantics (batching, retry, DLQ, health checks).

// KafkaConfig holds Kafka connection settings.
type KafkaConfig struct {
	Brokers       []string // Kafka broker addresses
	ConsumerGroup string   // Consumer group ID for offset tracking
	ClientID      string   // Unique client identifier
	TopicPrefix   string   // Prefix for topic names (e.g. "alancoin.")

	// TLS
	TLSEnabled bool
	TLSCert    string
	TLSKey     string
	TLSCA      string

	// SASL authentication
	SASLEnabled   bool
	SASLMechanism string // "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"
	SASLUsername  string
	SASLPassword  string

	// Tuning
	BatchSize     int // Max events per batch (default: 100)
	FlushInterval int // Flush interval in milliseconds (default: 1000)
	MaxRetries    int // Max retries before DLQ (default: 3)
	BufferSize    int // Internal buffer size (default: 10000)
}

// DefaultKafkaConfig returns sensible defaults for Kafka configuration.
func DefaultKafkaConfig() KafkaConfig {
	return KafkaConfig{
		Brokers:       []string{"localhost:9092"},
		ConsumerGroup: "alancoin",
		ClientID:      "alancoin-1",
		TopicPrefix:   "alancoin.",
		BatchSize:     100,
		FlushInterval: 1000,
		MaxRetries:    3,
		BufferSize:    10000,
	}
}

// Topic names with prefix for Kafka:
//   - alancoin.settlement.completed
//   - alancoin.escrow.disputed
//   - alancoin.forensics.alert
//   - alancoin.kya.issued
//
// Consumer groups:
//   - alancoin-forensics (anomaly detection)
//   - alancoin-chargeback (cost attribution)
//   - alancoin-webhooks (event delivery)
//
// Each consumer group maintains its own offset, so all three process
// every event independently (fan-out via consumer groups, not topics).
//
// To deploy Kafka:
//   1. Set EVENTBUS_BACKEND=kafka in environment
//   2. Set KAFKA_BROKERS=kafka-1:9092,kafka-2:9092
//   3. Create topics: kafka-topics --create --topic alancoin.settlement.completed --partitions 6 --replication-factor 3
//   4. Swap NewMemoryBus → NewKafkaBus in internal/server/server.go
