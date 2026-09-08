package collector_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestSpringIncompleteStatusAggregateDoesNotFabricateRecoveryDelta(t *testing.T) {
	tests := []struct {
		name           string
		failedResponse func(http.ResponseWriter)
		warningType    string
	}{
		{
			name: "fetch failure",
			failedResponse: func(w http.ResponseWriter) {
				http.Error(w, "backend timeout", http.StatusGatewayTimeout)
			},
			warningType: "metric_fetch_failed",
		},
		{
			name: "missing COUNT",
			failedResponse: func(w http.ResponseWriter) {
				fmt.Fprint(w, `{"name":"http.server.requests","measurements":[{"statistic":"TOTAL_TIME","value":1}]}`)
			},
			warningType: "metric_measurement_missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			poll := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/actuator/health":
					fmt.Fprint(w, `{"status":"UP"}`)
				case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "":
					fmt.Fprint(w, `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":100},{"statistic":"TOTAL_TIME","value":10}],"availableTags":[{"tag":"status","values":["500","501"]}]}`)
				case r.URL.Query().Get("tag") == "status:500":
					value := 5
					if poll > 0 {
						value = 6
					}
					fmt.Fprintf(w, `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":%d}]}`, value)
				case r.URL.Query().Get("tag") == "status:501" && poll == 1:
					tt.failedResponse(w)
				case r.URL.Query().Get("tag") == "status:501":
					value := 5
					if poll > 0 {
						value = 6
					}
					fmt.Fprintf(w, `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":%d}]}`, value)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client, err := collector.NewActuatorClient(server.URL+"/actuator", time.Second, nil)
			if err != nil {
				t.Fatalf("NewActuatorClient() error = %v", err)
			}
			springCollector := collector.NewSpringActuatorCollector("app", client, false)
			store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "statlite.sqlite"))
			if err != nil {
				t.Fatalf("storage.Open() error = %v", err)
			}
			defer store.Close()

			base := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
			appRunID, err := store.EnsureAppRun(context.Background(), "app", &base, base)
			if err != nil {
				t.Fatalf("EnsureAppRun() error = %v", err)
			}
			results := make([]*collector.CollectionResult, 3)
			for poll = range results {
				result, collectErr := springCollector.Collect(context.Background())
				if collectErr != nil {
					t.Fatalf("Collect() poll %d error = %v", poll, collectErr)
				}
				result.PollStartedAt = base.Add(time.Duration(poll) * time.Minute)
				result.PollFinishedAt = result.PollStartedAt.Add(time.Second)
				if _, saveErr := store.SaveCollectionResultWithAppRun(context.Background(), result, &appRunID); saveErr != nil {
					t.Fatalf("SaveCollectionResultWithAppRun() poll %d error = %v", poll, saveErr)
				}
				results[poll] = result
			}

			if sampleValue(results[0], "http_5xx_total") != 10 {
				t.Fatalf("baseline http_5xx_total missing or wrong: %#v", results[0].Samples)
			}
			if hasSample(results[1], "http_5xx_total") {
				t.Fatalf("incomplete poll retained http_5xx_total: %#v", results[1].Samples)
			}
			if sampleValue(results[2], "http_5xx_total") != 12 {
				t.Fatalf("recovery http_5xx_total missing or wrong: %#v", results[2].Samples)
			}
			if !hasStatusAggregateWarning(results[1], tt.warningType, "http_5xx_total", "status 501") {
				t.Fatalf("incomplete poll events = %#v, want warning %s for http_5xx_total status 501", results[1].Events, tt.warningType)
			}

			series, err := store.Series(context.Background(), "app", base, base.Add(3*time.Minute))
			if err != nil {
				t.Fatalf("Series() error = %v", err)
			}
			if len(series.Points) != 3 {
				t.Fatalf("series point count = %d, want 3", len(series.Points))
			}
			if series.Points[1].HTTP5xx != nil {
				t.Fatalf("incomplete poll HTTP5xx delta = %v, want nil", *series.Points[1].HTTP5xx)
			}
			if got := series.Points[2].HTTP5xx; got == nil || *got != 2 {
				t.Fatalf("recovery HTTP5xx delta = %v, want 2", got)
			}
		})
	}
}

func hasSample(result *collector.CollectionResult, key string) bool {
	for _, sample := range result.Samples {
		if sample.Key == key {
			return true
		}
	}
	return false
}

func sampleValue(result *collector.CollectionResult, key string) float64 {
	for _, sample := range result.Samples {
		if sample.Key == key {
			return sample.Value
		}
	}
	return -1
}

func hasStatusAggregateWarning(result *collector.CollectionResult, eventType, metricKey, messagePart string) bool {
	for _, event := range result.Events {
		if event.Severity == collector.EventSeverityWarning &&
			event.Type == eventType &&
			event.MetricKey == metricKey &&
			strings.Contains(event.Message, messagePart) {
			return true
		}
	}
	return false
}
