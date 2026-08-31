package app

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func typeName(value any) string {
	return reflect.TypeOf(value).String()
}
