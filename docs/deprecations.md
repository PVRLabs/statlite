# Deprecations and compatibility

This document is the inventory of deprecated StatLite configuration, storage,
and API surfaces. It also tracks legacy persisted data that StatLite still
reads. User guides mention only the replacement needed for current setups;
compatibility behavior and eventual removal work belong here.

Deprecated behavior remains supported until a documented breaking release.
New configurations and code should use the replacement rather than relying on
a compatibility migration.

## Current deprecations

| Surface | Deprecated since | Replacement | Planned removal |
|---|---|---|---|
| Spring target field `actuator_base_url` | `v0.4.0` | `url` | Future breaking release |
| Target type `statlite` | `v0.2.0` | `statlite-metrics` | Future breaking release |

The release is the first version that warns about or documents the surface as
deprecated. `v0.4.0` is currently unreleased.

## Configuration

### `actuator_base_url`

**Replacement:** `url`

For Spring targets, StatLite accepts `actuator_base_url`, logs a deprecation
warning, and treats it as `url`. Configuring both fields is an error, even when
their values are identical.

Embedded credentials remain supported only through this compatibility path. A
credential-bearing `actuator_base_url` forces Actuator metrics when
`metrics_source` is omitted or set to `auto`; `metrics_source: "prometheus"` is
rejected. Do not combine embedded credentials with an `auth` block. New
configurations should use `url` and the explicit `auth` fields.

**Removal:** Future breaking release. Remove the alias, its embedded-credential
handling, migration warnings, and associated tests together.

### Target type `statlite`

**Replacement:** `statlite-metrics`

At startup, StatLite migrates `type: "statlite"` to
`type: "statlite-metrics"`, changes the URL path to `/statlite/metrics`, and
logs a deprecation warning.

**Removal:** Future breaking release. Remove the migration, warning, legacy
constant, and associated tests together.

## Database schema

There are currently no deprecated SQLite tables or columns. Add them here when
a schema migration supersedes a persisted field, table, index, or value
encoding. Record its replacement, read/write compatibility, migration path,
and removal release.

## Legacy persisted data

### Timestamp text ending in `Z`

Databases created before the current implicit-UTC timestamp encoding may
contain RFC 3339 or RFC 3339Nano text ending in `Z`. StatLite continues to read
both representations, and existing rows are not rewritten. The
`app_runs.process_start_time` identity field continues to use its established
RFC 3339Nano representation so it remains equal to existing rows.

This is data compatibility rather than a deprecated table or column. Removing
the legacy reader requires an explicit schema migration that normalizes
existing values without changing application-run identity.

## Removal checklist

When removing an entry:

1. Confirm the planned breaking release and document it in the changelog.
2. Provide any required configuration or database migration instructions.
3. Remove the compatibility code, warnings, and tests in the same change.
4. Remove the entry from user-facing documentation and retain its removal
   record here.
