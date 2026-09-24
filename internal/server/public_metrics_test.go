package server

import (
	"context"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestReadPublicMetricsClampsRetentionAndClearsFirstCounterDelta(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registerServerTestTargets(t, store)
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	run, err := store.EnsureAppRun(t.Context(), "app", nil, base)
	if err != nil {
		t.Fatal(err)
	}
	saveServerRetentionPoll(t, store, run, base, 10, nil)
	saveServerRetentionPoll(t, store, run, base.Add(20*time.Minute), 20, nil)
	saveServerRetentionPoll(t, store, run, base.Add(30*time.Minute), 30, nil)
	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	cutoff := base.Add(15 * time.Minute)
	s := NewWithManagerRetentionCutoff("127.0.0.1:0", mustSingleServerTestManager(t, mon), 1, func() time.Time { return cutoff })
	got, err := s.readPublicMetrics(context.Background(), monitor.ManagedTarget{Monitor: mon}, base.Add(40*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 1 {
		t.Fatalf("points = %#v", got.Points)
	}
	if !got.Points[0].Timestamp.Equal(base.Add(30 * time.Minute)) {
		t.Fatalf("timestamp = %v", got.Points[0].Timestamp)
	}
	if got.Points[0].Requests == nil || *got.Points[0].Requests != 10 {
		t.Fatalf("requests = %v, want 10", got.Points[0].Requests)
	}
	if got.LatestHTTPObservationAt == nil || !got.LatestHTTPObservationAt.Equal(base.Add(30*time.Minute)) {
		t.Fatalf("HTTP freshness = %v", got.LatestHTTPObservationAt)
	}
}

func TestReadPublicMetricsRejectsPreCutoffBaselineAfterResourceOnlyPoll(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registerServerTestTargets(t, store)
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	run, err := store.EnsureAppRun(t.Context(), "app", nil, base)
	if err != nil {
		t.Fatal(err)
	}
	saveServerRetentionPoll(t, store, run, base, 100, nil)
	resourceAt := base.Add(20 * time.Minute)
	resource := &collector.CollectionResult{
		TargetName: "app", PollStartedAt: resourceAt, PollFinishedAt: resourceAt.Add(time.Second),
		Samples: []collector.MetricSample{{Key: "process_cpu_usage", Kind: collector.MetricKindGauge, Value: .4, Unit: "cores"}},
	}
	if _, err := store.SaveCollectionResultWithAppRun(t.Context(), resource, &run); err != nil {
		t.Fatal(err)
	}
	saveServerRetentionPoll(t, store, run, base.Add(30*time.Minute), 150, nil)
	saveServerRetentionPoll(t, store, run, base.Add(40*time.Minute), 170, nil)
	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	cutoff := base.Add(15 * time.Minute)
	s := NewWithManagerRetentionCutoff("127.0.0.1:0", mustSingleServerTestManager(t, mon), 1, func() time.Time { return cutoff })
	got, err := s.readPublicMetrics(context.Background(), monitor.ManagedTarget{Monitor: mon}, base.Add(50*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 2 {
		t.Fatalf("points = %#v, want resource slot and later request slot", got.Points)
	}
	if !got.Points[0].Timestamp.Equal(resourceAt) || got.Points[0].Requests != nil {
		t.Fatalf("resource slot = %#v", got.Points[0])
	}
	if !got.Points[1].Timestamp.Equal(base.Add(40*time.Minute)) || got.Points[1].Requests == nil || *got.Points[1].Requests != 20 {
		t.Fatalf("request slot = %#v, want only post-cutoff delta 20", got.Points[1])
	}
	if got.LatestHTTPObservationAt == nil || !got.LatestHTTPObservationAt.Equal(base.Add(40*time.Minute)) {
		t.Fatalf("HTTP freshness = %v", got.LatestHTTPObservationAt)
	}
}
