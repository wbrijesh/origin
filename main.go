package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/wbrijesh/origin/internal/config"
	"github.com/wbrijesh/origin/internal/db"
	sshsrv "github.com/wbrijesh/origin/internal/ssh"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.Info("configuration loaded",
		"name", cfg.Name,
		"data_path", cfg.DataPath,
		"ssh_addr", cfg.SSH.ListenAddr,
		"http_addr", cfg.HTTP.ListenAddr,
	)

	// Create data directories
	if err := cfg.EnsureDirectories(); err != nil {
		slog.Error("failed to create directories", "error", err)
		os.Exit(1)
	}

	// Open database
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	slog.Info("database ready", "path", cfg.DBPath())

	// Set up graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Create SSH server
	sshServer, err := sshsrv.New(cfg, database)
	if err != nil {
		slog.Error("failed to create SSH server", "error", err)
		os.Exit(1)
	}

	// Start servers concurrently
	g, _ := errgroup.WithContext(ctx)

	g.Go(func() error {
		return sshServer.ListenAndServe()
	})

	// TODO: Phase 5 — start HTTP server

	slog.Info(fmt.Sprintf("%s is ready", cfg.Name))

	// Wait for shutdown signal
	go func() {
		<-ctx.Done()
		slog.Info("shutting down...")
		sshServer.Close()
	}()

	if err := g.Wait(); err != nil {
		slog.Error("server error", "error", err)
	}
}
