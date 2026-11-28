package monitor

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/prometheus"
)

func TestFastAPIStoredRestartAndLatencyGap(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	payload := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, <-payload)
	}))
	defer server.Close()
	client, err := prometheus.NewClient(time.Second, collector.DirectRoutePrometheusLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := collector.NewFastAPICollector("app", server.URL+"/metrics", client)
	mon := newTestMonitor(t, store, c)
	start := time.Now().Add(-time.Second)
	poll := func(identity, count, durationCount int, wantRestart bool) int64 {
		t.Helper()
		payload <- fmt.Sprintf(`http_request_duration_highr_seconds_count 100
http_request_duration_highr_seconds_sum 10
process_start_time_seconds %d
http_requests_total{handler="/ok",method="GET",status="200"} %d
http_request_duration_seconds_count{handler="/ok",method="GET"} %d
http_request_duration_seconds_sum{handler="/ok",method="GET"} %d
`, identity, count, durationCount, durationCount)
		snap, err := mon.PollNow(t.Context())
		if err != nil || snap.AppRunID == nil {
			t.Fatalf("poll = %+v, %v", snap, err)
		}
		restarted := false
		for _, event := range snap.Result.Events {
			restarted = restarted || event.Type == EventTypeRestartDetected
		}
		if restarted != wantRestart {
			t.Fatalf("restart = %v, want %v", restarted, wantRestart)
		}
		return *snap.AppRunID
	}
	before := poll(100, 10, 10, false)
	if poll(100, 11, 10, false) != before || poll(100, 12, 12, false) != before {
		t.Fatal("duration gap broke request continuity")
	}
	after := poll(200, 30, 30, true)
	if after == before || poll(200, 33, 33, false) != after {
		t.Fatal("unexpected process restart boundary")
	}
	series, err := mon.Series(t.Context(), start, time.Now().Add(time.Second))
	if err != nil || len(series.Points) != 5 {
		t.Fatalf("series = %+v, %v", series, err)
	}
	for i, point := range series.Points {
		switch i {
		case 0, 3:
			if point.Requests != nil || point.AverageLatencySeconds != nil {
				t.Fatalf("boundary has HTTP deltas: %+v", point)
			}
		case 1, 2:
			if point.Requests == nil || *point.Requests != 1 || point.AverageLatencySeconds != nil {
				t.Fatalf("invalid latency interval: %+v", point)
			}
		case 4:
			if point.Requests == nil || *point.Requests != 3 || point.AverageLatencySeconds == nil || *point.AverageLatencySeconds != 1 {
				t.Fatalf("recovered interval: %+v", point)
			}
		}
	}
}

func TestFastAPIIdlePollsReuseAnonymousRun(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	counts := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := <-counts
		fmt.Fprintf(w, "http_request_duration_highr_seconds_count %d\nhttp_request_duration_highr_seconds_sum %d\n", count, count)
		if count > 0 {
			fmt.Fprintf(w, "http_requests_total{handler=\"/ok\",method=\"GET\",status=\"200\"} %d\n", count)
		}
	}))
	defer server.Close()

	client, err := prometheus.NewClient(time.Second, collector.DirectRoutePrometheusLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mon := newTestMonitor(t, store, collector.NewFastAPICollector("app", server.URL+"/metrics", client))
	var previous int64
	for _, step := range []struct {
		name   string
		count  int
		newRun bool
	}{
		{"first idle poll", 0, true},
		{"repeated idle poll", 0, false},
		{"first HTTP observation", 10, true},
		{"continued HTTP observations", 12, false},
		{"HTTP population disappears", 0, true},
		{"population stays absent", 0, false},
		{"HTTP population returns", 15, true},
		{"HTTP observations resume", 18, false},
	} {
		counts <- step.count
		snap, err := mon.PollNow(t.Context())
		if err != nil || snap.AppRunID == nil {
			t.Fatalf("%s: poll = %+v, %v", step.name, snap, err)
		}
		if current := *snap.AppRunID; (current != previous) != step.newRun {
			t.Fatalf("%s: run changed from %d to %d, want change=%v", step.name, previous, current, step.newRun)
		}
		previous = *snap.AppRunID
	}
}
