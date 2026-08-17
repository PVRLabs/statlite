package collector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type springContractFixture struct {
	name                  string
	collectHostMetrics    bool
	responses             map[string]springScriptedResponse
	transportError        error
	wantError             bool
	wantNormalized        springNormalizedSnapshot
	baselineRequests      map[string]int
	baselineEvents        []CollectorEvent
	allowedConsolidations []springEventConsolidation
}

type springScriptedResponse struct {
	status int
	body   string
}

type springNormalizedSnapshot struct {
	healthStatus     string
	dbHealthStatus   string
	processStartTime string
	samples          []MetricSample
}

type springEventConsolidation struct {
	severity          EventSeverity
	eventType         string
	affectedKeys      []string
	effectiveEndpoint string
	httpStatus        string
	actionableCause   string
}

type springRecordingTransport struct {
	requests       []string
	responses      map[string]springScriptedResponse
	transportError error
}

func TestSpringCollectorPost22ContractMatrix(t *testing.T) {
	fixtures := springPost22ContractFixtures()
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			transport := &springRecordingTransport{
				responses:      fixture.responses,
				transportError: fixture.transportError,
			}
			client, err := NewActuatorClient("http://spring.test/actuator", time.Second, nil)
			if err != nil {
				t.Fatalf("NewActuatorClient() error = %v", err)
			}
			client.httpClient.Transport = transport

			result, err := NewSpringActuatorCollector("spring-contract", client, fixture.collectHostMetrics).Collect(context.Background())
			if (err != nil) != fixture.wantError {
				t.Fatalf("Collect() error = %v, wantError=%v", err, fixture.wantError)
			}

			gotNormalized := snapshotSpringNormalized(result)
			if !reflect.DeepEqual(gotNormalized, fixture.wantNormalized) {
				t.Fatalf("normalized snapshot mismatch\n got: %#v\nwant: %#v", gotNormalized, fixture.wantNormalized)
			}
			assertSpringRequestsWithinBaseline(t, transport.requests, fixture.baselineRequests)
			assertSpringEventsWithinBaseline(t, result.Events, fixture.baselineEvents, fixture.allowedConsolidations)
		})
	}
}

func (t *springRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		key += "?" + req.URL.RawQuery
	}
	t.requests = append(t.requests, key)
	if t.transportError != nil {
		return nil, t.transportError
	}
	response, ok := t.responses[key]
	if !ok {
		response = springScriptedResponse{status: http.StatusNotFound, body: "not found\n"}
	}
	return &http.Response{
		StatusCode: response.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(response.body)),
		Request:    req,
	}, nil
}

