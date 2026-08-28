package storage

// This file provides SQLite value conversion and timestamp formatting helpers.

import "time"

const sortableTimeLayout = "2006-01-02T15:04:05.999999999"

func nullableString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt64(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

// formatSortableTime formats chronological SQLite TEXT values as implicit UTC.
// Omitting a suffix keeps minimal, variable-width fractions lexically sortable.
func formatSortableTime(value time.Time) string {
	return value.UTC().Format(sortableTimeLayout)
}

// formatIdentityTime preserves the historical representation used by values
// that participate in equality and uniqueness, notably process_start_time.
func formatIdentityTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

// parseStoredTime accepts both new implicit-UTC values and legacy RFC3339 values.
func parseStoredTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(sortableTimeLayout, value); err == nil {
		return parsed, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}
