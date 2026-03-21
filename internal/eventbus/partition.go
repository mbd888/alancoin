package eventbus

import (
	"hash/fnv"
)

// Partitioner assigns events to partitions based on their key.
// Events with the same key always go to the same partition,
// guaranteeing ordering within a key (e.g. all events for agent 0xABC
// are processed in order, even with concurrent consumers).
//
// This mirrors Kafka's partitioning semantics for the in-memory bus.

// NumPartitions is the default partition count.
// Should match Kafka topic partition count for consistent behavior when migrating.
const NumPartitions = 16

// Partition returns the partition index for a key.
// Uses FNV-1a hash for fast, uniform distribution.
func Partition(key string, numPartitions int) int {
	if numPartitions <= 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32()) % numPartitions
}

// PartitionedChannels creates a set of partitioned channels.
// Events are routed to channels based on their key, ensuring
// per-key ordering while allowing parallel processing across partitions.
type PartitionedChannels struct {
	channels []chan Event
	count    int
}

// NewPartitionedChannels creates N partitioned channels.
func NewPartitionedChannels(n, bufferSize int) *PartitionedChannels {
	if n <= 0 {
		n = NumPartitions
	}
	chs := make([]chan Event, n)
	for i := range chs {
		chs[i] = make(chan Event, bufferSize)
	}
	return &PartitionedChannels{channels: chs, count: n}
}

// Route sends an event to the correct partition based on its key.
func (pc *PartitionedChannels) Route(event Event) bool {
	idx := Partition(event.Key, pc.count)
	select {
	case pc.channels[idx] <- event:
		return true
	default:
		return false // partition full
	}
}

// Channel returns the channel for partition i.
func (pc *PartitionedChannels) Channel(i int) <-chan Event {
	return pc.channels[i]
}

// Close closes all partition channels.
func (pc *PartitionedChannels) Close() {
	for _, ch := range pc.channels {
		close(ch)
	}
}

// Count returns the number of partitions.
func (pc *PartitionedChannels) Count() int {
	return pc.count
}