func springPost22ContractFixtures() []springContractFixture {
	const (
		healthUP       = `{"status":"UP"}`
		healthUPWithDB = `{"status":"UP","components":{"db":{"status":"UP"}}}`
		httpComplete   = `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":20},{"statistic":"TOTAL_TIME","value":4.2}],"availableTags":[{"tag":"status","values":["200","404","500"]}]}`
		httpPartial    = `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":10},{"statistic":"TOTAL_TIME","value":2}],"availableTags":[{"tag":"status","values":["404","500"]}]}`
		httpShared     = `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":10},{"statistic":"TOTAL_TIME","value":2}],"availableTags":[{"tag":"status","values":["404"]}]}`
		status404      = `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":3}]}`
		jvmHeap        = `{"name":"jvm.memory.used","measurements":[{"statistic":"VALUE","value":1024}]}`
		processCPU     = `{"name":"process.cpu.usage","measurements":[{"statistic":"VALUE","value":0.12}]}`
		hostCPU        = `{"name":"system.cpu.usage","measurements":[{"statistic":"VALUE","value":0.34}]}`
		diskFree       = `{"name":"disk.free","measurements":[{"statistic":"VALUE","value":600}]}`
		diskTotal      = `{"name":"disk.total","measurements":[{"statistic":"VALUE","value":1000}]}`
		processStart   = `{"name":"process.start.time","measurements":[{"statistic":"VALUE","value":1700000000}]}`
	)

	return []springContractFixture{
		{
			name:               "complete output with host metrics",
			collectHostMetrics: true,
			responses: map[string]springScriptedResponse{
				"/actuator/health":                                        springOK(healthUPWithDB),
				"/actuator/metrics/http.server.requests":                  springOK(httpComplete),
				"/actuator/metrics/http.server.requests?tag=status%3A404": springOK(status404),
				"/actuator/metrics/http.server.requests?tag=status%3A500": springOK(`{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":2}]}`),
				"/actuator/metrics/jvm.memory.used?tag=area%3Aheap":       springOK(jvmHeap),
				"/actuator/metrics/process.cpu.usage":                     springOK(processCPU),
				"/actuator/metrics/system.cpu.usage":                      springOK(hostCPU),
				"/actuator/metrics/disk.free":                             springOK(diskFree),
				"/actuator/metrics/disk.total":                            springOK(diskTotal),
				"/actuator/metrics/process.start.time":                    springOK(processStart),
			},
			wantNormalized: springNormalizedSnapshot{
				healthStatus:     "UP",
				dbHealthStatus:   "UP",
				processStartTime: "2023-11-14T22:13:20Z",
				samples: []MetricSample{
					{Key: "host_cpu_usage", Kind: MetricKindGauge, Value: 0.34, Unit: "ratio"},
					{Key: "host_disk_total_bytes", Kind: MetricKindGauge, Value: 1000, Unit: "bytes"},
					{Key: "host_disk_used_bytes", Kind: MetricKindGauge, Value: 400, Unit: "bytes"},
					{Key: "http_404_total", Kind: MetricKindCounter, Value: 3, Unit: "requests"},
					{Key: "http_4xx_total", Kind: MetricKindCounter, Value: 3, Unit: "requests"},
					{Key: "http_5xx_total", Kind: MetricKindCounter, Value: 2, Unit: "requests"},
					{Key: "http_request_time_total_seconds", Kind: MetricKindCounter, Value: 4.2, Unit: "seconds"},
					{Key: "http_requests_total", Kind: MetricKindCounter, Value: 20, Unit: "requests"},
					{Key: "jvm_heap_used_bytes", Kind: MetricKindGauge, Value: 1024, Unit: "bytes"},
					{Key: "process_cpu_usage", Kind: MetricKindGauge, Value: 0.12, Unit: "ratio"},
					{Key: "process_start_time", Kind: MetricKindGauge, Value: 1700000000, Unit: "unix_seconds"},
				},
			},
			baselineRequests: map[string]int{
				"/actuator/health":                                        1,
				"/actuator/metrics/http.server.requests":                  1,
				"/actuator/metrics/http.server.requests?tag=status%3A404": 2,
				"/actuator/metrics/http.server.requests?tag=status%3A500": 1,
				"/actuator/metrics/jvm.memory.used?tag=area%3Aheap":       1,
				"/actuator/metrics/process.cpu.usage":                     1,
				"/actuator/metrics/system.cpu.usage":                      1,
				"/actuator/metrics/disk.free":                             1,
				"/actuator/metrics/disk.total":                            1,
				"/actuator/metrics/process.start.time":                    1,
			},
		},
		{
			name: "missing optional metric exposure",
			responses: map[string]springScriptedResponse{
				"/actuator/health": springOK(healthUP),
			},
			wantNormalized: springNormalizedSnapshot{healthStatus: "UP", samples: []MetricSample{}},
			baselineRequests: springRequestCounts(
				"/actuator/health",
				"/actuator/metrics/http.server.requests",
				"/actuator/metrics/jvm.memory.used?tag=area%3Aheap",
				"/actuator/metrics/process.cpu.usage",
				"/actuator/metrics/process.start.time",
			),
			baselineEvents: []CollectorEvent{
				springFetchWarning("http_requests_total", "actuator metrics/http.server.requests returned HTTP 404: not found"),
				springFetchWarning("jvm_heap_used_bytes", "actuator metrics/jvm.memory.used returned HTTP 404: not found"),
				springFetchWarning("process_cpu_usage", "actuator metrics/process.cpu.usage returned HTTP 404: not found"),
				springFetchWarning("process_start_time", "actuator metrics/process.start.time returned HTTP 404: not found"),
			},
		},
		{
			name: "sparse metric response",
			responses: map[string]springScriptedResponse{
				"/actuator/health":                       springOK(healthUP),
				"/actuator/metrics/http.server.requests": springOK(`{"name":"http.server.requests"}`),
			},
			wantNormalized: springNormalizedSnapshot{healthStatus: "UP", samples: []MetricSample{}},
			baselineRequests: springRequestCounts(
				"/actuator/health",
				"/actuator/metrics/http.server.requests",
				"/actuator/metrics/jvm.memory.used?tag=area%3Aheap",
				"/actuator/metrics/process.cpu.usage",
				"/actuator/metrics/process.start.time",
			),
			baselineEvents: []CollectorEvent{
				{Severity: EventSeverityWarning, Type: "metric_measurement_missing", MetricKey: "http_requests_total", Message: "http.server.requests missing COUNT measurement"},
				{Severity: EventSeverityWarning, Type: "metric_measurement_missing", MetricKey: "http_request_time_total_seconds", Message: "http.server.requests missing TOTAL_TIME measurement"},
				{Severity: EventSeverityWarning, Type: "metric_tag_missing", MetricKey: "http_4xx_total", Message: "http.server.requests does not expose status tags"},
				springFetchWarning("jvm_heap_used_bytes", "actuator metrics/jvm.memory.used returned HTTP 404: not found"),
				springFetchWarning("process_cpu_usage", "actuator metrics/process.cpu.usage returned HTTP 404: not found"),
				springFetchWarning("process_start_time", "actuator metrics/process.start.time returned HTTP 404: not found"),
			},
		},
		{
			name: "partial status failure",
			responses: map[string]springScriptedResponse{
				"/actuator/health":                                        springOK(healthUP),
				"/actuator/metrics/http.server.requests":                  springOK(httpPartial),
				"/actuator/metrics/http.server.requests?tag=status%3A404": springOK(`{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":1}]}`),
				"/actuator/metrics/http.server.requests?tag=status%3A500": {status: http.StatusGatewayTimeout, body: "backend timeout\n"},
				"/actuator/metrics/process.cpu.usage":                     springOK(`{"name":"process.cpu.usage","measurements":[{"statistic":"VALUE","value":0.5}]}`),
			},
			wantNormalized: springNormalizedSnapshot{
				healthStatus: "UP",
				samples: []MetricSample{
					{Key: "http_404_total", Kind: MetricKindCounter, Value: 1, Unit: "requests"},
					{Key: "http_4xx_total", Kind: MetricKindCounter, Value: 1, Unit: "requests"},
					{Key: "http_request_time_total_seconds", Kind: MetricKindCounter, Value: 2, Unit: "seconds"},
					{Key: "http_requests_total", Kind: MetricKindCounter, Value: 10, Unit: "requests"},
					{Key: "process_cpu_usage", Kind: MetricKindGauge, Value: 0.5, Unit: "ratio"},
				},
			},
			baselineRequests: springRequestCounts(
				"/actuator/health",
				"/actuator/metrics/http.server.requests",
				"/actuator/metrics/http.server.requests?tag=status%3A404",
				"/actuator/metrics/http.server.requests?tag=status%3A404",
				"/actuator/metrics/http.server.requests?tag=status%3A500",
				"/actuator/metrics/jvm.memory.used?tag=area%3Aheap",
				"/actuator/metrics/process.cpu.usage",
				"/actuator/metrics/process.start.time",
			),
			baselineEvents: []CollectorEvent{
				springFetchWarning("http_5xx_total", "http.server.requests status 500: actuator metrics/http.server.requests returned HTTP 504: backend timeout"),
				springFetchWarning("jvm_heap_used_bytes", "actuator metrics/jvm.memory.used returned HTTP 404: not found"),
				springFetchWarning("process_start_time", "actuator metrics/process.start.time returned HTTP 404: not found"),
			},
		},
		{
			name: "equivalent shared status failure",
			responses: map[string]springScriptedResponse{
				"/actuator/health":                                        springOK(healthUP),
				"/actuator/metrics/http.server.requests":                  springOK(httpShared),
				"/actuator/metrics/http.server.requests?tag=status%3A404": {status: http.StatusNotFound, body: "missing status\n"},
				"/actuator/metrics/jvm.memory.used?tag=area%3Aheap":       springOK(jvmHeap),
				"/actuator/metrics/process.cpu.usage":                     springOK(processCPU),
				"/actuator/metrics/process.start.time":                    springOK(processStart),
			},
			wantNormalized: springNormalizedSnapshot{
				healthStatus:     "UP",
				processStartTime: "2023-11-14T22:13:20Z",
				samples: []MetricSample{
					{Key: "http_5xx_total", Kind: MetricKindCounter, Value: 0, Unit: "requests"},
					{Key: "http_request_time_total_seconds", Kind: MetricKindCounter, Value: 2, Unit: "seconds"},
					{Key: "http_requests_total", Kind: MetricKindCounter, Value: 10, Unit: "requests"},
					{Key: "jvm_heap_used_bytes", Kind: MetricKindGauge, Value: 1024, Unit: "bytes"},
					{Key: "process_cpu_usage", Kind: MetricKindGauge, Value: 0.12, Unit: "ratio"},
					{Key: "process_start_time", Kind: MetricKindGauge, Value: 1700000000, Unit: "unix_seconds"},
				},
			},
			baselineRequests: springRequestCounts(
				"/actuator/health",
				"/actuator/metrics/http.server.requests",
				"/actuator/metrics/http.server.requests?tag=status%3A404",
				"/actuator/metrics/http.server.requests?tag=status%3A404",
				"/actuator/metrics/jvm.memory.used?tag=area%3Aheap",
				"/actuator/metrics/process.cpu.usage",
				"/actuator/metrics/process.start.time",
			),
			baselineEvents: []CollectorEvent{
				springFetchWarning("http_404_total", "http.server.requests status 404: actuator metrics/http.server.requests returned HTTP 404: missing status"),
				springFetchWarning("http_4xx_total", "http.server.requests status 404: actuator metrics/http.server.requests returned HTTP 404: missing status"),
			},
			allowedConsolidations: []springEventConsolidation{{
				severity:          EventSeverityWarning,
				eventType:         "metric_fetch_failed",
				affectedKeys:      []string{"http_404_total", "http_4xx_total"},
				effectiveEndpoint: "/actuator/metrics/http.server.requests?tag=status%3A404",
				httpStatus:        "HTTP 404",
				actionableCause:   "missing status",
			}},
		},
		{
			name:             "authentication failure",
			responses:        map[string]springScriptedResponse{"/actuator/health": {status: http.StatusUnauthorized, body: `{"error":"Unauthorized"}`}},
			wantError:        true,
			wantNormalized:   springNormalizedSnapshot{samples: []MetricSample{}},
			baselineRequests: springRequestCounts("/actuator/health"),
			baselineEvents:   []CollectorEvent{{Severity: EventSeverityError, Type: "health_fetch_failed", Message: `actuator health returned HTTP 401: {"error":"Unauthorized"}`}},
		},
		{
			name: "missing health exposure",
			responses: map[string]springScriptedResponse{
				"/actuator/health": {status: http.StatusNotFound, body: `{"error":"Not Found"}`},
			},
			wantError:        true,
			wantNormalized:   springNormalizedSnapshot{samples: []MetricSample{}},
			baselineRequests: springRequestCounts("/actuator/health"),
			baselineEvents:   []CollectorEvent{{Severity: EventSeverityError, Type: "health_fetch_failed", Message: `actuator health returned HTTP 404: {"error":"Not Found"}`}},
		},
		{
			name:             "malformed health response",
			responses:        map[string]springScriptedResponse{"/actuator/health": springOK(`{"status":`)},
			wantError:        true,
			wantNormalized:   springNormalizedSnapshot{samples: []MetricSample{}},
			baselineRequests: springRequestCounts("/actuator/health"),
			baselineEvents:   []CollectorEvent{{Severity: EventSeverityError, Type: "health_fetch_failed", Message: "parsing actuator health response: unexpected end of JSON input"}},
		},
		{
			name:             "transport failure",
			responses:        map[string]springScriptedResponse{},
			transportError:   errors.New("transport unavailable"),
			wantError:        true,
			wantNormalized:   springNormalizedSnapshot{samples: []MetricSample{}},
			baselineRequests: springRequestCounts("/actuator/health"),
			baselineEvents:   []CollectorEvent{{Severity: EventSeverityError, Type: "health_fetch_failed", Message: `fetching actuator health: Get "http://spring.test/actuator/health": transport unavailable`}},
		},
	}
}

