package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Agent struct {
	ID             string    `json:"id"`
	Hostname       string    `json:"hostname"`
	Address        string    `json:"address"`
	CPUCapacity    float64   `json:"cpu_capacity"`
	MemoryCapacity int64     `json:"memory_capacity"`
	CPUUsed        float64   `json:"cpu_used"`
	MemoryUsed     int64     `json:"memory_used"`
	Status         string    `json:"status"`
	LastSeen       time.Time `json:"last_seen"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Deployment struct {
	ID              int64     `json:"id"`
	AppName         string    `json:"app_name"`
	RepoURL         string    `json:"repo_url"`
	CommitSHA       string    `json:"commit_sha"`
	Branch          string    `json:"branch"`
	Status          string    `json:"status"`
	ConfigJSON      string    `json:"config_json,omitempty"`
	AssignedAgentID string    `json:"assigned_agent_id"`
	Host            string    `json:"host"`
	TargetPort      int       `json:"target_port"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Task struct {
	ID           int64     `json:"id"`
	DeploymentID int64     `json:"deployment_id"`
	AgentID      string    `json:"agent_id"`
	Type         string    `json:"type"`
	Status       string    `json:"status"`
	PayloadJSON  string    `json:"payload_json,omitempty"`
	Attempts     int       `json:"attempts"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	CompletedAt  time.Time `json:"completed_at,omitempty"`
}

type TaskEvent struct {
	ID           int64     `json:"id"`
	TaskID       int64     `json:"task_id"`
	DeploymentID int64     `json:"deployment_id"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	CreatedAt    time.Time `json:"created_at"`
}

type Secret struct {
	AppName    string    `json:"app_name"`
	Key        string    `json:"key"`
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(absPath))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)

	s := &Store{db: db}
	if _, err := s.db.ExecContext(context.Background(), schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func sqliteDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) UpsertAgent(ctx context.Context, agent Agent) error {
	now := timestamp(time.Now())
	if agent.Status == "" {
		agent.Status = "online"
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agents(id, hostname, address, cpu_capacity, memory_capacity, cpu_used, memory_used, status, last_seen, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  hostname=excluded.hostname,
  address=excluded.address,
  cpu_capacity=excluded.cpu_capacity,
  memory_capacity=excluded.memory_capacity,
  status=excluded.status,
  last_seen=excluded.last_seen,
  updated_at=excluded.updated_at;
`, agent.ID, agent.Hostname, agent.Address, agent.CPUCapacity, agent.MemoryCapacity, agent.CPUUsed, agent.MemoryUsed, agent.Status, now, now, now)
	return err
}

func (s *Store) UpdateAgentHeartbeat(ctx context.Context, id string, address string, cpuUsed float64, memoryUsed int64) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `
UPDATE agents SET address=?, cpu_used=?, memory_used=?, status='online', last_seen=?, updated_at=? WHERE id=?;
`, address, cpuUsed, memoryUsed, now, now, id)
	return err
}

func (s *Store) OnlineAgents(ctx context.Context, cutoff time.Time) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM agents WHERE status='online' AND last_seen >= ? ORDER BY last_seen DESC;`, timestamp(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgents(rows)
}

func (s *Store) GetAgent(ctx context.Context, id string) (Agent, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT * FROM agents WHERE id=? LIMIT 1;`, id)
	agent, err := scanAgent(row)
	if err == sql.ErrNoRows {
		return Agent{}, false, nil
	}
	if err != nil {
		return Agent{}, false, err
	}
	return agent, true, nil
}

func (s *Store) CreateDeployment(ctx context.Context, d Deployment) (Deployment, error) {
	now := timestamp(time.Now())
	if d.Status == "" {
		d.Status = "pending"
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO deployments(app_name, repo_url, commit_sha, branch, status, config_json, assigned_agent_id, host, target_port, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
`, d.AppName, d.RepoURL, d.CommitSHA, d.Branch, d.Status, d.ConfigJSON, d.AssignedAgentID, d.Host, d.TargetPort, now, now)
	if err != nil {
		return Deployment{}, err
	}
	d.ID, err = result.LastInsertId()
	if err != nil {
		return Deployment{}, err
	}
	d.CreatedAt = parseTime(now)
	d.UpdatedAt = d.CreatedAt
	return d, nil
}

func (s *Store) ListDeploymentsByStatus(ctx context.Context, status string) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM deployments WHERE status=? ORDER BY created_at ASC;`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

func (s *Store) ListDeployments(ctx context.Context, limit int) ([]Deployment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM deployments ORDER BY created_at DESC LIMIT ?;`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

func (s *Store) GetDeployment(ctx context.Context, id int64) (Deployment, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT * FROM deployments WHERE id=? LIMIT 1;`, id)
	deployment, err := scanDeployment(row)
	if err == sql.ErrNoRows {
		return Deployment{}, false, nil
	}
	if err != nil {
		return Deployment{}, false, err
	}
	return deployment, true, nil
}

func (s *Store) HasRunningDeploymentHost(ctx context.Context, host string) (bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM deployments WHERE host=? AND status='running' LIMIT 1;`, host).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) UpdateDeploymentAssignment(ctx context.Context, id int64, agentID string, status string) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET assigned_agent_id=?, status=?, updated_at=? WHERE id=?;`, agentID, status, now, id)
	return err
}

func (s *Store) UpdateDeploymentStatus(ctx context.Context, id int64, status string) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET status=?, updated_at=? WHERE id=?;`, status, now, id)
	return err
}

