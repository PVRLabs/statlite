package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	if !strings.Contains(stdout.String(), "Detected: Spring Boot Actuator") {
		t.Fatalf("stdout = %q, want inspection result", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
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
	if !strings.Contains(stdout.String(), "statlite inspect <application-url>") {
		t.Fatalf("stdout = %q, want inspect help", stdout.String())
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
