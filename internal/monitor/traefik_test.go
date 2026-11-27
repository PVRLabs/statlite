package monitor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/prometheus"
)

type traefikTestTransport struct {
	status int
	body   string
}

func (t *traefikTestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: t.status, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader(t.body))}, nil
}

type timedTraefikCollector struct {
	*collector.TraefikCollector
	at time.Time
}

func (c *timedTraefikCollector) Collect(ctx context.Context) (*collector.CollectionResult, error) {
	r, err := c.TraefikCollector.Collect(ctx)
	c.at = c.at.Add(time.Minute)
	r.PollStartedAt, r.PollFinishedAt = c.at, c.at
	return r, err
}

func TestTraefikStoredContinuity(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	ctx := t.Context()
	transport := &traefikTestTransport{status: 200}
	client, err := prometheus.NewClientWithTransport(time.Second, prometheus.DefaultLimits, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	c := collector.NewTraefikCollector("app", "http://localhost/metrics", "alpha@file", client)
	if got := newTestMonitor(t, store, c).IntegrationType(); got != config.TargetTypeTraefik {
		t.Fatalf("integration type = %q", got)
	}
	base := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	mon := newTestMonitor(t, store, &timedTraefikCollector{TraefikCollector: c, at: base})
	seriesBody := func(success, errors int, latency bool) string {
		var body strings.Builder
		body.WriteString("process_start_time_seconds 100\ntraefik_config_reloads_total 1\n")
		for _, tuple := range []struct{ code, count int }{{200, success}, {500, errors}} {
			if tuple.count == 0 {
				continue
			}
			labels := fmt.Sprintf(`{service="alpha@file",protocol="http",method="GET",code="%d"}`, tuple.code)
			fmt.Fprintf(&body, "traefik_service_requests_total%s %d\n", labels, tuple.count)
			if latency {
				fmt.Fprintf(&body, "traefik_service_request_duration_seconds_count%s %d\ntraefik_service_request_duration_seconds_sum%s %d\n", labels, tuple.count, labels, tuple.count)
			}
		}
		return body.String()
	}
	poll := func(success, errors int, latency bool) {
		t.Helper()
		transport.body = seriesBody(success, errors, latency)
		snap, err := mon.PollNow(ctx)
		if err != nil || snap.AppRunID == nil || snap.Result.ProcessStartTime != nil {
			t.Fatalf("poll = %+v, %v", snap, err)
		}
		for _, event := range snap.Result.Events {
			if event.Type == EventTypeRestartDetected {
				t.Fatal("counter boundary emitted application restart")
			}
		}
	}
	poll(100, 0, true)
	poll(108, 2, true) // First 5xx tuple, with the same proxy and reload count.
	firstErrors, err := mon.AttentionHTTP5xx(ctx, base, base.Add(2*time.Minute))
	if err != nil || !firstErrors.Adequate || firstErrors.Requests != 10 ||
		firstErrors.Errors != 2 || firstErrors.Covered != time.Minute {
		t.Fatalf("first 5xx Attention inputs = %+v, %v", firstErrors, err)
	}
	poll(300, 1, true) // One reset hidden by growth.
	poll(308, 3, false)
	poll(316, 5, true) // Request history resumes through the latency gap.
	poll(324, 7, true)
	transport.status = 503
	if _, err := mon.PollNow(ctx); err == nil {
		t.Fatal("expected failure")
	}
	transport.status = 200
	poll(400, 20, true)
	poll(408, 22, true)
	end := base.Add(9 * time.Minute)

	for _, sampled := range []bool{false, true} {
		series, err := mon.Series(ctx, base, end)
		if sampled {
			series, err = mon.DashboardSeries(ctx, base, end, time.Minute)
		}
		if err != nil {
			t.Fatal(err)
		}
		seen := map[int]bool{}
		for _, point := range series.Points {
			minute := int(point.Timestamp.Sub(base) / time.Minute)
			if minute == 2 || minute == 4 || minute == 5 || minute == 6 || minute == 9 {
				seen[minute] = true
				if point.Requests == nil || *point.Requests != 10 || point.HTTP5xx == nil || *point.HTTP5xx != 2 {
					t.Fatalf("minute %d: invalid HTTP delta %+v", minute, point)
				}
			} else if point.Requests != nil || point.HTTP5xx != nil {
				t.Fatalf("minute %d: invalid interval produced a delta", minute)
			}
			if minute == 2 || minute == 6 || minute == 9 {
				if point.AverageLatencySeconds == nil || *point.AverageLatencySeconds != 1 {
					t.Fatalf("minute %d: invalid latency delta %+v", minute, point)
				}
			} else if point.AverageLatencySeconds != nil {
				t.Fatalf("minute %d: invalid interval produced latency", minute)
			}
		}
		if len(seen) != 5 {
			t.Fatalf("sampled=%t: missing valid traffic: %v", sampled, seen)
		}
	}
	window, err := mon.AttentionHTTP5xx(ctx, base.Add(time.Minute), end)
	if err != nil || window.Requests != 50 || window.Errors != 10 || window.Covered != 5*time.Minute {
		t.Fatalf("Attention window = %+v, %v", window, err)
	}
	window, err = mon.AttentionHTTP5xx(ctx, base.Add(3*time.Minute), base.Add(6*time.Minute))
	if err != nil || !window.Adequate || window.Requests != 30 || window.Errors != 6 {
		t.Fatalf("dense Attention window = %+v, %v", window, err)
	}
}
