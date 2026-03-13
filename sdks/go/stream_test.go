package alancoin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newStreamServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/streams", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(streamResponse{
			Stream: Stream{
				ID:           "str_test",
				BuyerAddr:    "0xBUYER",
				SellerAddr:   "0xSELLER",
				HoldAmount:   "10.00",
				SpentAmount:  "0.00",
				PricePerTick: "0.01",
				TickCount:    0,
				Status:       "open",
				CreatedAt:    now,
			},
		})
	})

	mux.HandleFunc("GET /v1/streams/str_test", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(streamResponse{
			Stream: Stream{
				ID:          "str_test",
				Status:      "open",
				HoldAmount:  "10.00",
				SpentAmount: "0.50",
				TickCount:   50,
			},
		})
	})

	mux.HandleFunc("POST /v1/streams/str_test/tick", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tickResponse{
			Tick: StreamTick{
				ID:         "tick_1",
				StreamID:   "str_test",
				Seq:        1,
				Amount:     "0.01",
				Cumulative: "0.01",
				CreatedAt:  now,
			},
			Stream: Stream{
				ID:          "str_test",
				SpentAmount: "0.01",
				TickCount:   1,
				Status:      "open",
			},
		})
	})

	mux.HandleFunc("POST /v1/streams/str_test/close", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(streamResponse{
			Stream: Stream{
				ID:          "str_test",
				Status:      "settled",
				SpentAmount: "0.50",
				TickCount:   50,
				CloseReason: "completed",
			},
		})
	})

	mux.HandleFunc("GET /v1/streams/str_test/ticks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listTicksResponse{
			Ticks: []StreamTick{
				{ID: "tick_1", Seq: 1, Amount: "0.01", Cumulative: "0.01"},
				{ID: "tick_2", Seq: 2, Amount: "0.01", Cumulative: "0.02"},
			},
		})
	})

	mux.HandleFunc("GET /v1/agents/0xBUYER/streams", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listStreamsResponse{
			Streams: []Stream{
				{ID: "str_1", Status: "open"},
				{ID: "str_2", Status: "settled"},
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestOpenStream(t *testing.T) {
	srv := newStreamServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	stream, err := c.OpenStream(context.Background(), OpenStreamRequest{
		BuyerAddr:    "0xBUYER",
		SellerAddr:   "0xSELLER",
		HoldAmount:   "10.00",
		PricePerTick: "0.01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stream.ID != "str_test" || stream.Status != "open" {
		t.Errorf("stream = %+v", stream)
	}
	if stream.HoldAmount != "10.00" {
		t.Errorf("HoldAmount = %q", stream.HoldAmount)
	}
}

func TestGetStream(t *testing.T) {
	srv := newStreamServer(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	stream, err := c.GetStream(context.Background(), "str_test")
	if err != nil {
		t.Fatal(err)
	}
	if stream.TickCount != 50 {
		t.Errorf("TickCount = %d", stream.TickCount)
	}
}

func TestTickStream(t *testing.T) {
	srv := newStreamServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	tick, stream, err := c.TickStream(context.Background(), "str_test", TickStreamRequest{
		Amount:   "0.01",
		Metadata: "token_count=50",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tick.Amount != "0.01" {
		t.Errorf("tick Amount = %q", tick.Amount)
	}
	if stream.TickCount != 1 {
		t.Errorf("stream TickCount = %d", stream.TickCount)
	}
}

func TestCloseStream(t *testing.T) {
	srv := newStreamServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	stream, err := c.CloseStream(context.Background(), "str_test", "completed")
	if err != nil {
		t.Fatal(err)
	}
	if stream.Status != "settled" {
		t.Errorf("Status = %q", stream.Status)
	}
	if stream.SpentAmount != "0.50" {
		t.Errorf("SpentAmount = %q", stream.SpentAmount)
	}
}

func TestListStreamTicks(t *testing.T) {
	srv := newStreamServer(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	ticks, err := c.ListStreamTicks(context.Background(), "str_test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 {
		t.Errorf("len = %d", len(ticks))
	}
}

func TestListStreams(t *testing.T) {
	srv := newStreamServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("ak_test"))
	streams, err := c.ListStreams(context.Background(), "0xBUYER", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 2 {
		t.Errorf("len = %d", len(streams))
	}
}
