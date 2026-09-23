PRAGMA foreign_keys = ON;

-- Configured applications monitored by Statlite.
CREATE TABLE IF NOT EXISTS targets (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE CHECK (name <> ''),
  created_at TEXT NOT NULL
);

-- Detected runtime instances for a target process.
CREATE TABLE IF NOT EXISTS app_runs (
  id INTEGER PRIMARY KEY,
  target_id INTEGER NOT NULL,
  process_start_time TEXT,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  FOREIGN KEY (target_id) REFERENCES targets(id),
  UNIQUE (target_id, process_start_time)
);

-- One logical collection snapshot per poll cycle.
CREATE TABLE IF NOT EXISTS polls (
  id INTEGER PRIMARY KEY,
  target_id INTEGER NOT NULL,
  app_run_id INTEGER,
  started_at TEXT NOT NULL,
  finished_at TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('ok', 'error')),
  health_status TEXT,
  db_health_status TEXT,
  error_summary TEXT,
  FOREIGN KEY (target_id) REFERENCES targets(id),
  FOREIGN KEY (app_run_id) REFERENCES app_runs(id)
);

-- Raw normalized metric values observed during a poll.
CREATE TABLE IF NOT EXISTS metric_samples (
  id INTEGER PRIMARY KEY,
  poll_id INTEGER NOT NULL,
  metric_key TEXT NOT NULL CHECK (metric_key <> ''),
  metric_kind TEXT NOT NULL CHECK (metric_kind IN ('counter', 'gauge')),
  value REAL NOT NULL,
  unit TEXT,
  FOREIGN KEY (poll_id) REFERENCES polls(id) ON DELETE CASCADE
);

-- Collector warnings and errors recorded for a poll.
CREATE TABLE IF NOT EXISTS collector_events (
  id INTEGER PRIMARY KEY,
  poll_id INTEGER NOT NULL,
  severity TEXT NOT NULL CHECK (severity IN ('warning', 'error')),
  event_type TEXT NOT NULL CHECK (event_type <> ''),
  metric_key TEXT,
  message TEXT NOT NULL CHECK (message <> ''),
  FOREIGN KEY (poll_id) REFERENCES polls(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_polls_target_started_at ON polls(target_id, started_at);
CREATE INDEX IF NOT EXISTS idx_metric_samples_poll_id ON metric_samples(poll_id);
CREATE INDEX IF NOT EXISTS idx_collector_events_poll_id ON collector_events(poll_id);
