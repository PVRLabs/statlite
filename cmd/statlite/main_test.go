package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/inspect"
	"github.com/pvrlabs/statlite/internal/version"
)

func TestEntrypointRejectsRetiredTargetTypesClearly(t *testing.T) {
	for _, retiredType := range []string{"statlite-health", "host"} {
		t.Run(retiredType, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "statlite.yaml")
			config := `server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "obsolete-self"
    type: "` + retiredType + `"
    url: "http://127.0.0.1:9090/healthz"
`
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			cmd := exec.Command("go", "run", ".", "--config", configPath)
			cmd.Dir = "."
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("go run succeeded for retired type %q; output=%s", retiredType, output)
			}
			if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() == 0 {
				t.Fatalf("go run error = %v, want clean non-zero exit; output=%s", err, output)
			}
			message := string(output)
			for _, want := range []string{
				`targets[0].type`,
				`unsupported type`,
				`"` + retiredType + `"`,
				`spring`,
				`statlite-metrics`,
			} {
				if !strings.Contains(message, want) {
					t.Fatalf("startup output = %q, missing %q", message, want)
				}
			}
			for _, forbidden := range []string{"panic:", "goroutine", "runtime error", "stack trace"} {
				if strings.Contains(strings.ToLower(message), forbidden) {
					t.Fatalf("startup output = %q, contains unexpected crash text %q", message, forbidden)
				}
			}
		})
	}
}

func TestPrintVersion(t *testing.T) {
	var out bytes.Buffer
	printVersion(&out)

	got := out.String()
	want := "statlite " + version.Version + "\n"
	if got != want {
		t.Fatalf("printVersion() = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "statlite v") {
		t.Fatalf("printVersion() = %q, want leading statlite v", got)
	}
}

func TestStartupMessage(t *testing.T) {
	got := startupMessage("0.0.0.0:9090", 3)
	want := "StatLite starting: version=" + version.Version + " listen=0.0.0.0:9090 targets=3"
	if got != want {
		t.Fatalf("startupMessage() = %q, want %q", got, want)
	}
}

func TestPrintHelp(t *testing.T) {
	var out bytes.Buffer
	printHelp(&out)

	got := out.String()
	for _, want := range []string{
		"StatLite - tiny self-hosted metrics dashboard for small servers.",
		"Spring Boot Actuator",
		"Usage:",
		"statlite [--config path]",
		"--version",
		"--help",
		"Docs: README.md, docs/configuration.md",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("printHelp() missing %q\n%s", want, got)
		}
	}
}

func TestRunPreservesTopLevelHelpAndVersion(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"--help"}, want: "statlite inspect <application-url>"},
		{name: "version", args: []string{"--version"}, want: "statlite " + version.Version},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != 0 {
				t.Fatalf("run() exit code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			output := stdout.String() + stderr.String()
			if !strings.Contains(output, tt.want) {
				t.Fatalf("output = %q, want %q", output, tt.want)
			}
		})
	}
}

