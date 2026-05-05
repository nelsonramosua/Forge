CREATE TABLE IF NOT EXISTS allowed_repos (
  repo_full_name TEXT PRIMARY KEY,
  source TEXT NOT NULL DEFAULT 'admin',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
