//go:build integration

package storage_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/storage"
)

func TestBoundedSeriesRejectsPollOverflowBeforeSamples(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "statlite.sqlite")
	store, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterTargets(ctx, []storage.TargetIdentity{{Name: "app", Type: "spring"}}); err != nil {
		t.Fatal(err)
	}

	// Insert raw polls so this checks the query limit independently of sample loading.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmt, err := db.PrepareContext(ctx, `INSERT INTO polls (target_id, started_at, finished_at, status) VALUES ((SELECT id FROM targets WHERE name = 'app'), ?, ?, 'ok')`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	for i := 0; i < storage.PublicMetricsPollLimit; i++ {
		ts := base.Add(time.Duration(i) * time.Millisecond).Format("2006-01-02T15:04:05.999999999")
		if _, err := stmt.ExecContext(ctx, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.BoundedSeries(ctx, "app", base, base.Add(time.Minute), time.Time{}); err != nil {
		t.Fatalf("at poll limit: %v", err)
	}
	ts := base.Add(time.Duration(storage.PublicMetricsPollLimit) * time.Millisecond).Format("2006-01-02T15:04:05.999999999")
	if _, err := stmt.ExecContext(ctx, ts, ts); err != nil {
		t.Fatal(err)
	}
	_, err = store.BoundedSeries(ctx, "app", base, base.Add(time.Minute), time.Time{})
	if err == nil || !strings.Contains(err.Error(), "poll limit exceeded") {
		t.Fatalf("overflow error = %v", err)
	}
}