func springOK(body string) springScriptedResponse {
	return springScriptedResponse{status: http.StatusOK, body: body}
}

func springFetchWarning(metricKey, message string) CollectorEvent {
	return CollectorEvent{Severity: EventSeverityWarning, Type: "metric_fetch_failed", MetricKey: metricKey, Message: message}
}

func springRequestCounts(requests ...string) map[string]int {
	counts := make(map[string]int, len(requests))
	for _, request := range requests {
		counts[request]++
	}
	return counts
}

func snapshotSpringNormalized(result *CollectionResult) springNormalizedSnapshot {
	snapshot := springNormalizedSnapshot{
		healthStatus:   result.HealthStatus,
		dbHealthStatus: result.DBHealthStatus,
		samples:        append([]MetricSample(nil), result.Samples...),
	}
	if snapshot.samples == nil {
		snapshot.samples = []MetricSample{}
	}
	if result.ProcessStartTime != nil {
		snapshot.processStartTime = result.ProcessStartTime.UTC().Format(time.RFC3339Nano)
	}
	sort.Slice(snapshot.samples, func(i, j int) bool {
		return snapshot.samples[i].Key < snapshot.samples[j].Key
	})
	return snapshot
}

func assertSpringRequestsWithinBaseline(t *testing.T, requests []string, baseline map[string]int) {
	t.Helper()
	got := springRequestCounts(requests...)
	for request, count := range got {
		baselineCount, ok := baseline[request]
		if !ok {
			t.Fatalf("new effective request %q made %d time(s); baseline=%v", request, count, baseline)
		}
		if count > baselineCount {
			t.Fatalf("effective request %q count = %d, baseline maximum = %d", request, count, baselineCount)
		}
	}
	for request, baselineCount := range baseline {
		gotCount := got[request]
		if baselineCount == 1 && gotCount != 1 {
			t.Fatalf("unique baseline request %q count = %d, want 1", request, gotCount)
		}
		if baselineCount > 1 && gotCount < 1 {
			t.Fatalf("duplicated baseline request %q disappeared; baseline count = %d", request, baselineCount)
		}
	}
	if len(requests) > springRequestTotal(baseline) {
		t.Fatalf("total requests = %d, baseline maximum = %d; got=%v", len(requests), springRequestTotal(baseline), got)
	}
}

