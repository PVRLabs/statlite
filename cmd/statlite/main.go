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
	"path/filepath"
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
	return runWithInspectors(args, stdout, stderr, inspect.Application, inspect.Inspect)
}

type inspectApplicationFunc func(context.Context, string) (*inspect.Result, error)
type inspectApplicationByTypeFunc func(context.Context, inspect.TargetType, string) (*inspect.Result, error)

func runWithInspector(args []string, stdout, stderr io.Writer, inspectApplication inspectApplicationFunc) int {
	return runWithInspectors(args, stdout, stderr, inspectApplication, inspect.Inspect)
}

func runWithInspectors(args []string, stdout, stderr io.Writer, inspectApplication inspectApplicationFunc, inspectByType inspectApplicationByTypeFunc) int {
	if len(args) > 0 && args[0] == "inspect" {
		return runInspectWithTyped(args[1:], stdout, stderr, inspectApplication, inspectByType)
	}
	return runMonitor(args, stdout, stderr)
}

func runInspectWithTyped(args []string, stdout, stderr io.Writer, inspectApplication inspectApplicationFunc, inspectByType inspectApplicationByTypeFunc) int {
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
	typed := inspectFlags.String("type", "", "inspect a specific target type (currently: quarkus)")
	name := inspectFlags.String("name", "", "target name (default: derived from type, host, and port)")
	createPath := inspectFlags.String("create-config", "", "create a new configuration file at PATH")
	addPath := inspectFlags.String("add-to-config", "", "append a target to an existing configuration file at PATH")
	reordered, err := reorderInspectArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "inspect: %v\n", err)
		return 2
	}
	if err := inspectFlags.Parse(reordered); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	var createSet, addSet bool
	inspectFlags.Visit(func(option *flag.Flag) {
		switch option.Name {
		case "create-config":
			createSet = true
		case "add-to-config":
			addSet = true
		}
	})
	if createSet && addSet {
		fmt.Fprintln(stderr, "inspect: --create-config and --add-to-config cannot be used together")
		return 2
	}
	if (createSet && *createPath == "") || (addSet && *addPath == "") {
		fmt.Fprintln(stderr, "inspect: write operation requires a destination path")
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

	var result *inspect.Result
	if *typed == "" {
		result, err = inspectApplication(context.Background(), inspectFlags.Arg(0))
	} else {
		result, err = inspectByType(context.Background(), inspect.TargetType(*typed), inspectFlags.Arg(0))
	}
	if err != nil {
		printInspectFailure(stderr, err)
		if isInspectUsageError(err) {
			return 2
		}
		return 1
	}
	target, presentation, err := suggestedInspectionTarget(result, *name)
	if err != nil {
		fmt.Fprintf(stderr, "inspect: could not render suggested configuration: %v\n", err)
		return 1
	}
	if createSet || addSet {
		path := *createPath
		if createSet {
			err = createInspectionConfig(path, target, presentation.errorContext)
		} else {
			path = *addPath
			err = addInspectionTarget(path, target)
		}
		if err != nil {
			fmt.Fprintf(stderr, "inspect: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Wrote %s with target %q.\n", path, target.Name)
		if addSet {
			fmt.Fprintln(stdout, "Restart StatLite to load the new target.")
		}
		fmt.Fprintf(stdout, "Next: %s\n", monitorCommand(path))
		return 0
	}
	output, err := renderInspectionWithOptions(result, target, presentation, inspectFlags.Arg(0), *typed)
	if err != nil {
		fmt.Fprintf(stderr, "inspect: could not render suggested configuration: %v\n", err)
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
	noPoll := monitorFlags.Bool("no-poll", false, "serve stored data without polling targets")
	rawSeries := monitorFlags.Bool("raw-series", false, "serve full-resolution series within the effective range without dashboard sampling or aggregation")
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
		writeConfigFailure(stderr, err)
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
	identities := make([]storage.TargetIdentity, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		identities = append(identities, storage.TargetIdentity{Name: target.Name, Type: target.Type})
	}
	if err := store.RegisterTargets(ctx, identities); err != nil {
		logger.Printf("storage: %v", err)
		return 1
	}
	retentionCutoff := storage.NewRetentionCutoffTracker(cfg.Storage.RetentionDays)

	manager, err := app.NewMonitorManager(cfg.Targets, store, timeout, interval)
	if err != nil {
		logger.Printf("monitor manager: %v", err)
		return 1
	}
	var dashboardNow *time.Time
	if *noPoll {
		if dashboardNow, err = manager.EnableNoPoll(ctx); err != nil {
			logger.Printf("monitor manager: %v", err)
			return 1
		}
		for _, message := range noPollStartupMessages(dashboardNow != nil) {
			logger.Print(message)
		}
	}

	serverRetentionDays := cfg.Storage.RetentionDays
	if *noPoll {
		serverRetentionDays = 0
	}
	srv := server.NewWithManagerRetentionCutoffAndFilesystem(cfg.Server.Listen, manager, serverRetentionDays, retentionCutoff.Current, cfg.Storage.SQLitePath)
	srv.SetRawSeries(*rawSeries)
	if dashboardNow != nil {
		srv.FreezeDashboardTime(*dashboardNow)
	}
	listener, err := srv.Listen()
	if err != nil {
		logger.Printf("server: %v", err)
		return 1
	}
	logger.Print(startupMessage(cfg.Server.Listen, len(manager.Names()), cfg.Storage.SQLitePath))

	// The listener is bound before starting monitor goroutines so the first
	// poll can reach StatLite's own metrics endpoint. Keep cleanup ahead of
	// serving HTTP so requests cannot observe partial startup initialization.
	// Prune before starting monitor goroutines so the first poll only sees retained history.
	if !*noPoll {
		storage.StartRetentionCleanup(ctx, store, cfg.Storage.RetentionDays, retentionCutoff.Set)
	}

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

	if !*noPoll {
		manager.Start(ctx)
	}

	if err := <-serveErr; err != nil {
		logger.Printf("server: %v", err)
		return 1
	}
	return 0
}

func writeConfigFailure(w io.Writer, err error) {
	var validation *config.TargetValidationError
	if !errors.As(err, &validation) {
		fmt.Fprintf(w, "config: %v\n", err)
		return
	}
	fmt.Fprintln(w, validation.Error())
	fmt.Fprintf(w, "\nConfiguration documentation:\n  %s\n", configurationDocsURL)
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

func startupMessage(listen string, targets int, sqlitePath string) string {
	databasePath, err := filepath.Abs(sqlitePath)
	if err != nil {
		databasePath = filepath.Clean(sqlitePath)
	}
	return fmt.Sprintf("StatLite starting: version=%s listen=%s targets=%d database=%q", version.Version, listen, targets, databasePath)
}

func noPollStartupMessages(hasStoredPoll bool) []string {
	messages := []string{"Polling disabled (--no-poll); serving stored data only"}
	if !hasStoredPoll {
		messages = append(messages, "WARNING: polling disabled (--no-poll), but the database contains no stored polls; the dashboard will be empty")
	}
	return messages
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `StatLite - tiny self-hosted metrics dashboard for small servers.

Polls Spring Boot Actuator and StatLite self-monitoring endpoints, stores
samples in local SQLite, and serves a localhost dashboard.

Usage:
  statlite [--config path] [--no-poll] [--raw-series]
  statlite inspect <application-url>
  statlite inspect --type quarkus <application-or-metrics-url>
  statlite inspect <application-url> --create-config PATH
  statlite inspect <application-url> --add-to-config PATH
  statlite --version
  statlite --help

Options:
  --config path   Config file (default: statlite.yaml)
  --no-poll       Serve stored data without polling targets
  --raw-series    Serve full-resolution series within the effective range
  --version       Print version and exit
  --help          Show this help

Docs: README.md, docs/configuration.md
`)
}

func printInspectHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  statlite inspect [options] <application-url>

Example:
  statlite inspect 'http://localhost:8080'
  statlite inspect 'http://localhost:8080' --create-config ./statlite.yaml
  statlite inspect 'http://localhost:8080' --add-to-config ./statlite.yaml

Probe a supported application endpoint and print an inspection summary.
Plain inspection is read-only and does not require or create statlite.yaml.
--create-config PATH writes a new config only if PATH does not exist.
--add-to-config PATH appends a target to an existing config whose last section is
a normal targets list. Existing names and endpoints cause an error.
--name overrides the stable type-host-port name. Options may appear before
or after the URL.

Use --type quarkus with a Quarkus application URL or an exact customized
Prometheus/OpenMetrics endpoint. A base URL uses the conventional /q/metrics path.

Quote the URL when pasting it from a browser, especially if it contains ? or &.
Untyped inspection requires a base URL, so remove any query string or fragment first.
Typed Quarkus inspection accepts a base URL or exact metrics endpoint URL.`)
}

const configurationDocsURL = "https://github.com/PVRLabs/statlite/blob/main/docs/configuration.md"

type suggestedServerConfig struct {
	Listen string `yaml:"listen"`
}

type suggestedStorageConfig struct {
	SQLitePath string `yaml:"sqlite_path"`
}

type suggestedPollingConfig struct {
	Interval string `yaml:"interval"`
}

type suggestedTarget struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	URL  string `yaml:"url,omitempty"`
}

type suggestedConfig struct {
	Server  suggestedServerConfig  `yaml:"server"`
	Storage suggestedStorageConfig `yaml:"storage"`
	Polling suggestedPollingConfig `yaml:"polling"`
	Targets []suggestedTarget      `yaml:"targets"`
}

type inspectionTargetPresentation struct {
	displayName  string
	targetType   string
	errorContext string
}

func renderInspection(result *inspect.Result) (string, error) {
	target, presentation, err := suggestedInspectionTarget(result, "")
	if err != nil {
		return "", err
	}
	return renderInspectionWithOptions(result, target, presentation, result.Endpoint, "")
}

func renderInspectionWithOptions(result *inspect.Result, target suggestedTarget, presentation inspectionTargetPresentation, rawURL, typed string) (string, error) {
	if result == nil {
		return "", errors.New("inspection returned no result")
	}
	configYAML, err := renderSuggestedTargetConfig(target, presentation.errorContext)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Detected: %s\n\nEndpoint:\n  %s\n", presentation.displayName, result.Endpoint)
	if result.Status != "" {
		fmt.Fprintf(&output, "\nCompatibility: %s\n", result.Status)
	}
	fmt.Fprint(&output, "\nAvailable:\n")
	for _, capability := range result.Capabilities {
		fmt.Fprintf(&output, "  ✓ %s\n", capability)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(&output, "\nWarning: %s\n", warning)
	}
	fmt.Fprintf(&output, "\nSuggested statlite.yaml:\n\n----- BEGIN statlite.yaml -----\n%s----- END statlite.yaml -----\n\nNext:\n  Create config: %s\n  Add target: %s\n\nThen run:\n  %s\n\nOpen:\n  http://127.0.0.1:9090\n\nMore configuration options:\n  %s\n", configYAML, inspectWriteCommand(rawURL, result.Endpoint, typed, target, "--create-config"), inspectWriteCommand(rawURL, result.Endpoint, typed, target, "--add-to-config"), monitorCommand("./statlite.yaml"), configurationDocsURL)
	return output.String(), nil
}

func inspectionPresentation(targetType inspect.TargetType) (inspectionTargetPresentation, error) {
	switch targetType {
	case inspect.TargetSpring:
		return inspectionTargetPresentation{
			displayName:  "Spring Boot Actuator",
			targetType:   config.TargetTypeSpring,
			errorContext: "spring target",
		}, nil
	case inspect.TargetStatliteMetrics:
		return inspectionTargetPresentation{
			displayName:  "StatLite Metrics v1",
			targetType:   config.TargetTypeStatliteMetrics,
			errorContext: "statlite-metrics target",
		}, nil
	case inspect.TargetQuarkus:
		return inspectionTargetPresentation{
			displayName:  "Quarkus Metrics",
			targetType:   config.TargetTypeQuarkus,
			errorContext: "quarkus target",
		}, nil
	default:
		return inspectionTargetPresentation{}, fmt.Errorf("unsupported inspection target type %q", targetType)
	}
}

func renderSuggestedTargetConfig(target suggestedTarget, errorContext string) (string, error) {
	cfg := suggestedConfig{
		Server:  suggestedServerConfig{Listen: "127.0.0.1:9090"},
		Storage: suggestedStorageConfig{SQLitePath: "./statlite.sqlite"},
		Polling: suggestedPollingConfig{Interval: "30s"},
		Targets: []suggestedTarget{target},
	}
	validation := config.Config{
		Server:  config.ServerConfig{Listen: "127.0.0.1:9090"},
		Storage: config.StorageConfig{SQLitePath: "./statlite.sqlite"},
		Polling: config.PollingConfig{Interval: "30s"},
		Targets: []config.TargetConfig{{Name: target.Name, Type: target.Type, URL: target.URL}},
	}
	if err := config.Validate(&validation); err != nil {
		return "", fmt.Errorf("%s: %w", errorContext, err)
	}
	return marshalSuggestedConfig(cfg)
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
	case inspect.FailureIncompatible:
		fmt.Fprintf(w, "inspect: the configured Quarkus metrics endpoint is incompatible: %v\n", failure.Err)
	case inspect.FailureTypeUnsupported, inspect.FailureTypeUnavailable:
		fmt.Fprintf(w, "inspect: %v\n", failure)
	default:
		fmt.Fprintln(w, "inspect: no supported integration was recognized")
	}
}

func isInspectUsageError(err error) bool {
	var failure *inspect.Failure
	if !errors.As(err, &failure) {
		return true
	}
	return failure.Kind == inspect.FailureTypeUnsupported || failure.Kind == inspect.FailureTypeUnavailable
}

func printMissingConfigSuggestion(w io.Writer) {
	fmt.Fprintln(w, `
To use an existing configuration:
  statlite --config /path/to/statlite.yaml

Configuration documentation:
  `+configurationDocsURL)
}
