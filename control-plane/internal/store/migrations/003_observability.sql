CREATE TABLE IF NOT EXISTS deployment_health_observations (
  deployment_id INTEGER PRIMARY KEY,
  status TEXT NOT NULL,
  reason TEXT NOT NULL,
  checked_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS task_event_metadata (
  task_event_id INTEGER PRIMARY KEY,
  request_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