func springRequestTotal(counts map[string]int) int {
	var total int
	for _, count := range counts {
		total += count
	}
	return total
}

func assertSpringEventsWithinBaseline(t *testing.T, got, baseline []CollectorEvent, consolidations []springEventConsolidation) {
	t.Helper()
	if reflect.DeepEqual(got, baseline) {
		return
	}
	if len(got) > len(baseline) {
		t.Fatalf("event count = %d, baseline maximum = %d; got=%#v", len(got), len(baseline), got)
	}
	if len(consolidations) == 0 {
		t.Fatalf("events changed outside an allowed consolidation\n got: %#v\nwant: %#v", got, baseline)
	}

	remainingBaseline := append([]CollectorEvent(nil), baseline...)
	remainingGot := append([]CollectorEvent(nil), got...)
	for _, consolidation := range consolidations {
		baselineGroup := springEventsForKeys(remainingBaseline, consolidation.affectedKeys)
		gotGroup := springEventsForKeys(remainingGot, consolidation.affectedKeys)
		remainingBaseline = removeSpringEventsForKeys(remainingBaseline, consolidation.affectedKeys)
		if reflect.DeepEqual(gotGroup, baselineGroup) {
			remainingGot = removeSpringEventsForKeys(remainingGot, consolidation.affectedKeys)
			continue
		}
		index := consolidatedSpringEventIndex(remainingGot, consolidation)
		if index < 0 {
			t.Fatalf("events do not preserve allowed consolidation %#v: %#v", consolidation, got)
		}
		remainingGot = append(remainingGot[:index], remainingGot[index+1:]...)
	}
	if !reflect.DeepEqual(remainingGot, remainingBaseline) {
		t.Fatalf("unaffected events changed\n got: %#v\nwant: %#v", remainingGot, remainingBaseline)
	}
}

