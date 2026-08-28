package storage

import (
	"slices"
	"testing"
	"time"
)

func TestFormatSortableTimeSortsChronologically(t *testing.T) {
	base := time.Date(2026, 8, 28, 18, 6, 20, 0, time.UTC)
	chronological := []time.Time{
		base,
		base.Add(100 * time.Millisecond),
		base.Add(110 * time.Millisecond),
		base.Add(123 * time.Millisecond),
		base.Add(time.Second),
	}
	want := []string{
		"2026-08-28T18:06:20",
		"2026-08-28T18:06:20.1",
		"2026-08-28T18:06:20.11",
		"2026-08-28T18:06:20.123",
		"2026-08-28T18:06:21",
	}

	formatted := make([]string, len(chronological))
	for i, value := range chronological {
		formatted[i] = formatSortableTime(value)
	}
	if !slices.Equal(formatted, want) {
		t.Fatalf("formatted timestamps = %q, want %q", formatted, want)
	}
	sorted := slices.Clone(formatted)
	slices.Sort(sorted)
	if !slices.Equal(sorted, want) {
		t.Fatalf("lexically sorted timestamps = %q, want %q", sorted, want)
	}
}

func TestFormatSortableTimeConvertsToUTC(t *testing.T) {
	location := time.FixedZone("UTC-7", -7*60*60)
	value := time.Date(2026, 8, 28, 11, 6, 20, 123456789, location)
	if got, want := formatSortableTime(value), "2026-08-28T18:06:20.123456789"; got != want {
		t.Fatalf("formatSortableTime() = %q, want %q", got, want)
	}
}

func TestParseStoredTimeAcceptsNewAndLegacyRepresentations(t *testing.T) {
	want := time.Date(2026, 8, 28, 18, 6, 20, 123000000, time.UTC)
	for _, value := range []string{
		"2026-08-28T18:06:20.123",
		"2026-08-28T18:06:20.123Z",
	} {
		got, err := parseStoredTime(value)
		if err != nil {
			t.Fatalf("parseStoredTime(%q) error = %v", value, err)
		}
		if !got.Equal(want) || got.Location() != time.UTC {
			t.Fatalf("parseStoredTime(%q) = %v in %v, want %v in UTC", value, got, got.Location(), want)
		}
	}

	exact, err := parseStoredTime("2026-08-28T18:06:20Z")
	if err != nil {
		t.Fatalf("parse legacy exact-second timestamp: %v", err)
	}
	if wantExact := want.Truncate(time.Second); !exact.Equal(wantExact) {
		t.Fatalf("legacy exact-second timestamp = %v, want %v", exact, wantExact)
	}
}
