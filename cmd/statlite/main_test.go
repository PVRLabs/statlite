package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pvrlabs/statlite/internal/version"
)

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
