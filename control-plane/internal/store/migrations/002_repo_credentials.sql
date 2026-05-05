CREATE TABLE IF NOT EXISTS repo_credentials (
  repo_full_name TEXT PRIMARY KEY,
  nonce TEXT NOT NULL,
  ciphertext TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
