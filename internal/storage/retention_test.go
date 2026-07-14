package storage

// This file verifies retention cleanup behavior against SQLite storage.

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

func TestStartRetentionCleanupSkipsDisabledRetention(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "statlite.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	oldStart := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	saveRetentionTestPoll(t, store, oldStart)

	StartRetentionCleanup(ctx, store, 0, nil)

	series, err := store.Series(ctx, "app", oldStart.Add(-time.Hour), oldStart.Add(time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 1 {
		t.Fatalf("series points = %d, want old poll retained when cleanup is disabled", len(series.Points))
	}
}

func TestCleanupRetentionDeletesExpiredPolls(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "statlite.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	oldStart := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	retainedStart := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	saveRetentionTestPoll(t, store, oldStart)
	saveRetentionTestPoll(t, store, retainedStart)

	var trackedCutoff time.Time
	cleanupRetention(ctx, store, 90, func() time.Time {
		return retainedStart
	}, func(cutoff time.Time) {
		trackedCutoff = cutoff
	})
	wantCutoff := retainedStart.AddDate(0, 0, -90)
	if !trackedCutoff.Equal(wantCutoff) {
		t.Fatalf("tracked cutoff = %v, want %v", trackedCutoff, wantCutoff)
	}

	series, err := store.Series(ctx, "app", oldStart.Add(-time.Hour), retainedStart.Add(time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 1 {
		t.Fatalf("series points = %d, want only retained poll", len(series.Points))
	}
	if !series.Points[0].Timestamp.Equal(retainedStart) {
		t.Fatalf("retained timestamp = %v, want %v", series.Points[0].Timestamp, retainedStart)
	}
}

func TestCleanupRetentionDoesNotLogWhenNothingDeleted(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "statlite.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	retainedStart := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	saveRetentionTestPoll(t, store, retainedStart)

	var logs bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	cleanupRetention(ctx, store, 90, func() time.Time {
		return retainedStart
	}, nil)

	if strings.Contains(logs.String(), "retention cleanup deleted 0 poll(s)") {
		t.Fatalf("cleanupRetention() log = %q, want no zero-delete retention cleanup log", logs.String())
	}
}

func saveRetentionTestPoll(t *testing.T, store *Store, startedAt time.Time) {
	t.Helper()
	result := &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  startedAt,
		PollFinishedAt: startedAt.Add(time.Second),
		HealthStatus:   "UP",
		Samples: []collector.MetricSample{
			{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: float64(startedAt.Unix()), Unit: "requests"},
		},
	}
	if _, err := store.SaveCollectionResult(context.Background(), result); err != nil {
		t.Fatalf("SaveCollectionResult(%s) error = %v", startedAt, err)
	}
}
