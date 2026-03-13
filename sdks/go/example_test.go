package alancoin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/mbd888/alancoin/sdks/go"
)

// stubServer returns a test server that responds to common Alancoin API routes.
func stubServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /v1/gateway/sessions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"session": map[string]any{
				"id": "sess_example", "status": "active",
				"maxTotal": "5.00", "totalSpent": "0.00",
			},
		})
	})

	mux.HandleFunc("POST /v1/gateway/proxy", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"response":    map[string]any{"text": "Hello from the agent network!"},
			"serviceUsed": "0xSELLER",
			"serviceName": "GPT-Agent",
			"amountPaid":  "0.10",
			"totalSpent":  "0.10",
			"remaining":   "4.90",
			"latencyMs":   55,
		})
	})

	mux.HandleFunc("DELETE /v1/gateway/sessions/sess_example", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"session": map[string]any{"id": "sess_example", "status": "closed", "totalSpent": "0.10"},
		})
	})

	mux.HandleFunc("POST /v1/gateway/call", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"response":    map[string]any{"text": "One-shot result"},
			"serviceUsed": "0xSELLER",
			"amountPaid":  "0.25",
			"latencyMs":   30,
		})
	})

	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{
				{"id": "svc_1", "type": "inference", "name": "GPT Agent", "price": "0.10", "agentAddress": "0xA"},
				{"id": "svc_2", "type": "inference", "name": "Claude Agent", "price": "0.15", "agentAddress": "0xB"},
			},
		})
	})

	return httptest.NewServer(mux)
}

func ExampleNewClient() {
	srv := stubServer()
	defer srv.Close()

	c := alancoin.NewClient(srv.URL,
		alancoin.WithAPIKey("ak_example"),
	)

	health, err := c.Health(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Println(health["status"])
	// Output: ok
}

func ExampleClient_Gateway() {
	srv := stubServer()
	defer srv.Close()

	c := alancoin.NewClient(srv.URL, alancoin.WithAPIKey("ak_example"))
	ctx := context.Background()

	gw, err := c.Gateway(ctx, alancoin.GatewayConfig{MaxTotal: "5.00"})
	if err != nil {
		panic(err)
	}
	defer gw.Close(ctx)

	result, err := gw.Call(ctx, "inference", nil, map[string]any{"prompt": "hello"})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Response["text"])
	// Output: Hello from the agent network!
}

func ExampleSpend() {
	srv := stubServer()
	defer srv.Close()

	result, err := alancoin.Spend(context.Background(), srv.URL, "ak_example",
		"inference", "1.00", map[string]any{"prompt": "hello"},
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Response["text"])
	// Output: One-shot result
}

func ExampleConnect() {
	srv := stubServer()
	defer srv.Close()

	ctx := context.Background()
	gw, err := alancoin.Connect(ctx, srv.URL, "ak_example", "5.00")
	if err != nil {
		panic(err)
	}
	defer gw.Close(ctx)

	result, err := gw.Call(ctx, "inference", nil, map[string]any{"prompt": "hello"})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.AmountPaid)
	// Output: 0.10
}

func ExampleClient_Discover() {
	srv := stubServer()
	defer srv.Close()

	c := alancoin.NewClient(srv.URL, alancoin.WithAPIKey("ak_example"))

	services, err := c.Discover(context.Background(), alancoin.DiscoverOptions{
		Type: "inference",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(len(services))
	// Output: 2
}
