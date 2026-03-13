// Command go-agent demonstrates using the Alancoin Go SDK to build an
// autonomous AI agent that discovers, pays for, and consumes services.
//
// Usage:
//
//	export ALANCOIN_URL=http://localhost:8080
//	export ALANCOIN_API_KEY=ak_...
//	go run ./examples/go-agent
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	alancoin "github.com/mbd888/alancoin/sdks/go"
)

func main() {
	baseURL := envOr("ALANCOIN_URL", "http://localhost:8080")
	apiKey := envOr("ALANCOIN_API_KEY", "")

	if apiKey == "" {
		log.Fatal("ALANCOIN_API_KEY is required")
	}

	c := alancoin.NewClient(baseURL,
		alancoin.WithAPIKey(apiKey),
		alancoin.WithTimeout(30*time.Second),
	)
	ctx := context.Background()

	// 1. Check platform health
	fmt.Println("--- Platform Health ---")
	health, err := c.Health(ctx)
	if err != nil {
		log.Fatalf("health check failed: %v", err)
	}
	fmt.Printf("Status: %v\n\n", health["status"])

	// 2. Discover available inference services
	fmt.Println("--- Service Discovery ---")
	services, err := c.Discover(ctx, alancoin.DiscoverOptions{
		Type:   alancoin.ServiceTypeInference,
		SortBy: "price",
		Limit:  5,
	})
	if err != nil {
		log.Fatalf("discovery failed: %v", err)
	}
	for _, svc := range services {
		fmt.Printf("  %s (%s) - $%s/call by %s [rep: %.1f]\n",
			svc.Name, svc.Type, svc.Price, svc.AgentName, svc.ReputationScore)
	}
	fmt.Println()

	// 3. Open a gateway session with a $5 budget
	fmt.Println("--- Gateway Session ---")
	gw, err := c.Gateway(ctx, alancoin.GatewayConfig{
		MaxTotal:      "5.00",
		MaxPerRequest: "1.00",
		Strategy:      "best_value",
	})
	if err != nil {
		log.Fatalf("gateway creation failed: %v", err)
	}
	defer gw.Close(ctx)
	fmt.Printf("Session: %s (budget: $5.00)\n\n", gw.ID())

	// 4. Make inference calls
	fmt.Println("--- Inference Calls ---")
	prompts := []string{
		"What is the capital of France?",
		"Explain quantum computing in one sentence.",
		"Write a haiku about Go programming.",
	}
	for _, prompt := range prompts {
		result, err := gw.Call(ctx, alancoin.ServiceTypeInference, nil, map[string]any{
			"prompt": prompt,
		})
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
			continue
		}
		fmt.Printf("  Q: %s\n", prompt)
		fmt.Printf("  A: %v\n", result.Response["text"])
		fmt.Printf("  Cost: $%s via %s (%dms)\n\n",
			result.AmountPaid, result.ServiceName, result.LatencyMs)
	}

	// 5. Run a multi-step pipeline: embed → search → summarize
	fmt.Println("--- Pipeline ---")
	pipeline, err := gw.Pipeline(ctx, []alancoin.PipelineStep{
		{ServiceType: alancoin.ServiceTypeEmbedding, Params: map[string]any{"text": "AI agent payments"}},
		{ServiceType: alancoin.ServiceTypeSearch, Params: map[string]any{"vector": "$prev", "k": 3}},
		{ServiceType: alancoin.ServiceTypeInference, Params: map[string]any{"prompt": "Summarize: $prev"}},
	})
	if err != nil {
		fmt.Printf("  Pipeline error: %v\n\n", err)
	} else {
		for _, step := range pipeline.Steps {
			fmt.Printf("  Step %d (%s): $%s via %s\n",
				step.StepIndex, step.ServiceType, step.AmountPaid, step.ServiceName)
		}
		fmt.Printf("  Total pipeline cost: $%s\n\n", pipeline.TotalPaid)
	}

	// 6. Check session logs
	fmt.Println("--- Session Logs ---")
	logs, err := gw.Logs(ctx, 10)
	if err != nil {
		fmt.Printf("  Error: %v\n", err)
	} else {
		for _, l := range logs {
			fmt.Printf("  [%s] %s → %s: $%s (%dms)\n",
				l.Status, l.ServiceType, l.AgentCalled, l.Amount, l.LatencyMs)
		}
	}
	fmt.Println()

	// 7. Check network health via flywheel
	fmt.Println("--- Network Health ---")
	fwHealth, err := c.FlywheelHealth(ctx)
	if err != nil {
		fmt.Printf("  Error: %v\n", err)
	} else {
		fmt.Printf("  Health: %.1f/100 (%s)\n", fwHealth.HealthScore, fwHealth.HealthTier)
		fmt.Printf("  Velocity: %.1f | Growth: %.1f | Density: %.1f\n",
			fwHealth.VelocityScore, fwHealth.GrowthScore, fwHealth.DensityScore)
	}
	fmt.Println()

	// 8. Close session — print final spend
	info, err := gw.Close(ctx)
	if err != nil {
		log.Fatalf("close failed: %v", err)
	}
	fmt.Printf("--- Session Closed ---\nSpent: $%s | Requests: %d | Status: %s\n",
		info.TotalSpent, info.RequestCount, info.Status)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
