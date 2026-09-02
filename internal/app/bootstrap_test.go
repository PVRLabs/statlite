package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
)

func TestNewCollectorBuildsConfiguredTargetTypes(t *testing.T) {
	tests := []struct {
		name       string
		target     config.TargetConfig
		wantType   string
		wantTarget string
	}{
		{
			name:       "quarkus",
			target:     config.TargetConfig{Name: "orders", Type: config.TargetTypeQuarkus, URL: "https://example.com/q/metrics"},
			wantType:   "quarkus",
			wantTarget: "*collector.QuarkusCollector",
		},
		{
			name:       "default spring",
			target:     config.TargetConfig{Name: "spring", URL: "https://example.com/actuator"},
			wantType:   "spring",
			wantTarget: "*collector.SpringActuatorCollector",
		},
		{
			name:       "explicit spring",
			target:     config.TargetConfig{Name: "spring", Type: config.TargetTypeSpring, URL: "https://example.com/actuator"},
			wantType:   "spring",
			wantTarget: "*collector.SpringActuatorCollector",
		},
		{
			name:       "statlite metrics",
			target:     config.TargetConfig{Name: "metrics", Type: config.TargetTypeStatliteMetrics, URL: "https://example.com/statlite/metrics"},
			wantType:   "statlite metrics",
			wantTarget: "*collector.StatliteMetricsCollector",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newCollector(tt.target, time.Second)
			if err != nil {
				t.Fatalf("newCollector(%s) error = %v", tt.wantType, err)
			}
			if gotType := typeName(got); gotType != tt.wantTarget {
				t.Fatalf("newCollector(%s) type = %s, want %s", tt.wantType, gotType, tt.wantTarget)
			}
		})
	}
}

func TestNewCollectorRejectsInvalidStatliteMetricsURL(t *testing.T) {
	_, err := newCollector(config.TargetConfig{
		Name: "metrics",
		Type: config.TargetTypeStatliteMetrics,
		URL:  "ftp://example.com/metrics",
	}, time.Second)
	if err == nil {
		t.Fatal("newCollector() error = nil, want invalid URL error")
	}
	if !strings.Contains(err.Error(), "statlite metrics client") || !strings.Contains(err.Error(), "must use http or https") {
		t.Fatalf("newCollector() error = %q, want statlite metrics URL context", err)
	}
}

func TestNewCollectorDerivesQuarkusHealthEndpointAndSharesAuth(t *testing.T) {
	var metricsRequests, healthRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "user" || password != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/q/metrics":
			metricsRequests++
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			_, _ = w.Write([]byte("process_cpu_usage 0.25\n"))
		case "/q/health":
			healthRequests++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"UP","checks":[{"name":"Database connections health check","status":"UP"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	targetCollector, err := newCollector(config.TargetConfig{
		Name: "orders",
		Type: config.TargetTypeQuarkus,
		URL:  server.URL + "/q/metrics",
		Auth: &config.AuthConfig{Type: "basic", Username: "user", Password: "secret"},
	}, time.Second)
	if err != nil {
		t.Fatalf("newCollector() error = %v", err)
	}
	result, err := targetCollector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if metricsRequests != 1 || healthRequests != 1 {
		t.Fatalf("requests = metrics:%d health:%d, want 1/1", metricsRequests, healthRequests)
	}
	if result.HealthStatus != "UP" || result.DBHealthStatus != "UP" {
		t.Fatalf("health = %q/%q, want UP/UP", result.HealthStatus, result.DBHealthStatus)
	}
}

func TestNewCollectorAllowsCustomQuarkusMetricsEndpointWithoutHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manage/prom" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("process_cpu_usage 0.25\n"))
	}))
	defer server.Close()

	targetCollector, err := newCollector(config.TargetConfig{
		Name: "orders",
		Type: config.TargetTypeQuarkus,
		URL:  server.URL + "/manage/prom",
	}, time.Second)
	if err != nil {
		t.Fatalf("newCollector() error = %v", err)
	}
	result, err := targetCollector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(result.Samples) != 1 || result.HealthStatus != "UP" || len(result.Events) != 0 {
		t.Fatalf("result = %#v, want metrics-only custom endpoint reachability UP", result)
	}
}

func TestNewCollectorUsesExplicitQuarkusHealthURL(t *testing.T) {
	var healthRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "user" || password != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/manage/prom":
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			_, _ = w.Write([]byte("process_cpu_usage 0.25\n"))
		case "/manage/health":
			healthRequests++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"UP","checks":[{"name":"Database connections health check","status":"UP"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	targetCollector, err := newCollector(config.TargetConfig{
		Name:      "orders",
		Type:      config.TargetTypeQuarkus,
		URL:       server.URL + "/manage/prom",
		HealthURL: server.URL + "/manage/health",
		Auth:      &config.AuthConfig{Type: "basic", Username: "user", Password: "secret"},
	}, time.Second)
	if err != nil {
		t.Fatalf("newCollector() error = %v", err)
	}
	result, err := targetCollector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if healthRequests != 1 || result.HealthStatus != "UP" || result.DBHealthStatus != "UP" {
		t.Fatalf("health requests/status = %d %q/%q, want 1 UP/UP", healthRequests, result.HealthStatus, result.DBHealthStatus)
	}
}

func TestNewCollectorRejectsUnsupportedTargetType(t *testing.T) {
	_, err := newCollector(config.TargetConfig{Name: "unknown", Type: "unknown"}, time.Second)
	if err == nil {
		t.Fatal("newCollector() error = nil, want unsupported type error")
	}
	if !strings.Contains(err.Error(), `unsupported target type "unknown"`) {
		t.Fatalf("newCollector() error = %q, want unsupported type context", err)
	}
}

func TestNewCollectorPassesTimeoutToStatliteMetricsClient(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-releaseResponse
		_, _ = w.Write([]byte(`{"schema":"statlite-metrics/v1","status":"UP"}`))
	}))
	defer server.Close()
	defer close(releaseResponse)

	targetCollector, err := newCollector(config.TargetConfig{
		Name: "metrics",
		Type: config.TargetTypeStatliteMetrics,
		URL:  server.URL,
	}, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("newCollector() error = %v", err)
	}
	metricsCollector, ok := targetCollector.(*collector.StatliteMetricsCollector)
	if !ok {
		t.Fatalf("newCollector() type = %T, want *collector.StatliteMetricsCollector", targetCollector)
	}

	result := make(chan error, 1)
	go func() {
		_, err := metricsCollector.Collect(context.Background())
		result <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("collector did not make its request")
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "Client.Timeout") {
			t.Fatalf("Collect() error = %v, want configured timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Collect() did not honor the configured timeout")
	}
}

