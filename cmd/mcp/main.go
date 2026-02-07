// Alancoin MCP Server - Exposes Alancoin capabilities as MCP tools for LLMs
package main

import (
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"

	"github.com/mbd888/alancoin/internal/mcpserver"
)

func main() {
	cfg := mcpserver.Config{
		APIURL:       envOrDefault("ALANCOIN_API_URL", "http://localhost:8080"),
		APIKey:       os.Getenv("ALANCOIN_API_KEY"),
		AgentAddress: os.Getenv("ALANCOIN_AGENT_ADDRESS"),
	}

	if cfg.APIKey == "" {
		fmt.Fprintln(os.Stderr, "ALANCOIN_API_KEY is required")
		os.Exit(1)
	}
	if cfg.AgentAddress == "" {
		fmt.Fprintln(os.Stderr, "ALANCOIN_AGENT_ADDRESS is required")
		os.Exit(1)
	}

	s := mcpserver.NewMCPServer(cfg)
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
