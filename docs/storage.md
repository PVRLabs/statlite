# Storage and schema direction

StatLite persists its monitoring history in SQLite. The storage contract favors
a small runtime footprint, inspectable data, and explicit evolution over a
general-purpose abstraction or migration system.

## Schema versioning

StatLite uses SQLite `PRAGMA user_version` to record the schema version. The
current schema version is **1**, and `currentSchemaVersion` in Go is the source
of truth for the version understood by the application.

Version 0 identifies databases created before explicit schema versioning.
StatLite can adopt those existing databases as version 1 without rewriting
their data because version 1 represents the pre-versioning schema.

There is intentionally no general migration framework yet. When the first
actual schema change is required, StatLite will increment
`currentSchemaVersion` and introduce an explicit version-to-version migration.

## Timestamp storage

Chronological timestamps are stored as human-readable SQLite `TEXT`. New
sortable values use an implicit-UTC representation with no timezone suffix and
only the fractional digits needed, for example:

```text
2026-08-28T18:06:20
2026-08-28T18:06:20.1
2026-08-28T18:06:20.123456789
```

The missing timezone suffix does not mean local time. Timestamp values without
an offset in StatLite's SQLite storage are always UTC. Omitting the suffix lets
variable-width fractional seconds remain chronologically sortable as plain
SQLite text.

Legacy rows may contain RFC3339 or RFC3339Nano values ending in `Z`. Reads
remain compatible with both the legacy and implicit-UTC representations, and
existing rows are not migrated. Mixed old and new values within the same second
can therefore have historical ordering or range-boundary edge cases. StatLite
knowingly accepts those cases instead of normalizing queries or rewriting data.

Timestamps used as logical identity may retain their established representation
to preserve equality with existing rows. In particular,
`app_runs.process_start_time` continues to use its legacy RFC3339Nano UTC
representation.

## Future direction

The current text representation is a pragmatic choice, not necessarily the
permanent database model. If StatLite later needs stronger schema evolution,
larger datasets, more sophisticated querying, or a general migration, it may be
appropriate to store timestamps as Unix milliseconds or microseconds in SQLite
`INTEGER` columns.

Any such conversion should be an explicit future schema migration. StatLite
should not incrementally mix integer and text representations in the same
storage contract.