func TestNewCollectorLegacyActuatorUserinfoUsesActuatorWithoutPrometheusProbe(t *testing.T) {
	var prometheusRequests, actuatorRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "user" || password != "secret" {
			t.Errorf("basic auth = %q/%q/%v, want user/secret/true", user, password, ok)
		}
		switch r.URL.Path {
		case "/actuator/health":
			fmt.Fprint(w, `{"status":"UP"}`)
		case "/actuator/prometheus":
			prometheusRequests++
			http.Error(w, "Prometheus must not be probed", http.StatusInternalServerError)
		default:
			actuatorRequests++
			fmt.Fprint(w, `{"name":"metric","measurements":[{"statistic":"COUNT","value":1},{"statistic":"TOTAL_TIME","value":0}]}`)
		}
	}))
	defer server.Close()

	base := "http://user:secret@" + strings.TrimPrefix(server.URL, "http://") + "/actuator"
	cfg := loadAppTestConfig(t, fmt.Sprintf("actuator_base_url: %q", base))
	targetCollector, err := newCollector(cfg.Targets[0], time.Second)
	if err != nil {
		t.Fatalf("newCollector() error = %v", err)
	}
	if _, err := targetCollector.Collect(context.Background()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if prometheusRequests != 0 {
		t.Fatalf("prometheus requests = %d, want 0", prometheusRequests)
	}
	if actuatorRequests == 0 {
		t.Fatal("actuator metric requests = 0, want Actuator path")
	}
}

func TestNewCollectorDeprecatedSpringAliasKeepsAutoPrometheusPreferenceAndFallback(t *testing.T) {
	for _, prometheusAvailable := range []bool{true, false} {
		t.Run(fmt.Sprintf("prometheus_available=%t", prometheusAvailable), func(t *testing.T) {
			var prometheusRequests, actuatorRequests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/actuator/health":
					fmt.Fprint(w, `{"status":"UP"}`)
				case "/actuator/prometheus":
					prometheusRequests++
					if !prometheusAvailable {
						http.Error(w, "missing", http.StatusNotFound)
						return
					}
					w.Header().Set("Content-Type", "text/plain; version=0.0.4")
					fmt.Fprint(w, "process_start_time_seconds 1\nprocess_cpu_usage 0.1\n")
				default:
					actuatorRequests++
					fmt.Fprint(w, `{"name":"metric","measurements":[{"statistic":"COUNT","value":1},{"statistic":"TOTAL_TIME","value":0}]}`)
				}
			}))
			defer server.Close()

			base := server.URL + "/actuator"
			cfg := loadAppTestConfig(t, fmt.Sprintf("actuator_base_url: %q", base))
			targetCollector, err := newCollector(cfg.Targets[0], time.Second)
			if err != nil {
				t.Fatalf("newCollector() error = %v", err)
			}
			if _, err := targetCollector.Collect(context.Background()); err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if prometheusRequests != 1 {
				t.Fatalf("prometheus requests = %d, want 1", prometheusRequests)
			}
			if prometheusAvailable && actuatorRequests != 0 {
				t.Fatalf("actuator metric requests = %d, want 0", actuatorRequests)
			}
			if !prometheusAvailable && actuatorRequests == 0 {
				t.Fatal("actuator metric requests = 0, want fallback")
			}
		})
	}
}

func TestNewCollectorCanonicalSpringURLUsesExplicitAuth(t *testing.T) {
	var healthAuth, prometheusAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "user" || password != "secret" {
			t.Errorf("basic auth = %q/%q/%v, want user/secret/true", user, password, ok)
		}
		switch r.URL.Path {
		case "/actuator/health":
			healthAuth = ok && user == "user" && password == "secret"
			fmt.Fprint(w, `{"status":"UP"}`)
		case "/actuator/prometheus":
			prometheusAuth = ok && user == "user" && password == "secret"
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			fmt.Fprint(w, "process_start_time_seconds 1\nprocess_cpu_usage 0.1\n")
		}
	}))
	defer server.Close()

	url := server.URL + "/actuator"
	cfg := loadAppTestConfig(t, fmt.Sprintf("url: %q\n    auth:\n      type: basic\n      username: user\n      password: secret", url))
	targetCollector, err := newCollector(cfg.Targets[0], time.Second)
	if err != nil {
		t.Fatalf("newCollector() error = %v", err)
	}
	if _, err := targetCollector.Collect(context.Background()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if !healthAuth || !prometheusAuth {
		t.Fatalf("explicit auth applied to health=%t prometheus=%t", healthAuth, prometheusAuth)
	}
}

func loadAppTestConfig(t *testing.T, target string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := fmt.Sprintf(`
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"
    type: spring
    %s
`, target)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cfg
}

func typeName(value any) string {
	return reflect.TypeOf(value).String()
}
