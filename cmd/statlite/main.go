package main

// This file wires CLI startup, configuration loading, monitor startup, and shutdown.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pvrlabs/statlite/internal/app"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/inspect"
	"github.com/pvrlabs/statlite/internal/server"
	"github.com/pvrlabs/statlite/internal/storage"
	"github.com/pvrlabs/statlite/internal/version"
	"gopkg.in/yaml.v3"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithInspector(args, stdout, stderr, inspect.Application)
}

type inspectApplicationFunc func(context.Context, string) (*inspect.Result, error)

func runWithInspector(args []string, stdout, stderr io.Writer, inspectApplication inspectApplicationFunc) int {
	if len(args) > 0 && args[0] == "inspect" {
		return runInspect(args[1:], stdout, stderr, inspectApplication)
	}
	return runMonitor(args, stdout, stderr)
}

func runInspect(args []string, stdout, stderr io.Writer, inspectApplication inspectApplicationFunc) int {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			printInspectHelp(stdout)
			return 0
		}
	}
	inspectFlags := flag.NewFlagSet("statlite inspect", flag.ContinueOnError)
	inspectFlags.SetOutput(stderr)
	inspectFlags.Usage = func() {
		printInspectHelp(stderr)
	}
	if err := inspectFlags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if inspectFlags.NArg() != 1 {
		if inspectFlags.NArg() == 0 {
			fmt.Fprintln(stderr, "inspect: missing application URL")
		} else {
			fmt.Fprintln(stderr, "inspect: expected exactly one application URL")
		}
		printInspectHelp(stderr)
		return 2
	}

	result, err := inspectApplication(context.Background(), inspectFlags.Arg(0))
	if err != nil {
		printInspectFailure(stderr, err)
		if isInspectUsageError(err) {
			return 2
		}
		return 1
	}
	output, err := renderInspection(result)
	if err != nil {
		fmt.Fprintf(stderr, "inspect: could not render suggested target: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, output)
	return 0
}

func runMonitor(args []string, stdout, stderr io.Writer) int {
	implicitConfig := !hasConfigFlag(args)
	monitorFlags := flag.NewFlagSet("statlite", flag.ContinueOnError)
	monitorFlags.SetOutput(stderr)
	monitorFlags.Usage = func() {
		printHelp(stderr)
	}

	configPath := monitorFlags.String("config", "statlite.yaml", "path to config file")
	showVersion := monitorFlags.Bool("version", false, "print version and exit")
	if err := monitorFlags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion {
		printVersion(stdout)
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "config: %v\n", err)
		if implicitConfig && *configPath == "statlite.yaml" && errors.Is(err, os.ErrNotExist) {
			printMissingConfigSuggestion(stderr)
		}
		return 1
	}
	logger := log.New(stderr, "", log.LstdFlags)
	for _, warning := range cfg.DeprecationWarnings() {
		logger.Printf("WARNING: %s", warning)
	}

	timeout, err := time.ParseDuration(cfg.Polling.Timeout)
	if err != nil {
		logger.Printf("config: polling.timeout: %v", err)
		return 1
	}
	interval, err := time.ParseDuration(cfg.Polling.Interval)
	if err != nil {
		logger.Printf("config: polling.interval: %v", err)
		return 1
	}

	if !config.IsLoopbackListen(cfg.Server.Listen) {
		logger.Printf("WARNING: StatLite's dashboard and API have no built-in authentication. Listening on %q is normal in a container, but ensure the published port is restricted or protected by a firewall, VPN, SSH tunnel, or authenticated reverse proxy.", cfg.Server.Listen)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := storage.Open(ctx, cfg.Storage.SQLitePath)
	if err != nil {
		logger.Printf("storage: %v", err)
		return 1
	}
	defer store.Close()
	retentionCutoff := storage.NewRetentionCutoffTracker(cfg.Storage.RetentionDays)

	manager, err := app.NewMonitorManager(cfg.Targets, store, timeout, interval)
	if err != nil {
		logger.Printf("monitor manager: %v", err)
		return 1
	}

	srv := server.NewWithManagerRetentionCutoffAndFilesystem(cfg.Server.Listen, manager, cfg.Storage.RetentionDays, retentionCutoff.Current, cfg.Storage.SQLitePath)
	listener, err := srv.Listen()
	if err != nil {
		logger.Printf("server: %v", err)
		return 1
	}
	logger.Print(startupMessage(cfg.Server.Listen, len(manager.Names())))

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
			logger.Printf("server shutdown: %v", err)
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
		logger.Printf("server: %v", err)
		return 1
	}
	return 0
}

func hasConfigFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--config" || arg == "-config" || strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "-config=") {
			return true
		}
	}
	return false
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
  statlite inspect <application-url>
  statlite --version
  statlite --help

Options:
  --config path   Config file (default: statlite.yaml)
  --version       Print version and exit
  --help          Show this help

