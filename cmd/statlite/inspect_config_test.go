package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pvrlabs/statlite/internal/inspect"
)

func testInspector(_ context.Context, raw string) (*inspect.Result, error) {
	return &inspect.Result{TargetType: inspect.TargetSpring, Endpoint: raw + "/actuator", Capabilities: []string{"health"}}, nil
}

func TestInspectCreateConfigAndRefuseOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statlite.yaml")
	args := []string{"inspect", "http://example.test:8080", "--create-config", path}
	var out, errOut bytes.Buffer
	if code := runWithInspector(args, &out, &errOut, testInspector); code != 0 {
		t.Fatalf("create exit=%d stderr=%q", code, errOut.String())
	}
	created, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(created), "name: spring-example-test-8080") || !strings.Contains(out.String(), "statlite --config '") {
		t.Fatalf("created=%q stdout=%q", created, out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runWithInspector(args, &out, &errOut, testInspector); code == 0 || !strings.Contains(errOut.String(), "exist") {
		t.Fatalf("overwrite exit=%d stderr=%q", code, errOut.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(created, after) {
		t.Fatalf("config changed after refusal: %v", err)
	}
}

func TestInspectAddPreservesExistingBytesAndRefusesConflicts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		newline string
	}{
		{"LF without final newline", "server:\n  listen: 127.0.0.1:9090\nstorage:\n  sqlite_path: ${DB_PATH}\ntargets:\n  # existing\n  - name: old\n    type: spring\n    url: http://old.test/actuator", "\n"},
		{"CRLF", "server:\r\n  listen: 127.0.0.1:9090\r\ntargets:\r\n  - name: old\r\n    type: spring\r\n    url: http://old.test/actuator\r\n", "\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "statlite.yaml")
			if err := os.WriteFile(path, []byte(tc.content), 0o640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			var out, errOut bytes.Buffer
			args := []string{"inspect", "http://new.test:8080", "--add-to-config", path}
			if code := runWithInspector(args, &out, &errOut, testInspector); code != 0 {
				t.Fatalf("add exit=%d stderr=%q", code, errOut.String())
			}
			updated, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			prefix := tc.content
			if !strings.HasSuffix(prefix, "\n") {
				prefix += tc.newline
			}
			want := prefix + "  - name: spring-new-test-8080" + tc.newline + "    type: spring" + tc.newline + "    url: http://new.test:8080/actuator" + tc.newline
			if string(updated) != want {
				t.Fatalf("updated=%q\nwant=%q", updated, want)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, info) {
				t.Fatalf("append replaced file: before=%v after=%v", before, info)
			}
			out.Reset()
			errOut.Reset()
			if code := runWithInspector(args, &out, &errOut, testInspector); code == 0 || !strings.Contains(errOut.String(), "already exists") {
				t.Fatalf("duplicate exit=%d stderr=%q", code, errOut.String())
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(updated, after) {
				t.Fatalf("config changed after conflict: %v", err)
			}
		})
	}
}

func TestInspectAddRejectsYAMLDocumentTerminator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statlite.yaml")
	original := "targets:\n  - name: old\n    url: http://old.test/actuator\n... # end of document\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	args := []string{"inspect", "http://new.test", "--add-to-config", path}
	if code := runWithInspector(args, &out, &errOut, testInspector); code == 0 || !strings.Contains(errOut.String(), "document terminator") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != original {
		t.Fatalf("config changed after refusal: %v content=%q", err, after)
	}
}

func TestInspectAddRejectsUnsupportedLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statlite.yaml")
	original := "targets:\n  - name: old\n    url: http://old.test/actuator\nserver:\n  listen: 127.0.0.1:9090\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runWithInspector([]string{"inspect", "http://new.test", "--add-to-config", path}, &out, &errOut, testInspector); code == 0 || !strings.Contains(errOut.String(), "last top-level section") {
		t.Fatalf("exit=%d stderr=%q", code, errOut.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != original {
		t.Fatalf("config changed after refusal: %v", err)
	}
}

func TestInspectAddRejectsUncertainEnvironmentConflicts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
	}{
		{"name", "name: ${APP_NAME}\n    url: http://old.test/actuator"},
		{"endpoint", "name: old\n    url: $APP_URL"},
		{"legacy endpoint", "name: old\n    actuator_base_url: ${APP_URL}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "statlite.yaml")
			original := "targets:\n  - " + tc.target + "\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			var out, errOut bytes.Buffer
			args := []string{"inspect", "http://new.test", "--add-to-config", path}
			if code := runWithInspector(args, &out, &errOut, testInspector); code == 0 || !strings.Contains(errOut.String(), "cannot safely check duplicate") || !strings.Contains(errOut.String(), "add the target manually") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original {
				t.Fatalf("config changed after refusal: %v content=%q", err, after)
			}
		})
	}
}

func TestInspectAddRejectsEndpointConflictAndFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "actual.yaml")
	linkPath := filepath.Join(dir, "statlite.yaml")
	original := "targets:\n  - name: existing\n    type: spring\n    url: http://new.test/actuator\n"
	if err := os.WriteFile(realPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	conflict := func(_ context.Context, _ string) (*inspect.Result, error) {
		return &inspect.Result{TargetType: inspect.TargetSpring, Endpoint: "http://new.test:80/actuator"}, nil
	}
	args := []string{"inspect", "http://new.test", "--name", "different", "--add-to-config", linkPath}
	if code := runWithInspector(args, &out, &errOut, conflict); code == 0 || !strings.Contains(errOut.String(), "already configured") {
		t.Fatalf("conflict exit=%d stderr=%q", code, errOut.String())
	}
	after, err := os.ReadFile(realPath)
	if err != nil || string(after) != original {
		t.Fatalf("config changed after conflict: %v", err)
	}
	out.Reset()
	errOut.Reset()
	different := func(_ context.Context, _ string) (*inspect.Result, error) {
		return &inspect.Result{TargetType: inspect.TargetSpring, Endpoint: "http://another.test/actuator"}, nil
	}
	if code := runWithInspector(args, &out, &errOut, different); code != 0 {
		t.Fatalf("add exit=%d stderr=%q", code, errOut.String())
	}
	info, err := os.Lstat(linkPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config link replaced: %v", err)
	}
	after, err = os.ReadFile(realPath)
	if err != nil || !strings.Contains(string(after), "name: different") {
		t.Fatalf("linked config not updated: %v content=%q", err, after)
	}
}

func TestInspectFlagsAfterURLAndQuotedCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	typed := func(_ context.Context, kind inspect.TargetType, raw string) (*inspect.Result, error) {
		if kind != inspect.TargetQuarkus || raw != "http://example.test/q/metrics?scope=a&b=1" {
			t.Fatalf("typed inspect args: %q %q", kind, raw)
		}
		return &inspect.Result{TargetType: kind, Endpoint: raw}, nil
	}
	if code := runWithInspectors([]string{"inspect", "http://example.test/q/metrics?scope=a&b=1", "--type", "quarkus"}, &out, &errOut, testInspector, typed); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "'http://example.test/q/metrics?scope=a&b=1' --type 'quarkus' --create-config ./statlite.yaml") {
		t.Fatalf("command not shell quoted: %q", out.String())
	}
}

func TestInspectWriteFlagsRequirePathsAndAreMutuallyExclusive(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"create missing path", []string{"inspect", "http://app.test", "--create-config"}, "--create-config requires a value"},
		{"add missing path", []string{"inspect", "http://app.test", "--add-to-config"}, "--add-to-config requires a value"},
		{"create empty path", []string{"inspect", "http://app.test", "--create-config="}, "requires a destination path"},
		{"add empty path", []string{"inspect", "http://app.test", "--add-to-config="}, "requires a destination path"},
		{"both write modes", []string{"inspect", "http://app.test", "--create-config", "a.yaml", "--add-to-config", "b.yaml"}, "cannot be used together"},
		{"inspect config removed", []string{"inspect", "http://app.test", "--config", "a.yaml"}, "flag provided but not defined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			calls := 0
			inspector := func(_ context.Context, _ string) (*inspect.Result, error) {
				calls++
				return nil, nil
			}
			if code := runWithInspector(tc.args, &out, &errOut, inspector); code != 2 || calls != 0 || out.Len() != 0 || !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("exit=%d calls=%d stdout=%q stderr=%q", code, calls, out.String(), errOut.String())
			}
		})
	}
}

func TestInspectWriteOutputAndOptionOrders(t *testing.T) {
	dir := t.TempDir()
	createPath := filepath.Join(dir, "team's config.yaml")
	var out, errOut bytes.Buffer
	if code := runWithInspector([]string{"inspect", "http://app.test:8080", "--create-config", createPath}, &out, &errOut, testInspector); code != 0 {
		t.Fatalf("create exit=%d stderr=%q", code, errOut.String())
	}
	want := fmt.Sprintf("Wrote %s with target %q.\nNext: statlite --config %s\n", createPath, "spring-app-test-8080", shellQuote(createPath))
	if out.String() != want || !strings.Contains(out.String(), "'\"'\"'") {
		t.Fatalf("create stdout=%q want=%q", out.String(), want)
	}
	beforeURLPath := filepath.Join(dir, "created-before-url.yaml")
	out.Reset()
	errOut.Reset()
	if code := runWithInspector([]string{"inspect", "--create-config", beforeURLPath, "http://app.test:8080"}, &out, &errOut, testInspector); code != 0 {
		t.Fatalf("create before URL exit=%d stderr=%q", code, errOut.String())
	}
	if _, err := os.Stat(beforeURLPath); err != nil {
		t.Fatalf("create before URL did not write config: %v", err)
	}
	addPath := filepath.Join(dir, "existing config.yaml")
	if err := os.WriteFile(addPath, []byte("targets:\n  - name: old\n    url: http://old.test/actuator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runWithInspector([]string{"inspect", "--add-to-config", addPath, "http://app.test:8080"}, &out, &errOut, testInspector); code != 0 {
		t.Fatalf("add exit=%d stderr=%q", code, errOut.String())
	}
	want = fmt.Sprintf("Wrote %s with target %q.\nRestart StatLite to load the new target.\nNext: statlite --config %s\n", addPath, "spring-app-test-8080", shellQuote(addPath))
	if out.String() != want {
		t.Fatalf("add stdout=%q want=%q", out.String(), want)
	}
}

func TestInspectSuggestedCommandsCarryTypeAndCustomName(t *testing.T) {
	var out, errOut bytes.Buffer
	endpoint := "http://app.test/q/metrics?scope=a&b=1"
	typed := func(_ context.Context, _ inspect.TargetType, _ string) (*inspect.Result, error) {
		return &inspect.Result{TargetType: inspect.TargetQuarkus, Endpoint: endpoint}, nil
	}
	if code := runWithInspectors([]string{"inspect", endpoint, "--type", "quarkus", "--name", "orders west"}, &out, &errOut, testInspector, typed); code != 0 {
		t.Fatalf("inspect exit=%d stderr=%q", code, errOut.String())
	}
	for _, mode := range []string{"--create-config", "--add-to-config"} {
		want := "statlite inspect '" + endpoint + "' --type 'quarkus' --name 'orders west' " + mode + " ./statlite.yaml"
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing command %q in %q", want, out.String())
		}
	}
}
