package collector

import (
	"compress/gzip"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

func traefikTestCollector(t testing.TB, body *string, status *int, service string) *TraefikCollector {
	t.Helper()
	client, err := prometheus.NewClientWithTransport(time.Second, prometheus.DefaultLimits, nil, scrapeTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: *status, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader(*body))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return NewTraefikCollector("app", "http://localhost/metrics", service, client)
}

func traefikRecordedScrape(t testing.TB, name string) string {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", "traefik", name+".prom.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestTraefikRecordedDeltas(t *testing.T) {
	for _, tt := range []struct {
		name, service string
		want          [4]float64
		duration      float64
		breaks        bool
	}{
		{"mixed", "alpha@file", [4]float64{10, 0, 0, 2}, 0.004875828, false},
		{"mixed", "beta@file", [4]float64{}, 0, false},
		{"scrapes-only", "alpha@file", [4]float64{}, 0, false},
		{"unmatched-404", "alpha@file", [4]float64{}, 0, false},
		{"empty-pool", "beta@file", [4]float64{}, 0, false},
		{"timeout", "alpha@file", [4]float64{3, 0, 0, 3}, 1.203918923, false},
		{"unavailable", "beta@file", [4]float64{3, 0, 0, 3}, 0.000597994, false},
		{"after-restart-traffic", "alpha@file", [4]float64{4, 0, 0, 1}, 0.002178396, false},
	} {
		t.Run(tt.name+"/"+tt.service, func(t *testing.T) {
			body, status := traefikRecordedScrape(t, tt.name+"-before"), 200
			c := traefikTestCollector(t, &body, &status, tt.service)
			before := collectTraefikOK(t, c, true, 5)
			body = traefikRecordedScrape(t, tt.name)
			after := collectTraefikOK(t, c, tt.breaks, 5)
			for i, want := range tt.want {
				if delta := after.Samples[i].Value - before.Samples[i].Value; delta != want {
					t.Fatalf("%s delta = %g, want %g", after.Samples[i].Key, delta, want)
				}
			}
			duration := after.Samples[4].Value - before.Samples[4].Value
			if math.Abs(duration-tt.duration) > 1e-9 {
				t.Fatalf("unexpected duration delta %g", duration)
			}
		})
	}
}

func collectTraefikOK(t *testing.T, c *TraefikCollector, broken bool, samples int) *CollectionResult {
	t.Helper()
	r, err := c.Collect(t.Context())
	if err != nil || r.CounterContinuityBreak != broken || len(r.Samples) != samples ||
		!r.TracksCounterContinuity || r.ProcessStartTime != nil {
		t.Fatalf("collect = %+v, err = %v, want break %t and %d samples", r, err, broken, samples)
	}
	return r
}

func traefikTestTuple(service, code string, count int, sum float64) string {
	labels := fmt.Sprintf(`{service=%q,code=%q,protocol="http",method="GET"}`, service, code)
	reordered := fmt.Sprintf(`{code=%q,method="GET",protocol="http",service=%q}`, code, service)
	return fmt.Sprintf("%s%s %d\n%s%s %d\n%s%s %g\n", traefikRequests, labels, count, traefikCount, labels, count, traefikSum, reordered, sum)
}

func TestTraefikContinuity(t *testing.T) {
	const a, b = "alpha@file", "beta@file"
	pair := func(success, failure int) string {
		return traefikTestTuple(a, "200", success, float64(success)) + traefikTestTuple(a, "500", failure, float64(failure))
	}
	body, status := pair(100, 100), 200
	c := traefikTestCollector(t, &body, &status, a)
	collectTraefikOK(t, c, true, 5)
	body = pair(300, 1) // Reset hidden by growth in the other tuple.
	collectTraefikOK(t, c, true, 5)
	body = pair(301, 1) + traefikTestTuple(a, "404", 0, 0)
	collectTraefikOK(t, c, false, 5)
	body = pair(302, 1) + traefikTestTuple(a, "404", 1, 1) + traefikTestTuple(a, "502", 8, 8)
	collectTraefikOK(t, c, true, 5)
	body = pair(303, 1)
	collectTraefikOK(t, c, true, 5)
	body += traefikTestTuple(a, "502", 8, 8)
	collectTraefikOK(t, c, true, 5)
	status = 503
	if r, err := c.Collect(t.Context()); err == nil || len(r.Samples) != 0 {
		t.Fatalf("failed scrape = %+v, %v", r, err)
	}
	status = 200
	collectTraefikOK(t, c, true, 5)
	collectTraefikOK(t, c, false, 5)
	for _, proxyStart := range []int{100, 200} {
		body = fmt.Sprintf("process_start_time_seconds %d\n", proxyStart) + pair(500, 500)
		collectTraefikOK(t, c, true, 5)
	}
	for _, reloads := range []int{1, 2} {
		body = fmt.Sprintf("process_start_time_seconds 200\ntraefik_config_reloads_total %d\n", reloads) + pair(500, 500)
		collectTraefikOK(t, c, true, 5)
	}
	collectTraefikOK(t, c, false, 5)
	// Verify the scope check independently of the change of label tuple keys.
	next := c.previous
	next.scope = b
	if !c.previous.breaksContinuity(next, true) {
		t.Fatal("scope change did not break continuity")
	}
}

func TestTraefikRecordedReloadAndRestart(t *testing.T) {
	body, status := traefikRecordedScrape(t, "before-service-change"), 200
	c := traefikTestCollector(t, &body, &status, "beta@file")
	collectTraefikOK(t, c, true, 5)
	body = traefikRecordedScrape(t, "after-service-change")
	collectTraefikOK(t, c, true, 5) // The reload is visible before the old series disappear.
	body = traefikRecordedScrape(t, "after-reload-traffic-before")
	if r, err := c.Collect(t.Context()); err == nil || !strings.Contains(err.Error(), "HTTP request metrics not observed") ||
		len(r.Samples) != 0 {
		t.Fatalf("removed scope = %+v, %v", r, err)
	}
	c.service = "beta-new@file"
	body = traefikRecordedScrape(t, "after-reload-traffic")
	collectTraefikOK(t, c, true, 5)
	c.service = "alpha@file"
	collectTraefikOK(t, c, true, 5)
	body = traefikRecordedScrape(t, "after-restart-traffic-before")
	collectTraefikOK(t, c, true, 5)
	body = traefikRecordedScrape(t, "after-restart-traffic")
	collectTraefikOK(t, c, false, 5) // Stable identity and reload count establish zero origin.
	collectTraefikOK(t, c, false, 5)
}

func TestTraefikPartialHistogram(t *testing.T) {
	const service = "alpha@file"
	valid := traefikTestTuple(service, "200", 10, 5)
	for _, tt := range []struct{ name, body string }{
		{"missing", strings.SplitN(valid, "\n", 2)[0] + "\n"},
		{"different full labels", strings.Replace(valid, `{code="200",method=`, `{code="200",extra="value",method=`, 1)},
		{"different count", strings.Replace(valid, "} 10\n"+traefikSum, "} 9\n"+traefikSum, 1)},
		{"duration dropped", traefikTestTuple(service, "200", 11, 4)},
		{"sum increased without observations", traefikTestTuple(service, "200", 10, 6)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body, status := valid, 200
			c := traefikTestCollector(t, &body, &status, service)
			collectTraefikOK(t, c, true, 5)
			body = tt.body
			r := collectTraefikOK(t, c, false, 4)
			if len(r.Events) != 1 || r.Events[0].Severity != EventSeverityWarning || c.previousDurations != nil {
				t.Fatalf("partial result = %+v", r)
			}
		})
	}
}

func TestTraefikBoundsAndUnknownStatus(t *testing.T) {
	var input strings.Builder
	for i := range traefikMaxTuples {
		input.WriteString(strings.ReplaceAll(traefikTestTuple("alpha@file", "0", 1, 1), `"GET"`, fmt.Sprintf("%q", fmt.Sprint(i))))
	}
	body, status := input.String(), 200
	c := traefikTestCollector(t, &body, &status, "alpha@file")
	r := collectTraefikOK(t, c, true, 5)
	if len(c.previous.tuples) != traefikMaxTuples || len(c.previousDurations) != traefikMaxTuples ||
		r.Samples[0].Value != traefikMaxTuples || r.Samples[3].Value != 0 || len(r.Events) != 1 {
		t.Fatalf("unexpected bounded result %+v", r)
	}
	extra := traefikTestTuple("alpha@file", "200", 1, 1)
	lines := strings.SplitN(extra, "\n", 2)
	body += lines[1]
	collectTraefikOK(t, c, false, 4)
	if c.previousDurations != nil || len(c.previous.tuples) != traefikMaxTuples {
		t.Fatal("optional histogram overflow affected request state")
	}
	body += lines[0] + "\n"
	if r, err := c.Collect(t.Context()); err == nil || len(r.Samples) != 0 || c.previous.tuples != nil {
		t.Fatalf("overflow result = %+v, %v", r, err)
	}
	body = input.String()
	collectTraefikOK(t, c, true, 5)
}

func BenchmarkTraefikRecordedScrape(b *testing.B) {
	body, status := traefikRecordedScrape(b, "mixed"), 200
	c := traefikTestCollector(b, &body, &status, "alpha@file")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.Collect(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

func TestTraefikNewPositiveTupleRequiresStableIdentity(t *testing.T) {
	const start = "process_start_time_seconds 100\n"
	const reload = "traefik_config_reloads_total 1\n"
	const service = "alpha@file"
	for _, tc := range []struct {
		name, before, after string
		breaks              bool
	}{
		{"stable", start + reload, start + reload, false},
		{"no proxy identity", reload, reload, true},
		{"invalid proxy identity", "process_start_time_seconds 0\n" + reload, "process_start_time_seconds 0\n" + reload, true},
		{"no reload count", start, start, true},
		{"duplicate reload count", start + reload + reload, start + reload + reload, true},
		{"reload count appears", start, start + reload, true},
		{"reloaded", start + reload, start + "traefik_config_reloads_total 2\n", true},
		{"restarted", start + reload, "process_start_time_seconds 200\n" + reload, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, status := tc.before+traefikTestTuple(service, "200", 100, 10), 200
			c := traefikTestCollector(t, &body, &status, service)
			collectTraefikOK(t, c, true, 5)
			body = tc.after + traefikTestTuple(service, "200", 108, 10.8) + traefikTestTuple(service, "500", 2, 0.2)
			collectTraefikOK(t, c, tc.breaks, 5)
		})
	}
}
