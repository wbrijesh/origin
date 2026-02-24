package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/wbrijesh/origin/internal/config"
	"github.com/wbrijesh/origin/internal/db"
	"github.com/wbrijesh/origin/internal/hooks"
	httpsrv "github.com/wbrijesh/origin/internal/http"
	sshsrv "github.com/wbrijesh/origin/internal/ssh"
)

func main() {
	// Hidden "hook" subcommand — called by git hook scripts, not by users.
	if len(os.Args) >= 3 && os.Args[1] == "hook" {
		runHook(os.Args[2])
		return
	}

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

	// Create HTTP server
	httpServer := httpsrv.New(cfg, database)

	slog.Info(fmt.Sprintf("%s is ready", cfg.Name))

	// Start servers concurrently.
	// Each server runs independently — if one fails, the other keeps going.
	go func() {
		if err := sshServer.ListenAndServe(); err != nil {
			slog.Error("SSH server failed", "error", err)
		}
	}()

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && ctx.Err() == nil {
			slog.Error("HTTP server failed", "error", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	slog.Info("shutting down...")
	sshServer.Close()
	httpServer.Close()
}

// runHook executes a git hook. Called by the hook scripts that
// GenerateHooks writes into each bare repo.
func runHook(hookName string) {
	// Hooks log to stderr which git forwards to the pusher
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	switch hookName {
	case "pre-receive":
		if err := hooks.VerifyPreReceive(os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "origin: push rejected — %v\n", err)
			os.Exit(1)
		}
	case "post-receive":
		if err := hooks.RunPostReceive(os.Stdin); err != nil {
			slog.Error("post-receive hook error", "error", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "origin: unknown hook: %s\n", hookName)
		os.Exit(1)
	}
}