func (s *Store) SetDeploymentTargetPort(ctx context.Context, id int64, port int) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET target_port=?, updated_at=? WHERE id=?;`, port, now, id)
	return err
}

func (s *Store) MarkDeploymentRunning(ctx context.Context, id int64, port int) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET status='running', target_port=?, updated_at=? WHERE id=?;`, port, now, id)
	return err
}

func (s *Store) LatestRunningDeploymentByHostExcluding(ctx context.Context, host string, excludedID int64) (Deployment, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT * FROM deployments WHERE host=? AND status='running' AND id != ? ORDER BY updated_at DESC LIMIT 1;`, host, excludedID)
	deployment, err := scanDeployment(row)
	if err == sql.ErrNoRows {
		return Deployment{}, false, nil
	}
	if err != nil {
		return Deployment{}, false, err
	}
	return deployment, true, nil
}

func (s *Store) UsedTargetPorts(ctx context.Context, agentID string) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT target_port FROM deployments
WHERE assigned_agent_id=?
  AND target_port > 0
  AND status IN ('building', 'deploying', 'running');
`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ports := make(map[int]bool)
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			return nil, err
		}
		ports[port] = true
	}
	return ports, rows.Err()
}

func (s *Store) CreateTask(ctx context.Context, task Task) (Task, error) {
	now := timestamp(time.Now())
	if task.Status == "" {
		task.Status = "pending"
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO tasks(deployment_id, agent_id, type, status, payload_json, attempts, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?);
`, task.DeploymentID, task.AgentID, task.Type, task.Status, task.PayloadJSON, task.Attempts, now, now)
	if err != nil {
		return Task{}, err
	}
	task.ID, err = result.LastInsertId()
	if err != nil {
		return Task{}, err
	}
	task.CreatedAt = parseTime(now)
	task.UpdatedAt = task.CreatedAt
	return task, nil
}

func (s *Store) ClaimNextTask(ctx context.Context, agentID string) (*Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `SELECT * FROM tasks WHERE agent_id=? AND status='pending' ORDER BY created_at ASC LIMIT 1;`, agentID)
	task, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := timestamp(time.Now())
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET status='in_progress', attempts=attempts+1, updated_at=? WHERE id=? AND status='pending';`, now, task.ID)
	if err != nil {
		return nil, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if updated == 0 {
		return nil, tx.Commit()
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	task.Status = "in_progress"
	task.Attempts++
	task.UpdatedAt = parseTime(now)
	return &task, nil
}

func (s *Store) ActiveTaskCountsByAgent(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT agent_id, COUNT(*) AS count FROM tasks WHERE status IN ('pending', 'in_progress') GROUP BY agent_id;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var agentID string
		var count int
		if err := rows.Scan(&agentID, &count); err != nil {
			return nil, err
		}
		counts[agentID] = count
	}
	return counts, rows.Err()
}

func (s *Store) CompleteTask(ctx context.Context, id int64, status string) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?, completed_at=?, updated_at=? WHERE id=?;`, status, now, now, id)
	return err
}

func (s *Store) GetTask(ctx context.Context, id int64) (Task, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT * FROM tasks WHERE id=? LIMIT 1;`, id)
	task, err := scanTask(row)
	if err == sql.ErrNoRows {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	return task, true, nil
}

func (s *Store) AddTaskEvent(ctx context.Context, event TaskEvent) (TaskEvent, error) {
	now := timestamp(time.Now())
	result, err := s.db.ExecContext(ctx, `
INSERT INTO task_events(task_id, deployment_id, level, message, created_at)
VALUES(?, ?, ?, ?, ?);
`, event.TaskID, event.DeploymentID, event.Level, event.Message, now)
	if err != nil {
		return TaskEvent{}, err
	}
	event.ID, _ = result.LastInsertId()
	event.CreatedAt = parseTime(now)
	return event, nil
}

func (s *Store) PruneTaskEventsBefore(ctx context.Context, cutoff time.Time) error {
	_, err := s.db.ExecContext(ctx, `
DELETE FROM task_events
WHERE deployment_id IN (
  SELECT id FROM deployments WHERE updated_at < ?
);
`, timestamp(cutoff))
	return err
}

