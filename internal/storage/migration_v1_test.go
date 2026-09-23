package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func createV1Database(t *testing.T, version int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "statlite.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	schema, err := os.ReadFile("testdata/schema_v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO targets (id, name, created_at) VALUES (7, 'first', '2026-08-28T00:00:00Z'), (11, 'second', '2026-08-28T00:00:00Z')`,
		`INSERT INTO app_runs (id, target_id, process_start_time, first_seen_at, last_seen_at) VALUES (19, 7, '2026-08-28T00:00:00Z', '2026-08-28T00:00:01Z', '2026-08-28T00:00:02Z')`,
		`INSERT INTO polls (id, target_id, app_run_id, started_at, finished_at, status) VALUES (23, 7, 19, '2026-08-28T00:00:01Z', '2026-08-28T00:00:02Z', 'ok')`,
		`INSERT INTO metric_samples (id, poll_id, metric_key, metric_kind, value) VALUES (29, 23, 'requests', 'counter', 42)`,
		`INSERT INTO collector_events (id, poll_id, severity, event_type, message) VALUES (31, 23, 'warning', 'partial', 'old event')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = " + strconv.Itoa(version)); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenMigratesVersionedV1WithoutChangingHistory(t *testing.T) {
	path := createV1Database(t, 1)
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("version = %d, error = %v; want 2", version, err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT id FROM targets WHERE name = 'first'`, 7},
		{`SELECT id FROM targets WHERE name = 'second'`, 11},
		{`SELECT id FROM app_runs WHERE target_id = 7`, 19},
		{`SELECT id FROM polls WHERE target_id = 7 AND app_run_id = 19`, 23},
		{`SELECT id FROM metric_samples WHERE poll_id = 23 AND value = 42`, 29},
		{`SELECT id FROM collector_events WHERE poll_id = 23 AND message = 'old event'`, 31},
	} {
		var got int64
		if err := store.db.QueryRow(check.query).Scan(&got); err != nil || got != check.want {
			t.Fatalf("%s: got %d, error %v; want %d", check.query, got, err, check.want)
		}
	}
	var targetType, appRunType, metricsSource sql.NullString
	if err := store.db.QueryRow(`
SELECT targets.target_type, app_runs.target_type, polls.metrics_source
FROM targets JOIN app_runs ON app_runs.target_id = targets.id
JOIN polls ON polls.app_run_id = app_runs.id
WHERE polls.id = 23`).Scan(&targetType, &appRunType, &metricsSource); err != nil {
		t.Fatal(err)
	}
	if targetType.Valid || appRunType.Valid || metricsSource.Valid {
		t.Fatalf("historical provenance = %v/%v/%v; want all NULL", targetType, appRunType, metricsSource)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen migrated v2: %v", err)
	}
	reopened.Close()
}

func TestOpenRejectsMismatchedLegacyPollWithoutMigration(t *testing.T) {
	path := createV1Database(t, 1)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE polls SET target_id = 11 WHERE id = 23`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "different target") {
		t.Fatalf("Open() error = %v; want target mismatch", err)
	}
	assertUnmigratedV1(t, path, 1)
}

func TestOpenRejectsLegacyForeignKeyViolationWithoutMigration(t *testing.T) {
	path := createV1Database(t, 1)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE metric_samples SET poll_id = 999 WHERE id = 29`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "foreign key violation") {
		t.Fatalf("Open() error = %v; want foreign key violation", err)
	}
	assertUnmigratedV1(t, path, 1)
}

func TestOpenRejectsIncompatibleUnversionedShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE targets (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "unsupported v1 sqlite schema") {
		t.Fatalf("Open() error = %v; want incompatible shape", err)
	}
	assertUnmigratedV1(t, path, 0)
}

func TestOpenRejectsFutureAndBrokenV2(t *testing.T) {
	for _, tc := range []struct{ name, sql, want string }{
		{"future", "PRAGMA user_version = 3", "newer than supported"},
		{"broken-v2", "PRAGMA user_version = 2", "targets.target_type is missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := createV1Database(t, 1)
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			db.Close()
			_, err = Open(context.Background(), path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Open() error = %v; want %q", err, tc.want)
			}
		})
	}
}

func assertUnmigratedV1(t *testing.T, path string, wantVersion int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != wantVersion {
		t.Fatalf("version = %d, error = %v; want %d", version, err, wantVersion)
	}
	rows, err := db.Query(`PRAGMA table_info(targets)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "target_type" {
			t.Fatal("migration changed target table despite failed open")
		}
	}
}
