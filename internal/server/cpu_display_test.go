package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestDashboardCPUUnitsPreservePublicAndRawSamples(t *testing.T) {
	for _, targetType := range []string{"spring", "quarkus", "micronaut", "statlite-metrics"} {
		t.Run(targetType, func(t *testing.T) {
			store := newPublicTestStore(t)
			mon := newServerTestMonitor(t, "app", store, &countingCollector{})
			manager, err := monitor.NewManager([]monitor.ManagedTarget{{Metadata: monitor.TargetMetadata{Name: "app", Type: targetType}, Monitor: mon}})
			if err != nil {
				t.Fatal(err)
			}
			server := NewWithManager("", manager)
			now := time.Date(2026, 10, 20, 12, 1, 0, 0, time.UTC)
			server.FreezeDashboardTime(now)
			base := now.Add(-time.Minute)
			for i, sample := range []struct {
				value float64
				unit  string
			}{{.25, "ratio"}, {2.75, "cores"}, {1, ""}, {.5, "unknown"}} {
				at := base.Add(time.Duration(i) * 10 * time.Second)
				_, err := store.SaveCollectionResult(t.Context(), &collector.CollectionResult{
					TargetName: "app", PollStartedAt: at, PollFinishedAt: at,
					Samples: []collector.MetricSample{
						{Key: "process_cpu_usage", Kind: collector.MetricKindGauge, Value: sample.value, Unit: sample.unit},
						{Key: "runtime_heap_used_bytes", Kind: collector.MetricKindGauge, Value: 100, Unit: "bytes"},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			expectedUnit, expected := "ratio", .625
			if targetType == "statlite-metrics" {
				expectedUnit, expected = "cores", 1.875
			}
			for _, raw := range []bool{false, true} {
				server.rawSeries = raw
				for _, window := range []string{"1h", "24h", "7d", "30d"} {
					response := httptest.NewRecorder()
					server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/series?range="+window, nil))
					if response.Code != http.StatusOK {
						t.Fatalf("series: %d %s", response.Code, response.Body.String())
					}
					var series storage.Series
					if err := json.Unmarshal(response.Body.Bytes(), &series); err != nil {
						t.Fatal(err)
					}
					if series.CPUUnit != expectedUnit || !series.CPUUnitConflict {
						t.Fatalf("%s raw=%v metadata = %+v", window, raw, series)
					}
					// Long-range sampling keeps the first ratio and last unknown observation.
					sampled := !raw && (window == "7d" || window == "30d")
					if series.CPUUnitAssumed == sampled {
						t.Fatalf("%s raw=%v assumed=%v", window, raw, series.CPUUnitAssumed)
					}
					if series.LatestPoint == nil || series.LatestPoint.ProcessCPUUsage != nil {
						t.Fatalf("latest CPU = %+v", series.LatestPoint)
					}
					if !raw && window != "1h" {
						if len(series.Points) != 1 {
							t.Fatalf("%s points=%d", window, len(series.Points))
						}
						if sampled {
							if expectedUnit == "cores" {
								if series.Points[0].ProcessCPUUsage != nil {
									t.Fatal("conflicting CPU leaked into chart")
								}
							} else {
								assertPublicMetricValue(t, "sampled CPU", series.Points[0].ProcessCPUUsage, .25)
							}
						} else {
							assertPublicMetricValue(t, "chart CPU", series.Points[0].ProcessCPUUsage, expected)
						}
						assertPublicMetricValue(t, "memory", series.Points[0].HeapUsedBytes, 100)
					} else {
						if len(series.Points) != 4 {
							t.Fatalf("raw points=%d", len(series.Points))
						}
						matchingIndex, conflictingIndex, matchingValue := 0, 1, .25
						if expectedUnit == "cores" {
							matchingIndex, conflictingIndex, matchingValue = 1, 0, 2.75
						}
						assertPublicMetricValue(t, "matching CPU", series.Points[matchingIndex].ProcessCPUUsage, matchingValue)
						if series.Points[conflictingIndex].ProcessCPUUsage != nil {
							t.Fatalf("%s raw=%v conflicting CPU leaked into chart", window, raw)
						}
						assertPublicMetricValue(t, "assumed CPU", series.Points[2].ProcessCPUUsage, 1)
						if series.Points[3].ProcessCPUUsage != nil {
							t.Fatal("unknown explicit unit accepted")
						}
					}
				}
			}
			// Public API keeps the historical unit-agnostic average.
			body := requestPublicMetrics(t, server, "/api/v1/metrics")
			if len(body.Points) != 1 {
				t.Fatalf("public points = %d", len(body.Points))
			}
			assertPublicMetricValue(t, "legacy CPU", body.Points[0].ProcessCPUCores, 1.125)
			latest, err := store.LatestSnapshot(t.Context(), "app")
			if err != nil {
				t.Fatal(err)
			}
			if latest.Result.Samples[0].Key != "process_cpu_usage" || latest.Result.Samples[0].Unit != "unknown" || latest.Result.Samples[0].Value != .5 {
				t.Fatalf("raw snapshot changed: %+v", latest)
			}
		})
	}
}