Docs: README.md, docs/configuration.md
`)
}

func printInspectHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  statlite inspect <application-url>

Probe a supported application endpoint and print an inspection summary.
Inspection is read-only and does not require or create statlite.yaml.`)
}

type springSuggestedTarget struct {
	Name            string `yaml:"name"`
	Type            string `yaml:"type"`
	ActuatorBaseURL string `yaml:"actuator_base_url"`
}

type statliteSuggestedTarget struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	URL  string `yaml:"url"`
}

type springSuggestedConfig struct {
	Targets []springSuggestedTarget `yaml:"targets"`
}

type statliteSuggestedConfig struct {
	Targets []statliteSuggestedTarget `yaml:"targets"`
}

func renderInspection(result *inspect.Result) (string, error) {
	if result == nil {
		return "", errors.New("inspection returned no result")
	}
	targetYAML, err := renderSuggestedTarget(result)
	if err != nil {
		return "", err
	}

	name := "StatLite Metrics v1"
	if result.TargetType == inspect.TargetSpring {
		name = "Spring Boot Actuator"
	} else if result.TargetType != inspect.TargetStatliteMetrics {
		return "", fmt.Errorf("unsupported inspection target type %q", result.TargetType)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Detected: %s\n\nEndpoint:\n  %s\n\nAvailable:\n", name, result.Endpoint)
	for _, capability := range result.Capabilities {
		fmt.Fprintf(&output, "  ✓ %s\n", capability)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(&output, "\nWarning: %s\n", warning)
	}
	fmt.Fprintf(&output, "\nSuggested target:\n\n%s\nNext: add this target to statlite.yaml, run statlite, then open\nhttp://127.0.0.1:9090\n", targetYAML)
	return output.String(), nil
}

func renderSuggestedTarget(result *inspect.Result) (string, error) {
	switch result.TargetType {
	case inspect.TargetSpring:
		cfg := springSuggestedConfig{Targets: []springSuggestedTarget{{
			Name:            "app",
			Type:            "spring",
			ActuatorBaseURL: result.Endpoint,
		}}}
		validation := config.Config{
			Server:  config.ServerConfig{Listen: "127.0.0.1:9090"},
			Storage: config.StorageConfig{SQLitePath: "./statlite.sqlite"},
			Polling: config.PollingConfig{Interval: "30s"},
			Targets: []config.TargetConfig{{Name: "app", Type: config.TargetTypeSpring, ActuatorBaseURL: result.Endpoint}},
		}
		if err := config.Validate(&validation); err != nil {
			return "", fmt.Errorf("spring target: %w", err)
		}
		return marshalSuggestedConfig(cfg)
	case inspect.TargetStatliteMetrics:
		cfg := statliteSuggestedConfig{Targets: []statliteSuggestedTarget{{
			Name: "app",
			Type: config.TargetTypeStatliteMetrics,
			URL:  result.Endpoint,
		}}}
		validation := config.Config{
			Server:  config.ServerConfig{Listen: "127.0.0.1:9090"},
			Storage: config.StorageConfig{SQLitePath: "./statlite.sqlite"},
			Polling: config.PollingConfig{Interval: "30s"},
			Targets: []config.TargetConfig{{Name: "app", Type: config.TargetTypeStatliteMetrics, URL: result.Endpoint}},
		}
		if err := config.Validate(&validation); err != nil {
			return "", fmt.Errorf("statlite-metrics target: %w", err)
		}
		return marshalSuggestedConfig(cfg)
	default:
		return "", fmt.Errorf("unsupported inspection target type %q", result.TargetType)
	}
}

func marshalSuggestedConfig(value any) (string, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal YAML: %w", err)
	}
	return string(data), nil
}

func printInspectFailure(w io.Writer, err error) {
	var failure *inspect.Failure
	if !errors.As(err, &failure) {
		fmt.Fprintf(w, "inspect: invalid application URL: %v\n", err)
		return
	}
	switch failure.Kind {
	case inspect.FailureAuthRequired:
		fmt.Fprintln(w, "inspect: authentication is required; inspection cannot continue")
	case inspect.FailureUnreachable:
		fmt.Fprintln(w, "inspect: could not connect to the application")
	case inspect.FailureIncomplete:
		fmt.Fprintln(w, "inspect: inspection could not complete within the bounded probe")
	case inspect.FailureMultiple:
		fmt.Fprintln(w, "inspect: more than one supported integration was found")
	default:
		fmt.Fprintln(w, "inspect: no supported integration was recognized")
	}
}

func isInspectUsageError(err error) bool {
	var failure *inspect.Failure
	return !errors.As(err, &failure)
}

func printMissingConfigSuggestion(w io.Writer) {
	fmt.Fprintln(w, `
To identify a supported application endpoint and print target YAML:
  statlite inspect <application-url>

To use an existing configuration:
  statlite --config /path/to/statlite.yaml`)
}
