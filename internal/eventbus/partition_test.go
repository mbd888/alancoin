package eventbus

import (
	"fmt"
	"testing"
)

func TestPartitionDeterministic(t *testing.T) {
	// Same key always maps to same partition
	p1 := Partition("0xAgent1", 16)
	p2 := Partition("0xAgent1", 16)
	if p1 != p2 {
		t.Errorf("same key gave different partitions: %d vs %d", p1, p2)
	}
}

func TestPartitionDistribution(t *testing.T) {
	// 1000 unique keys should spread across partitions
	counts := make(map[int]int)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("0x%040d", i) // unique 40-char hex-like keys
		p := Partition(key, 16)
		counts[p]++
	}

	// Every partition should get at least some events
	for i := 0; i < 16; i++ {
		if counts[i] == 0 {
			t.Errorf("partition %d got 0 events out of 1000", i)
		}
	}
}

func TestPartitionSinglePartition(t *testing.T) {
	// With 1 partition, everything goes to 0
	p := Partition("anything", 1)
	if p != 0 {
		t.Errorf("single partition: got %d, want 0", p)
	}
}

func TestPartitionedChannelsRoute(t *testing.T) {
	pc := NewPartitionedChannels(4, 100)

	// Route events with same key — should go to same partition
	e1 := Event{Key: "0xAgent1"}
	e2 := Event{Key: "0xAgent1"}
	e3 := Event{Key: "0xAgent2"}

	pc.Route(e1)
	pc.Route(e2)
	pc.Route(e3)

	p1 := Partition("0xAgent1", 4)
	p2 := Partition("0xAgent2", 4)

	// Agent1's partition should have 2 events
	if len(pc.channels[p1]) != 2 {
		t.Errorf("partition %d has %d events, want 2", p1, len(pc.channels[p1]))
	}

	// Agent2's partition should have 1 event (unless same partition as Agent1)
	if p1 != p2 {
		if len(pc.channels[p2]) != 1 {
			t.Errorf("partition %d has %d events, want 1", p2, len(pc.channels[p2]))
		}
	}

	pc.Close()
}

func TestPartitionedChannelsFull(t *testing.T) {
	pc := NewPartitionedChannels(2, 1) // buffer size 1

	e := Event{Key: "0xA"}
	ok1 := pc.Route(e) // should succeed
	ok2 := pc.Route(e) // should fail (buffer full)

	if !ok1 {
		t.Error("first route should succeed")
	}
	if ok2 {
		t.Error("second route should fail (buffer full)")
	}

	pc.Close()
}
