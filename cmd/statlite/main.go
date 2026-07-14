package main

// This file wires CLI startup, configuration loading, monitor startup, and shutdown.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pvrlabs/statlite/internal/app"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/server"
	"github.com/pvrlabs/statlite/internal/storage"
	"github.com/pvrlabs/statlite/internal/version"
)

func main() {
	flag.Usage = func() {
		printHelp(flag.CommandLine.Output())
	}

	configPath := flag.String("config", "statlite.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		printVersion(os.Stdout)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	timeout, err := time.ParseDuration(cfg.Polling.Timeout)
	if err != nil {
		log.Fatalf("config: polling.timeout: %v", err)
	}
	interval, err := time.ParseDuration(cfg.Polling.Interval)
	if err != nil {
		log.Fatalf("config: polling.interval: %v", err)
	}

	if !config.IsLoopbackListen(cfg.Server.Listen) {
		log.Printf("WARNING: StatLite has no built-in dashboard/API auth. Non-local listen address %q must be protected by firewall, VPN, SSH tunnel, or reverse proxy auth.", cfg.Server.Listen)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := storage.Open(ctx, cfg.Storage.SQLitePath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()
	retentionCutoff := storage.NewRetentionCutoffTracker(cfg.Storage.RetentionDays)

	manager, err := app.NewMonitorManager(cfg.Targets, store, timeout, interval)
	if err != nil {
		log.Fatalf("monitor manager: %v", err)
	}
	// Prune before starting monitor goroutines so the first poll only sees retained history.
	storage.StartRetentionCleanup(ctx, store, cfg.Storage.RetentionDays, retentionCutoff.Set)
	manager.Start(ctx)

	srv := server.NewWithManagerRetentionCutoff(cfg.Server.Listen, manager, cfg.Storage.RetentionDays, retentionCutoff.Current)
	log.Printf("StatLite starting on %s with %d target(s)", cfg.Server.Listen, len(manager.Names()))

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()

	if err := srv.Start(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "statlite %s\n", version.Version)
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `StatLite - tiny self-hosted metrics dashboard for small servers.

Polls Spring Boot Actuator (and StatLite self-health), stores samples in
local SQLite, and serves a localhost dashboard.

Usage:
  statlite [--config path]
  statlite --version
  statlite --help

Options:
  --config path   Config file (default: statlite.yaml)
  --version       Print version and exit
  --help          Show this help

Docs: README.md, docs/configuration.md
`)
}
