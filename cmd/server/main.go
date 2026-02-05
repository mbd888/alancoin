// Alancoin - Payment infrastructure for AI agents
package main

import (
	"context"
	"os"

	"github.com/mbd888/alancoin/internal/config"
	"github.com/mbd888/alancoin/internal/logging"
	"github.com/mbd888/alancoin/internal/server"
)

// Build info - set by ldflags
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	// Create logger
	logger := logging.New("info", "text")

	logger.Info("starting alancoin",
		"version", Version,
		"commit", Commit,
		"build_time", BuildTime,
	)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Info("configuration loaded",
		"env", cfg.Env,
		"chain_id", cfg.ChainID,
		"usdc_contract", cfg.USDCContract,
	)

	// Create and run server
	srv, err := server.New(cfg, server.WithLogger(logger))
	if err != nil {
		logger.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if err := srv.Run(ctx); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