func (s *Store) SetSecret(ctx context.Context, secret Secret) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO secrets(app_name, key, nonce, ciphertext, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(app_name, key) DO UPDATE SET
  nonce=excluded.nonce,
  ciphertext=excluded.ciphertext,
  updated_at=excluded.updated_at;
`, secret.AppName, secret.Key, secret.Nonce, secret.Ciphertext, now, now)
	return err
}

func (s *Store) GetSecret(ctx context.Context, appName string, key string) (Secret, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT app_name, key, nonce, ciphertext, created_at, updated_at FROM secrets WHERE app_name=? AND key=? LIMIT 1;`, appName, key)
	secret, err := scanSecret(row)
	if err == sql.ErrNoRows {
		return Secret{}, false, nil
	}
	if err != nil {
		return Secret{}, false, err
	}
	return secret, true, nil
}

func (s *Store) ListSecretKeys(ctx context.Context, appName string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM secrets WHERE app_name=? ORDER BY key;`, appName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) DeleteSecret(ctx context.Context, appName string, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE app_name=? AND key=?;`, appName, key)
	return err
}

func (s *Store) ListTaskEventsByDeployment(ctx context.Context, deploymentID int64) ([]TaskEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, deployment_id, level, message, created_at FROM task_events WHERE deployment_id=? ORDER BY created_at ASC, id ASC;`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []TaskEvent
	for rows.Next() {
		var event TaskEvent
		var createdAt string
		if err := rows.Scan(&event.ID, &event.TaskID, &event.DeploymentID, &event.Level, &event.Message, &createdAt); err != nil {
			return nil, err
		}
		event.CreatedAt = parseTime(createdAt)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) DeploymentCounts(ctx context.Context) (map[string]int64, error) {
	return s.counts(ctx, "deployments")
}

func (s *Store) TaskCounts(ctx context.Context) (map[string]int64, error) {
	return s.counts(ctx, "tasks")
}

func (s *Store) counts(ctx context.Context, table string) (map[string]int64, error) {
	if table != "deployments" && table != "tasks" {
		return nil, fmt.Errorf("invalid table %q", table)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT status, COUNT(*) AS count FROM %s GROUP BY status;`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out[status] = count
	}
	return out, rows.Err()
}

func scanAgents(rows *sql.Rows) ([]Agent, error) {
	var agents []Agent
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func scanAgent(row scanner) (Agent, error) {
	var agent Agent
	var lastSeen, createdAt, updatedAt string
	err := row.Scan(&agent.ID, &agent.Hostname, &agent.Address, &agent.CPUCapacity, &agent.MemoryCapacity, &agent.CPUUsed, &agent.MemoryUsed, &agent.Status, &lastSeen, &createdAt, &updatedAt)
	if err != nil {
		return Agent{}, err
	}
	agent.LastSeen = parseTime(lastSeen)
	agent.CreatedAt = parseTime(createdAt)
	agent.UpdatedAt = parseTime(updatedAt)
	return agent, nil
}

func scanDeployments(rows *sql.Rows) ([]Deployment, error) {
	var deployments []Deployment
	for rows.Next() {
		deployment, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		deployments = append(deployments, deployment)
	}
	return deployments, rows.Err()
}

func scanDeployment(row scanner) (Deployment, error) {
	var deployment Deployment
	var createdAt, updatedAt string
	err := row.Scan(&deployment.ID, &deployment.AppName, &deployment.RepoURL, &deployment.CommitSHA, &deployment.Branch, &deployment.Status, &deployment.ConfigJSON, &deployment.AssignedAgentID, &deployment.Host, &deployment.TargetPort, &createdAt, &updatedAt)
	if err != nil {
		return Deployment{}, err
	}
	deployment.CreatedAt = parseTime(createdAt)
	deployment.UpdatedAt = parseTime(updatedAt)
	return deployment, nil
}

func scanTask(row scanner) (Task, error) {
	var task Task
	var createdAt, updatedAt, completedAt string
	err := row.Scan(&task.ID, &task.DeploymentID, &task.AgentID, &task.Type, &task.Status, &task.PayloadJSON, &task.Attempts, &createdAt, &updatedAt, &completedAt)
	if err != nil {
		return Task{}, err
	}
	task.CreatedAt = parseTime(createdAt)
	task.UpdatedAt = parseTime(updatedAt)
	task.CompletedAt = parseTime(completedAt)
	return task, nil
}

func scanSecret(row scanner) (Secret, error) {
	var secret Secret
	var createdAt, updatedAt string
	err := row.Scan(&secret.AppName, &secret.Key, &secret.Nonce, &secret.Ciphertext, &createdAt, &updatedAt)
	if err != nil {
		return Secret{}, err
	}
	secret.CreatedAt = parseTime(createdAt)
	secret.UpdatedAt = parseTime(updatedAt)
	return secret, nil
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

const schemaSQL = `
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
`
