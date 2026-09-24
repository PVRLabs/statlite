package server

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/monitor"
)

func TestParsePublicQuery(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		allowed []string
		wantErr bool
	}{
		{"status target", "/api/v1/status?target=app", []string{"target"}, false},
		{"status omitted", "/api/v1/status", []string{"target"}, false},
		{"status unknown", "/api/v1/status?range=1h", []string{"target"}, true},
		{"events typo", "/api/v1/events?rang=5m", []string{"target", "range", "limit"}, true},
		{"events start", "/api/v1/events?start=2026-09-24", []string{"target", "range", "limit"}, true},
		{"events end", "/api/v1/events?end=2026-09-24", []string{"target", "range", "limit"}, true},
		{"events accepted", "/api/v1/events?range=5m&limit=10", []string{"target", "range", "limit"}, false},
		{"duplicate identical", "/api/v1/status?target=app&target=app", []string{"target"}, true},
		{"duplicate range", "/api/v1/events?range=5m&range=1h", []string{"target", "range", "limit"}, true},
		{"empty target", "/api/v1/status?target=", []string{"target"}, true},
		{"empty range", "/api/v1/events?range=", []string{"target", "range", "limit"}, true},
		{"empty limit", "/api/v1/events?limit=", []string{"target", "range", "limit"}, true},
		{"whitespace", "/api/v1/status?target=+", []string{"target"}, true},
		{"bad encoding", "/api/v1/status?target=%zz", []string{"target"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePublicQuery(httptest.NewRequest("GET", tt.path, nil), tt.allowed...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePublicQuery() error = %v, want error %v", err, tt.wantErr)
			}
		})
	}
	for _, parameter := range []string{"range=1h", "limit=100", "start=x", "end=x", "bucket=60", "metric=x"} {
		t.Run("metrics "+parameter, func(t *testing.T) {
			_, err := parsePublicQuery(httptest.NewRequest("GET", "/api/v1/metrics?"+parameter, nil), "target")
			if err == nil {
				t.Fatal("metrics query accepted unsupported parameter")
			}
		})
	}
}

func TestSelectPublicTarget(t *testing.T) {
	store := newPublicTestStore(t)
	alpha := newServerTestMonitor(t, "alpha", store, &countingCollector{})
	beta := newServerTestMonitor(t, "beta", store, &countingCollector{})
	one, err := monitor.NewManager([]monitor.ManagedTarget{{Metadata: monitor.TargetMetadata{Name: "alpha"}, Monitor: alpha}})
	if err != nil {
		t.Fatal(err)
	}
	multiple := newServerTestManager(t, alpha, beta)
	for _, tt := range []struct {
		name, raw, want string
		manager         *monitor.Manager
		wantErr         bool
	}{
		{"one omitted", "", "alpha", one, false},
		{"one exact", "target=alpha", "alpha", one, false},
		{"one unknown", "target=missing", "", one, true},
		{"multiple omitted", "", "", multiple, true},
		{"multiple exact", "target=beta", "beta", multiple, false},
		{"multiple unknown", "target=missing", "", multiple, true},
		{"no trimming", "target=%20beta", "", multiple, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			query, err := parsePublicQuery(httptest.NewRequest("GET", "/api/v1/status?"+tt.raw, nil), "target")
			if err != nil {
				t.Fatal(err)
			}
			target, err := selectPublicTarget(tt.manager, query)
			if (err != nil) != tt.wantErr || (err == nil && target.Metadata.Name != tt.want) {
				t.Fatalf("selectPublicTarget() = %q, %v; want %q, error %v", target.Metadata.Name, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestParsePublicEventsOptions(t *testing.T) {
	for _, tt := range []struct {
		name, raw string
		window    time.Duration
		limit     int
		wantErr   bool
	}{
		{"defaults", "", time.Hour, 100, false},
		{"five minutes", "range=5m", 5 * time.Minute, 100, false},
		{"one hour", "range=1h", time.Hour, 100, false},
		{"day and max", "range=24h&limit=500", 24 * time.Hour, 500, false},
		{"minimum", "limit=1", time.Hour, 1, false},
		{"dashboard alias", "range=last_hour", 0, 0, true},
		{"dashboard longer", "range=7d", 0, 0, true},
		{"wrong case", "range=1H", 0, 0, true},
		{"zero", "limit=0", 0, 0, true},
		{"negative", "limit=-1", 0, 0, true},
		{"over max", "limit=501", 0, 0, true},
		{"not numeric", "limit=abc", 0, 0, true},
		{"decimal", "limit=1.5", 0, 0, true},
		{"signed", "limit=%2B1", 0, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			query, err := parsePublicQuery(httptest.NewRequest("GET", "/api/v1/events?"+tt.raw, nil), "target", "range", "limit")
			if err != nil {
				t.Fatal(err)
			}
			window, limit, err := parsePublicEventsOptions(query)
			if (err != nil) != tt.wantErr || (err == nil && (window != tt.window || limit != tt.limit)) {
				t.Fatalf("parsePublicEventsOptions() = %v, %d, %v", window, limit, err)
			}
		})
	}
}