func springEventsForKeys(events []CollectorEvent, keys []string) []CollectorEvent {
	keySet := make(map[string]bool, len(keys))
	for _, key := range keys {
		keySet[key] = true
	}
	var selected []CollectorEvent
	for _, event := range events {
		if keySet[event.MetricKey] {
			selected = append(selected, event)
		}
	}
	return selected
}

func removeSpringEventsForKeys(events []CollectorEvent, keys []string) []CollectorEvent {
	keySet := make(map[string]bool, len(keys))
	for _, key := range keys {
		keySet[key] = true
	}
	filtered := events[:0]
	for _, event := range events {
		if !keySet[event.MetricKey] {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func consolidatedSpringEventIndex(events []CollectorEvent, consolidation springEventConsolidation) int {
	for i, event := range events {
		if springEventMatchesConsolidation(event, consolidation) {
			return i
		}
	}
	return -1
}

func springEventMatchesConsolidation(event CollectorEvent, consolidation springEventConsolidation) bool {
	if event.Severity != consolidation.severity || event.Type != consolidation.eventType || event.MetricKey != "" {
		return false
	}
	requiredFragments := append([]string(nil), consolidation.affectedKeys...)
	requiredFragments = append(requiredFragments, consolidation.effectiveEndpoint, consolidation.httpStatus, consolidation.actionableCause)
	for _, fragment := range requiredFragments {
		if fragment == "" || !strings.Contains(event.Message, fragment) {
			return false
		}
	}
	return true
}

func TestSpringConsolidatedEventRequiresCompleteDiagnostics(t *testing.T) {
	contract := springEventConsolidation{
		severity:          EventSeverityWarning,
		eventType:         "metric_fetch_failed",
		affectedKeys:      []string{"http_404_total", "http_4xx_total"},
		effectiveEndpoint: "/actuator/metrics/http.server.requests?tag=status%3A404",
		httpStatus:        "HTTP 404",
		actionableCause:   "missing status",
	}
	completeMessage := "effective endpoint /actuator/metrics/http.server.requests?tag=status%3A404 returned HTTP 404: missing status; affected metrics: http_404_total, http_4xx_total"
	tests := []struct {
		name  string
		event CollectorEvent
		want  bool
	}{
		{name: "complete", event: CollectorEvent{Severity: EventSeverityWarning, Type: "metric_fetch_failed", Message: completeMessage}, want: true},
		{name: "metric key must be empty", event: CollectorEvent{Severity: EventSeverityWarning, Type: "metric_fetch_failed", MetricKey: "http_404_total", Message: completeMessage}},
		{name: "missing affected metric", event: CollectorEvent{Severity: EventSeverityWarning, Type: "metric_fetch_failed", Message: "effective endpoint /actuator/metrics/http.server.requests?tag=status%3A404 returned HTTP 404: missing status; affected metric: http_404_total"}},
		{name: "missing endpoint", event: CollectorEvent{Severity: EventSeverityWarning, Type: "metric_fetch_failed", Message: "HTTP 404: missing status; affected metrics: http_404_total, http_4xx_total"}},
		{name: "missing HTTP status", event: CollectorEvent{Severity: EventSeverityWarning, Type: "metric_fetch_failed", Message: "effective endpoint /actuator/metrics/http.server.requests?tag=status%3A404: missing status; affected metrics: http_404_total, http_4xx_total"}},
		{name: "missing actionable cause", event: CollectorEvent{Severity: EventSeverityWarning, Type: "metric_fetch_failed", Message: "effective endpoint /actuator/metrics/http.server.requests?tag=status%3A404 returned HTTP 404; affected metrics: http_404_total, http_4xx_total"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := springEventMatchesConsolidation(tt.event, contract); got != tt.want {
				t.Fatalf("springEventMatchesConsolidation() = %v, want %v for event %#v", got, tt.want, tt.event)
			}
		})
	}
}
