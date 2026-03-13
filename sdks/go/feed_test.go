package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newFeedServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/feed", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(FeedResponse{
			Feed: []FeedEntry{
				{
					ID:          "tx_1",
					FromName:    "Agent A",
					FromAddress: "0xAAA",
					ToName:      "Agent B",
					ToAddress:   "0xBBB",
					Amount:      "10.50",
					ServiceName: "GPT-4 Inference",
					ServiceType: "inference",
					TxHash:      "0xtx1",
					Timestamp:   now,
					TimeAgo:     "5 minutes ago",
				},
			},
			Stats: FeedStats{
				TotalAgents:       42,
				TotalTransactions: 1250,
				TotalVolume:       "12500.50",
			},
			Message: "AI agents hiring each other in real-time",
		})
	})

	mux.HandleFunc("GET /v1/network/stats/enhanced", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"totalAgents":       42,
			"totalTransactions": 1250,
			"totalVolume":       "12500.50",
			"activeAgents24h":   15,
			"avgLatencyMs":      250,
		})
	})

	return httptest.NewServer(mux)
}

func TestFeed(t *testing.T) {
	srv := newFeedServer(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	feed, err := c.Feed(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Feed) != 1 {
		t.Errorf("feed len = %d", len(feed.Feed))
	}
	if feed.Feed[0].FromName != "Agent A" {
		t.Errorf("FromName = %q", feed.Feed[0].FromName)
	}
	if feed.Stats.TotalAgents != 42 {
		t.Errorf("TotalAgents = %d", feed.Stats.TotalAgents)
	}
	if feed.Message != "AI agents hiring each other in real-time" {
		t.Errorf("Message = %q", feed.Message)
	}
}

func TestEnhancedNetworkStats(t *testing.T) {
	srv := newFeedServer(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	stats, err := c.EnhancedNetworkStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats["totalAgents"] != float64(42) {
		t.Errorf("totalAgents = %v", stats["totalAgents"])
	}
}
