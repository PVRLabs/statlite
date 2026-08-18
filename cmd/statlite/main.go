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
	for _, warning := range cfg.DeprecationWarnings() {
		log.Printf("WARNING: %s", warning)
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
		log.Printf("WARNING: StatLite's dashboard and API have no built-in authentication. Listening on %q is normal in a container, but ensure the published port is restricted or protected by a firewall, VPN, SSH tunnel, or authenticated reverse proxy.", cfg.Server.Listen)
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

	srv := server.NewWithManagerRetentionCutoffAndFilesystem(cfg.Server.Listen, manager, cfg.Storage.RetentionDays, retentionCutoff.Current, cfg.Storage.SQLitePath)
	listener, err := srv.Listen()
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Print(startupMessage(cfg.Server.Listen, len(manager.Names())))

	// The listener is bound before starting monitor goroutines so the first
	// poll can reach StatLite's own metrics endpoint. Keep cleanup ahead of
	// serving HTTP so requests cannot observe partial startup initialization.
	// Prune before starting monitor goroutines so the first poll only sees retained history.
	storage.StartRetentionCleanup(ctx, store, cfg.Storage.RetentionDays, retentionCutoff.Set)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	manager.Start(ctx)

	if err := <-serveErr; err != nil {
		log.Fatalf("server: %v", err)
	}
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "statlite %s\n", version.Version)
}

func startupMessage(listen string, targets int) string {
	return fmt.Sprintf("StatLite starting: version=%s listen=%s targets=%d", version.Version, listen, targets)
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `StatLite - tiny self-hosted metrics dashboard for small servers.

Polls Spring Boot Actuator and StatLite self-monitoring endpoints, stores
samples in local SQLite, and serves a localhost dashboard.

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
