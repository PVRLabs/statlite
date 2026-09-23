package storage

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

func TestRegisterTargetsPreservesIdentityAndRollsBackConflict(t *testing.T) {
	store, err := Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	first := []TargetIdentity{{Name: "old", Type: "spring"}, {Name: "stable", Type: "quarkus"}}
	if err := store.RegisterTargets(ctx, first); err != nil {
		t.Fatal(err)
	}
	var oldID int64
	if err := store.db.QueryRow(`SELECT id FROM targets WHERE name = 'old'`).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: "stable", Type: "quarkus"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: "new", Type: "spring"}, {Name: "stable", Type: "spring"}}); err == nil {
		t.Fatal("conflicting registration succeeded")
	} else {
		for _, want := range []string{`"stable"`, `"quarkus"`, `"spring"`, "restore the previous type", "use a new target name", "start with a new database"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("registration error %q missing %q", err, want)
			}
		}
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM targets WHERE name = 'new'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partially registered new name: count=%d err=%v", count, err)
	}
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: "old", Type: "spring"}, {Name: "renamed", Type: "spring"}}); err != nil {
		t.Fatal(err)
	}
	var readdedID, renamedID int64
	if err := store.db.QueryRow(`SELECT id FROM targets WHERE name = 'old'`).Scan(&readdedID); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT id FROM targets WHERE name = 'renamed'`).Scan(&renamedID); err != nil {
		t.Fatal(err)
	}
	if readdedID != oldID || renamedID == oldID {
		t.Fatalf("readded/renamed IDs = %d/%d, original=%d", readdedID, renamedID, oldID)
	}
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: "old", Type: "quarkus"}}); err == nil {
		t.Fatal("readded name accepted changed type")
	}
}

func TestRegisteredTargetRequiredForWrites(t *testing.T) {
	store, err := Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	if _, err := store.EnsureAppRun(t.Context(), "app", nil, at); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}
	if _, err := store.SaveCollectionResult(t.Context(), &collector.CollectionResult{TargetName: "app", PollStartedAt: at, PollFinishedAt: at}); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("SaveCollectionResult() error = %v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM targets`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("writes created targets: count=%d err=%v", count, err)
	}
}

func TestAppRunTypeOnlyOnNewRowsAfterLegacyBinding(t *testing.T) {
	path := createV1Database(t, 1)
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: "second", Type: "quarkus"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: "first", Type: "spring"}, {Name: "second", Type: "spring"}}); err == nil {
		t.Fatal("legacy binding was partially committed despite later conflict")
	}
	var unbound sql.NullString
	if err := store.db.QueryRow(`SELECT target_type FROM targets WHERE id = 7`).Scan(&unbound); err != nil || unbound.Valid {
		t.Fatalf("legacy target partially bound: type=%v err=%v", unbound, err)
	}
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: "second", Type: "quarkus"}, {Name: "first", Type: "spring"}}); err != nil {
		t.Fatal(err)
	}
	var targetType, legacyType sql.NullString
	if err := store.db.QueryRow(`SELECT target_type FROM targets WHERE id = 7`).Scan(&targetType); err != nil || targetType.String != "spring" {
		t.Fatalf("bound target type = %v, err=%v", targetType, err)
	}
	if err := store.db.QueryRow(`SELECT target_type FROM app_runs WHERE id = 19`).Scan(&legacyType); err != nil || legacyType.Valid {
		t.Fatalf("legacy app run type = %v, err=%v", legacyType, err)
	}
	legacyStart := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	seenAt := legacyStart.Add(time.Hour)
	id, err := store.EnsureAppRun(ctx, "first", &legacyStart, seenAt)
	if err != nil || id != 19 {
		t.Fatalf("upsert legacy app run = %d, err=%v", id, err)
	}
	if _, err := store.SaveCollectionResult(ctx, &collector.CollectionResult{
		TargetName: "first", PollStartedAt: seenAt, PollFinishedAt: seenAt, ProcessStartTime: &legacyStart,
	}); err != nil {
		t.Fatalf("poll using legacy app run: %v", err)
	}
	newStart := legacyStart.Add(2 * time.Hour)
	newID, err := store.EnsureAppRun(ctx, "first", &newStart, newStart)
	if err != nil {
		t.Fatal(err)
	}
	anonID, err := store.EnsureAppRun(ctx, "second", nil, seenAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		id    int64
		want  string
		valid bool
	}{{19, "", false}, {newID, "spring", true}, {anonID, "quarkus", true}} {
		var got sql.NullString
		if err := store.db.QueryRow(`SELECT target_type FROM app_runs WHERE id = ?`, check.id).Scan(&got); err != nil || got.Valid != check.valid || (got.Valid && got.String != check.want) {
			t.Fatalf("app run %d type = %v, err=%v; want %q valid=%v", check.id, got, err, check.want, check.valid)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: "first", Type: "spring"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT target_type FROM app_runs WHERE id = 19`).Scan(&legacyType); err != nil || legacyType.Valid {
		t.Fatalf("legacy app run type after restart = %v, err=%v", legacyType, err)
	}
}

func TestPollMetricsSourceRoundTripsWithoutChangingTargetIdentity(t *testing.T) {
	store, err := Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	const targetName = "spring-source"
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: targetName, Type: "spring"}}); err != nil {
		t.Fatal(err)
	}

	for i, source := range []string{"prometheus", "actuator", ""} {
		startedAt := time.Date(2026, 9, 23, 12, i, 0, 0, time.UTC)
		pollID, err := store.SaveCollectionResult(ctx, &collector.CollectionResult{
			TargetName: targetName, MetricsSource: source,
			PollStartedAt: startedAt, PollFinishedAt: startedAt.Add(time.Second),
		})
		if err != nil {
			t.Fatalf("SaveCollectionResult(%q): %v", source, err)
		}
		var stored sql.NullString
		if err := store.db.QueryRowContext(ctx, `SELECT metrics_source FROM polls WHERE id = ?`, pollID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored.Valid != (source != "") || (stored.Valid && stored.String != source) {
			t.Fatalf("stored source = %v, want %q", stored, source)
		}
		snapshot, err := store.LatestSnapshot(ctx, targetName)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Result.MetricsSource != source {
			t.Fatalf("read-back source = %q, want %q", snapshot.Result.MetricsSource, source)
		}
	}
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: targetName, Type: "spring"}}); err != nil {
		t.Fatalf("re-register unchanged integration after source changes: %v", err)
	}
}