func TestRunInspectDispatchesWithoutLoadingConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var inspectedURL string
	code := runWithInspector([]string{"inspect", "http://app.test"}, &stdout, &stderr,
		func(_ context.Context, rawURL string) (*inspect.Result, error) {
			inspectedURL = rawURL
			return &inspect.Result{
				TargetType:   inspect.TargetSpring,
				Endpoint:     rawURL + "/actuator",
				Capabilities: []string{"health", "metrics"},
			}, nil
		})
	if code != 0 {
		t.Fatalf("run() exit code = %d, stderr=%q", code, stderr.String())
	}
	if inspectedURL != "http://app.test" {
		t.Fatalf("inspected URL = %q", inspectedURL)
	}
	output := stdout.String()
	if !strings.Contains(output, "Detected: Spring Boot Actuator") {
		t.Fatalf("stdout = %q, want inspection result", stdout.String())
	}
	if !strings.Contains(output, "----- BEGIN statlite.yaml -----") || !strings.Contains(output, "url: http://app.test/actuator") || !strings.Contains(output, "New setup: save only the YAML between the markers as statlite.yaml.") {
		t.Fatalf("stdout = %q, want suggested YAML and next step", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunTypedInspectDispatchesOnlyToRequestedTarget(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var untypedCalls, typedCalls int
	code := runWithInspectors([]string{"inspect", "--type", "quarkus", "http://app.test/q/metrics?scope=app"}, &stdout, &stderr,
		func(context.Context, string) (*inspect.Result, error) {
			untypedCalls++
			return nil, errors.New("untyped inspector must not run")
		},
		func(_ context.Context, targetType inspect.TargetType, endpoint string) (*inspect.Result, error) {
			typedCalls++
			if targetType != inspect.TargetQuarkus || endpoint != "http://app.test/q/metrics?scope=app" {
				t.Fatalf("typed inspection arguments = %q, %q", targetType, endpoint)
			}
			return &inspect.Result{TargetType: inspect.TargetQuarkus, Endpoint: endpoint, Status: inspect.CompatibilityPartial, Capabilities: []string{"jvm_heap_used_bytes"}}, nil
		})
	if code != 0 || untypedCalls != 0 || typedCalls != 1 {
		t.Fatalf("code=%d untyped=%d typed=%d stdout=%q stderr=%q", code, untypedCalls, typedCalls, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Detected: Quarkus Metrics") || !strings.Contains(stdout.String(), "type: quarkus") || !strings.Contains(stdout.String(), "Compatibility: partial") {
		t.Fatalf("stdout = %q, want typed Quarkus output", stdout.String())
	}
}

func TestRenderInspectionSpringOutputAndConfigRoundTrip(t *testing.T) {
	result := &inspect.Result{
		TargetType:   inspect.TargetSpring,
		Endpoint:     "https://example.test/service/actuator",
		Capabilities: []string{"health", "metrics"},
	}

	got, err := renderInspection(result)
	if err != nil {
		t.Fatalf("renderInspection() error = %v", err)
	}
	want := `Detected: Spring Boot Actuator

Endpoint:
  https://example.test/service/actuator

Available:
  ✓ health
  ✓ metrics

Suggested statlite.yaml:

----- BEGIN statlite.yaml -----
server:
    listen: 127.0.0.1:9090
storage:
    sqlite_path: ./statlite.sqlite
polling:
    interval: 30s
targets:
    - name: app
      type: spring
      url: https://example.test/service/actuator
----- END statlite.yaml -----

Next:
  New setup: save only the YAML between the markers as statlite.yaml.
  Existing setup: add the target entry to your existing targets list, changing name if needed.

Then run:
  statlite

Open:
  http://127.0.0.1:9090

More configuration options:
  https://github.com/PVRLabs/statlite/blob/main/docs/configuration.md
`
	if got != want {
		t.Fatalf("renderInspection() = %q, want %q", got, want)
	}
	assertSuggestedConfigLoads(t, got, config.TargetTypeSpring, "https://example.test/service/actuator")
}

func TestRenderInspectionStatliteMetricsOutputOmitsIrrelevantFields(t *testing.T) {
	result := &inspect.Result{
		TargetType:   inspect.TargetStatliteMetrics,
		Endpoint:     "http://localhost:9090/statlite/metrics",
		Capabilities: []string{"health"},
		Warnings:     []string{"metrics are unavailable"},
	}

	got, err := renderInspection(result)
	if err != nil {
		t.Fatalf("renderInspection() error = %v", err)
	}
	for _, want := range []string{
		"Detected: StatLite Metrics v1",
		"type: statlite-metrics",
		"url: http://localhost:9090/statlite/metrics",
		"Warning: metrics are unavailable",
		"Suggested statlite.yaml:",
		"server:",
		"storage:",
		"polling:",
		"----- BEGIN statlite.yaml -----",
		"----- END statlite.yaml -----",
		"New setup: save only the YAML between the markers as statlite.yaml.",
		"Existing setup: add the target entry to your existing targets list, changing name if needed.",
		"More configuration options:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("renderInspection() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "actuator_base_url") {
		t.Fatalf("renderInspection() = %q, must omit Spring-only field", got)
	}
	assertSuggestedConfigLoads(t, got, config.TargetTypeStatliteMetrics, "http://localhost:9090/statlite/metrics")
}

func TestRenderInspectionQuarkusOutputRoundTripsExactEndpoint(t *testing.T) {
	result := &inspect.Result{
		TargetType:   inspect.TargetQuarkus,
		Endpoint:     "http://localhost:9000/q/metrics/?scope=app",
		Status:       inspect.CompatibilityCompatible,
		Capabilities: []string{"process_cpu_usage", "jvm_heap_used_bytes"},
	}

	got, err := renderInspection(result)
	if err != nil {
		t.Fatalf("renderInspection() error = %v", err)
	}
	for _, want := range []string{
		"Detected: Quarkus Metrics",
		"Compatibility: compatible",
		"type: quarkus",
		"url: http://localhost:9000/q/metrics/?scope=app",
		"process_cpu_usage",
		"jvm_heap_used_bytes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("renderInspection() = %q, missing %q", got, want)
		}
	}
	assertSuggestedConfigLoads(t, got, config.TargetTypeQuarkus, "http://localhost:9000/q/metrics/?scope=app")
}

func TestRunInspectFailuresUseExitOneAndPrintNoYAML(t *testing.T) {
	for _, kind := range []inspect.FailureKind{
		inspect.FailureAuthRequired,
		inspect.FailureUnreachable,
		inspect.FailureIncomplete,
		inspect.FailureMultiple,
		inspect.FailureUnrecognized,
	} {
		t.Run(string(kind), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithInspector([]string{"inspect", "http://app.test"}, &stdout, &stderr,
				func(context.Context, string) (*inspect.Result, error) {
					return nil, &inspect.Failure{Kind: kind}
				})
			if code != 1 {
				t.Fatalf("run() exit code = %d, want 1", code)
			}
			if stdout.Len() != 0 || strings.Contains(stderr.String(), "targets:") {
				t.Fatalf("stdout=%q stderr=%q, want failure without YAML", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunInspectTypeErrorsUseAccurateUsageMessages(t *testing.T) {
	tests := []struct {
		targetType string
		want       string
	}{
		{targetType: "prometheus", want: `unsupported inspection type "prometheus" (supported: quarkus)`},
		{targetType: "spring", want: `typed inspection type "spring" is not available (supported: quarkus)`},
	}
	for _, tt := range tests {
		t.Run(tt.targetType, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"inspect", "--type", tt.targetType, "http://app.test"}, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("run() exit code = %d, want 2; stderr=%q", code, stderr.String())
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.want) || strings.Contains(stderr.String(), "invalid application URL") {
				t.Fatalf("stdout=%q stderr=%q, want accurate type usage error", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRenderInspectionRejectsInvalidSuggestedConfig(t *testing.T) {
	_, err := renderInspection(&inspect.Result{TargetType: inspect.TargetSpring})
	if err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("renderInspection() error = %v, want config validation error", err)
	}
}

func assertSuggestedConfigLoads(t *testing.T, output string, targetType, endpoint string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "statlite.yaml")
	configStart := strings.Index(output, "server:\n")
	if configStart < 0 {
		t.Fatalf("output = %q, missing config YAML boundaries", output)
	}
	configEnd := strings.Index(output[configStart:], "----- END statlite.yaml -----")
	if configEnd < 0 {
		t.Fatalf("output = %q, missing config YAML end", output)
	}
	configText := output[configStart : configStart+configEnd]
	if err := os.WriteFile(path, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load() error = %v; config=%q", err, configText)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].Type != targetType {
		t.Fatalf("targets = %#v, want one %q target", cfg.Targets, targetType)
	}
	gotEndpoint := cfg.Targets[0].URL
	if targetType == config.TargetTypeSpring {
		gotEndpoint = cfg.Targets[0].URL
	}
	if gotEndpoint != endpoint {
		t.Fatalf("endpoint = %q, want %q", gotEndpoint, endpoint)
	}
}

func TestRunInspectUsageAndURLErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing URL", args: []string{"inspect"}},
		{name: "extra URL", args: []string{"inspect", "http://one.test", "http://two.test"}},
		{name: "invalid URL", args: []string{"inspect", "ftp://app.test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != 2 {
				t.Fatalf("run() exit code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if stderr.Len() == 0 {
				t.Fatal("stderr is empty, want usage error")
			}
		})
	}
}

func TestRunInspectHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"inspect", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"statlite inspect <application-url>",
		"statlite inspect 'http://localhost:8080'",
		"Quote the URL when pasting it from a browser",
		"remove any query string or fragment first",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want inspect help containing %q", stdout.String(), want)
		}
	}
}

func TestRunMissingImplicitConfigSuggestsInspect(t *testing.T) {
	workingDir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 1 {
		t.Fatalf("run() exit code = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "statlite inspect <application-url>") {
		t.Fatalf("stderr = %q, want missing-config inspect suggestion", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(workingDir, "statlite.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("statlite.sqlite stat error = %v, want file to remain absent", err)
	}
}

func TestRunExplicitAndMalformedConfigDoNotSuggestInspect(t *testing.T) {
	workingDir := t.TempDir()
	missing := filepath.Join(workingDir, "missing.yaml")
	malformed := filepath.Join(workingDir, "malformed.yaml")
	if err := os.WriteFile(malformed, []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{missing, malformed} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{"--config", path}, &stdout, &stderr); code != 1 {
				t.Fatalf("run() exit code = %d, want 1; stderr=%q", code, stderr.String())
			}
			if strings.Contains(stderr.String(), "statlite inspect <application-url>") {
				t.Fatalf("stderr = %q, explicit config must not get inspect suggestion", stderr.String())
			}
		})
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", "statlite.yaml"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run() exit code = %d, want 1; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "statlite inspect <application-url>") {
		t.Fatalf("stderr = %q, explicitly supplied default config must not get inspect suggestion", stderr.String())
	}
}
