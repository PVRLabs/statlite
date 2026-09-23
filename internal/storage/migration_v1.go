package storage

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// migrateV1ToV2 accepts only the known v1 tables and relationships. Its
// transaction rolls back if any check or schema change fails.
func migrateV1ToV2(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin v1 to v2 sqlite migration: %w", err)
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read v1 sqlite schema version: %w", err)
	}
	if version != 0 && version != 1 {
		return fmt.Errorf("cannot migrate sqlite schema version %d as v1", version)
	}

	v1Columns := map[string][]string{
		"targets":          {"id", "name", "created_at"},
		"app_runs":         {"id", "target_id", "process_start_time", "first_seen_at", "last_seen_at"},
		"polls":            {"id", "target_id", "app_run_id", "started_at", "finished_at", "status", "health_status", "db_health_status", "error_summary"},
		"metric_samples":   {"id", "poll_id", "metric_key", "metric_kind", "value", "unit"},
		"collector_events": {"id", "poll_id", "severity", "event_type", "metric_key", "message"},
	}
	for table, want := range v1Columns {
		got, err := tableColumns(ctx, tx, table)
		if err != nil {
			return fmt.Errorf("inspect v1 %s table: %w", table, err)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			return fmt.Errorf("unsupported v1 sqlite schema: %s columns are %v, expected %v", table, got, want)
		}
	}
	for table, want := range map[string][]string{
		"targets":          {},
		"app_runs":         {"target_id>targets.id:NO ACTION"},
		"polls":            {"app_run_id>app_runs.id:NO ACTION", "target_id>targets.id:NO ACTION"},
		"metric_samples":   {"poll_id>polls.id:CASCADE"},
		"collector_events": {"poll_id>polls.id:CASCADE"},
	} {
		got, err := tableForeignKeys(ctx, tx, table)
		if err != nil {
			return fmt.Errorf("inspect v1 %s foreign keys: %w", table, err)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			return fmt.Errorf("unsupported v1 sqlite schema: %s foreign keys are %v, expected %v", table, got, want)
		}
	}

	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("check v1 sqlite foreign keys: %w", err)
	}
	if rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var fkID int
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			rows.Close()
			return fmt.Errorf("read v1 sqlite foreign key violation: %w", err)
		}
		rows.Close()
		return fmt.Errorf("cannot migrate v1 sqlite database: foreign key violation in %s row %v referencing %s", table, rowID, parent)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("check v1 sqlite foreign keys: %w", err)
	}
	rows.Close()

	var mismatchedPollID int64
	err = tx.QueryRowContext(ctx, `
SELECT polls.id FROM polls
JOIN app_runs ON app_runs.id = polls.app_run_id
WHERE polls.target_id <> app_runs.target_id
LIMIT 1`).Scan(&mismatchedPollID)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("check v1 poll and app-run targets: %w", err)
	}
	if err == nil {
		return fmt.Errorf("cannot migrate v1 sqlite database: poll %d references an app run for a different target", mismatchedPollID)
	}

	for _, change := range []string{
		"ALTER TABLE targets ADD COLUMN target_type TEXT",
		"ALTER TABLE app_runs ADD COLUMN target_type TEXT",
		"ALTER TABLE polls ADD COLUMN metrics_source TEXT",
	} {
		if _, err := tx.ExecContext(ctx, change); err != nil {
			return fmt.Errorf("migrate v1 sqlite schema to v2: %w", err)
		}
	}
	if err := checkV2Columns(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 2"); err != nil {
		return fmt.Errorf("set sqlite schema version 2: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit v1 to v2 sqlite migration: %w", err)
	}
	return nil
}

type schemaQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func checkV2Columns(ctx context.Context, db schemaQueryer) error {
	for _, item := range []struct{ table, column string }{
		{"targets", "target_type"},
		{"app_runs", "target_type"},
		{"polls", "metrics_source"},
	} {
		columns, err := tableColumns(ctx, db, item.table)
		if err != nil {
			return fmt.Errorf("inspect v2 %s table: %w", item.table, err)
		}
		found := false
		for _, name := range columns {
			if name == item.column {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unsupported v2 sqlite schema: %s.%s is missing", item.table, item.column)
		}
	}
	return nil
}

func tableColumns(ctx context.Context, db schemaQueryer, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func tableForeignKeys(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_list("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var id, sequence int
		var parent, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		keys = append(keys, from+">"+parent+"."+to+":"+onDelete)
	}
	return keys, rows.Err()
}
