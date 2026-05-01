CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  hostname TEXT NOT NULL,
  address TEXT NOT NULL DEFAULT '',
  cpu_capacity REAL NOT NULL DEFAULT 0,
  memory_capacity INTEGER NOT NULL DEFAULT 0,
  cpu_used REAL NOT NULL DEFAULT 0,
  memory_used INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'online',
  last_seen TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS deployments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT NOT NULL,
  repo_url TEXT NOT NULL,
  commit_sha TEXT NOT NULL,
  branch TEXT NOT NULL,
  status TEXT NOT NULL,
  config_json TEXT NOT NULL,
  assigned_agent_id TEXT NOT NULL DEFAULT '',
  host TEXT NOT NULL,
  target_port INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS deployments_status_idx ON deployments(status);
CREATE INDEX IF NOT EXISTS deployments_agent_idx ON deployments(assigned_agent_id);

CREATE TABLE IF NOT EXISTS tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  type TEXT NOT NULL CHECK(type IN ('build', 'run')),
  status TEXT NOT NULL CHECK(status IN ('pending', 'in_progress', 'succeeded', 'failed')),
  payload_json TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  completed_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS tasks_agent_status_idx ON tasks(agent_id, status, created_at);
CREATE INDEX IF NOT EXISTS tasks_deployment_idx ON tasks(deployment_id);

CREATE TABLE IF NOT EXISTS task_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  level TEXT NOT NULL,
  message TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS task_events_deployment_idx ON task_events(deployment_id, created_at);

CREATE TABLE IF NOT EXISTS secrets (
  app_name TEXT NOT NULL,
  key TEXT NOT NULL,
  nonce TEXT NOT NULL,
  ciphertext TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(app_name, key)
);
