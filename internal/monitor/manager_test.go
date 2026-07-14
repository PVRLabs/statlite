package monitor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestManagerNamesAndSummariesAreDeterministic(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	alpha := newNamedTestMonitor(t, "alpha", store, &sequenceCollector{results: []collectResult{{result: namedSuccessfulResult("alpha", time.Now().UTC(), 1, 0.1)}}})
	beta := newNamedTestMonitor(t, "beta", store, &sequenceCollector{results: []collectResult{{result: namedSuccessfulResult("beta", time.Now().UTC(), 2, 0.2)}}})

	manager, err := NewManager([]ManagedTarget{
		{Metadata: TargetMetadata{Name: "alpha", Type: "spring", Endpoint: "http://alpha/actuator", EndpointSource: "actuator_base_url"}, Monitor: alpha},
		{Metadata: TargetMetadata{Name: "beta", Type: "statlite", Endpoint: "http://beta/healthz", EndpointSource: "url"}, Monitor: beta},
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	names := manager.Names()
	if !reflect.DeepEqual(names, []string{"alpha", "beta"}) {
		t.Fatalf("Names() = %#v, want alpha,beta", names)
	}
	names[0] = "mutated"
	if got := manager.Names()[0]; got != "alpha" {
		t.Fatalf("Names()[0] = %q after caller mutation, want alpha", got)
	}

	summaries := manager.Summaries()
	if len(summaries) != 2 {
		t.Fatalf("len(Summaries()) = %d, want 2", len(summaries))
	}
	if summaries[0].Metadata.Name != "alpha" || summaries[1].Metadata.Name != "beta" {
		t.Fatalf("Summaries() = %#v, want configured order", summaries)
	}
}

func TestManagerRejectsInvalidTargets(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	mon := newNamedTestMonitor(t, "alpha", store, &sequenceCollector{results: []collectResult{{result: namedSuccessfulResult("alpha", time.Now().UTC(), 1, 0.1)}}})

	tests := []struct {
		name    string
		targets []ManagedTarget
		want    string
	}{
		{name: "empty", targets: nil, want: "at least one monitor target"},
		{name: "blank name", targets: []ManagedTarget{{Metadata: TargetMetadata{Name: " "}, Monitor: mon}}, want: "name is required"},
		{name: "nil monitor", targets: []ManagedTarget{{Metadata: TargetMetadata{Name: "alpha"}}}, want: "monitor is required"},
		{name: "duplicate", targets: []ManagedTarget{
			{Metadata: TargetMetadata{Name: "alpha"}, Monitor: mon},
			{Metadata: TargetMetadata{Name: "alpha"}, Monitor: mon},
		}, want: "duplicated"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewManager(tt.targets)
			if err == nil {
				t.Fatal("NewManager() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewManager() error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestManagerPollNowRoutesToSelectedTargetAndSeparatesStorage(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	start := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	alpha := newNamedTestMonitor(t, "alpha", store, &sequenceCollector{results: []collectResult{{result: namedSuccessfulResult("alpha", start, 10, 1)}}})
	beta := newNamedTestMonitor(t, "beta", store, &sequenceCollector{results: []collectResult{{result: namedSuccessfulResult("beta", start.Add(time.Minute), 20, 2)}}})
	manager := newTestManager(t, alpha, beta)

	alphaSnapshot, err := manager.PollNow(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("PollNow(alpha) error = %v", err)
	}
	betaSnapshot, err := manager.PollNow(context.Background(), "beta")
	if err != nil {
		t.Fatalf("PollNow(beta) error = %v", err)
	}
	if alphaSnapshot.Result.TargetName != "alpha" {
		t.Fatalf("alpha snapshot target = %q, want alpha", alphaSnapshot.Result.TargetName)
	}
	if betaSnapshot.Result.TargetName != "beta" {
		t.Fatalf("beta snapshot target = %q, want beta", betaSnapshot.Result.TargetName)
	}

	storedAlpha, err := store.LatestSnapshot(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("LatestSnapshot(alpha) error = %v", err)
	}
	storedBeta, err := store.LatestSnapshot(context.Background(), "beta")
	if err != nil {
		t.Fatalf("LatestSnapshot(beta) error = %v", err)
	}
	if storedAlpha.PollID == storedBeta.PollID {
		t.Fatalf("stored poll ids both %d, want separated target polls", storedAlpha.PollID)
	}
}

func TestManagerResolveTargetPrefersRequestedThenProblemsThenFirst(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	start := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	alpha := newNamedTestMonitor(t, "alpha", store, &sequenceCollector{results: []collectResult{{result: namedSuccessfulResult("alpha", start, 1, 0.1)}}})
	beta := newNamedTestMonitor(t, "beta", store, &sequenceCollector{results: []collectResult{
		{
			result: &collector.CollectionResult{
				TargetName:     "beta",
				PollStartedAt:  start,
				PollFinishedAt: start.Add(time.Second),
				Events:         []collector.CollectorEvent{{Severity: collector.EventSeverityError, Type: "collector_failed", Message: "boom"}},
			},
			err: errors.New("boom"),
		},
	}})
	manager := newTestManager(t, alpha, beta)

	if got := manager.ResolveTarget("beta").Metadata.Name; got != "beta" {
		t.Fatalf("ResolveTarget(beta) = %q, want beta", got)
	}
	if got := manager.ResolveTarget("").Metadata.Name; got != "alpha" {
		t.Fatalf("ResolveTarget(empty) before problems = %q, want alpha", got)
	}
	if _, err := manager.PollNow(context.Background(), "beta"); err == nil {
		t.Fatal("PollNow(beta) error = nil, want collection error")
	}
	if got := manager.ResolveTarget("missing").Metadata.Name; got != "beta" {
		t.Fatalf("ResolveTarget(missing) after beta failure = %q, want beta", got)
	}
}

func newTestManager(t *testing.T, alpha, beta *Monitor) *Manager {
	t.Helper()
	manager, err := NewManager([]ManagedTarget{
		{Metadata: TargetMetadata{Name: "alpha", Type: "spring", Endpoint: "http://alpha/actuator", EndpointSource: "actuator_base_url"}, Monitor: alpha},
		{Metadata: TargetMetadata{Name: "beta", Type: "statlite", Endpoint: "http://beta/healthz", EndpointSource: "url"}, Monitor: beta},
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}

func newNamedTestMonitor(t *testing.T, name string, store *storage.Store, collector Collector) *Monitor {
	t.Helper()
	mon, err := New(name, collector, store, time.Hour)
	if err != nil {
		t.Fatalf("New(%q) error = %v", name, err)
	}
	return mon
}

func namedSuccessfulResult(targetName string, processStart time.Time, requests, requestSeconds float64) *collector.CollectionResult {
	pollStarted := processStart.Add(time.Hour)
	return &collector.CollectionResult{
		TargetName:       targetName,
		PollStartedAt:    pollStarted,
		PollFinishedAt:   pollStarted.Add(time.Second),
		HealthStatus:     "UP",
		ProcessStartTime: &processStart,
		Samples: []collector.MetricSample{
			{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: requests, Unit: "requests"},
			{Key: "http_request_time_total_seconds", Kind: collector.MetricKindCounter, Value: requestSeconds, Unit: "seconds"},
		},
	}
}
