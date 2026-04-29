package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Store struct {
	path string
	mu   sync.Mutex
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

func Open(path string) (*Store, error) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, fmt.Errorf("sqlite3 executable is required: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	s := &Store{path: path}
	if err := s.exec(context.Background(), schemaSQL); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) UpsertAgent(ctx context.Context, agent Agent) error {
	now := timestamp(time.Now())
	if agent.Status == "" {
		agent.Status = "online"
	}
	sql := fmt.Sprintf(`
INSERT INTO agents(id, hostname, address, cpu_capacity, memory_capacity, cpu_used, memory_used, status, last_seen, created_at, updated_at)
VALUES(%s, %s, %s, %s, %d, %s, %d, %s, %s, %s, %s)
ON CONFLICT(id) DO UPDATE SET
  hostname=excluded.hostname,
  address=excluded.address,
  cpu_capacity=excluded.cpu_capacity,
  memory_capacity=excluded.memory_capacity,
  status=excluded.status,
  last_seen=excluded.last_seen,
  updated_at=excluded.updated_at;
`, q(agent.ID), q(agent.Hostname), q(agent.Address), floatSQL(agent.CPUCapacity), agent.MemoryCapacity, floatSQL(agent.CPUUsed), agent.MemoryUsed, q(agent.Status), q(now), q(now), q(now))
	return s.exec(ctx, sql)
}

func (s *Store) UpdateAgentHeartbeat(ctx context.Context, id string, address string, cpuUsed float64, memoryUsed int64) error {
	now := timestamp(time.Now())
	sql := fmt.Sprintf(`
UPDATE agents SET address=%s, cpu_used=%s, memory_used=%d, status='online', last_seen=%s, updated_at=%s WHERE id=%s;
`, q(address), floatSQL(cpuUsed), memoryUsed, q(now), q(now), q(id))
	return s.exec(ctx, sql)
}

func (s *Store) OnlineAgents(ctx context.Context, cutoff time.Time) ([]Agent, error) {
	rows, err := s.query(ctx, fmt.Sprintf(`SELECT * FROM agents WHERE status='online' AND last_seen >= %s ORDER BY last_seen DESC;`, q(timestamp(cutoff))))
	if err != nil {
		return nil, err
	}
	return agentsFromRows(rows), nil
}

func (s *Store) GetAgent(ctx context.Context, id string) (Agent, bool, error) {
	rows, err := s.query(ctx, fmt.Sprintf(`SELECT * FROM agents WHERE id=%s LIMIT 1;`, q(id)))
	if err != nil {
		return Agent{}, false, err
	}
	agents := agentsFromRows(rows)
	if len(agents) == 0 {
		return Agent{}, false, nil
	}
	return agents[0], true, nil
}

func (s *Store) CreateDeployment(ctx context.Context, d Deployment) (Deployment, error) {
	now := timestamp(time.Now())
	if d.Status == "" {
		d.Status = "pending"
	}
	sql := fmt.Sprintf(`
INSERT INTO deployments(app_name, repo_url, commit_sha, branch, status, config_json, assigned_agent_id, host, target_port, created_at, updated_at)
VALUES(%s, %s, %s, %s, %s, %s, %s, %s, %d, %s, %s);
SELECT last_insert_rowid() AS id;
`, q(d.AppName), q(d.RepoURL), q(d.CommitSHA), q(d.Branch), q(d.Status), q(d.ConfigJSON), q(d.AssignedAgentID), q(d.Host), d.TargetPort, q(now), q(now))
	rows, err := s.query(ctx, sql)
	if err != nil {
		return Deployment{}, err
	}
	if len(rows) == 0 {
		return Deployment{}, fmt.Errorf("deployment insert did not return an id")
	}
	d.ID = int64From(rows[0], "id")
	d.CreatedAt = parseTime(now)
	d.UpdatedAt = d.CreatedAt
	return d, nil
}

func (s *Store) ListDeploymentsByStatus(ctx context.Context, status string) ([]Deployment, error) {
	rows, err := s.query(ctx, fmt.Sprintf(`SELECT * FROM deployments WHERE status=%s ORDER BY created_at ASC;`, q(status)))
	if err != nil {
		return nil, err
	}
	return deploymentsFromRows(rows), nil
}

func (s *Store) ListDeployments(ctx context.Context, limit int) ([]Deployment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.query(ctx, fmt.Sprintf(`SELECT * FROM deployments ORDER BY created_at DESC LIMIT %d;`, limit))
	if err != nil {
		return nil, err
	}
	return deploymentsFromRows(rows), nil
}

func (s *Store) GetDeployment(ctx context.Context, id int64) (Deployment, bool, error) {
	rows, err := s.query(ctx, fmt.Sprintf(`SELECT * FROM deployments WHERE id=%d LIMIT 1;`, id))
	if err != nil {
		return Deployment{}, false, err
	}
	deployments := deploymentsFromRows(rows)
	if len(deployments) == 0 {
		return Deployment{}, false, nil
	}
	return deployments[0], true, nil
}

func (s *Store) UpdateDeploymentAssignment(ctx context.Context, id int64, agentID string, status string) error {
	now := timestamp(time.Now())
	return s.exec(ctx, fmt.Sprintf(`UPDATE deployments SET assigned_agent_id=%s, status=%s, updated_at=%s WHERE id=%d;`, q(agentID), q(status), q(now), id))
}

func (s *Store) UpdateDeploymentStatus(ctx context.Context, id int64, status string) error {
	now := timestamp(time.Now())
	return s.exec(ctx, fmt.Sprintf(`UPDATE deployments SET status=%s, updated_at=%s WHERE id=%d;`, q(status), q(now), id))
}

func (s *Store) SetDeploymentTargetPort(ctx context.Context, id int64, port int) error {
	now := timestamp(time.Now())
	return s.exec(ctx, fmt.Sprintf(`UPDATE deployments SET target_port=%d, updated_at=%s WHERE id=%d;`, port, q(now), id))
}

func (s *Store) MarkDeploymentRunning(ctx context.Context, id int64, port int) error {
	now := timestamp(time.Now())
	return s.exec(ctx, fmt.Sprintf(`UPDATE deployments SET status='running', target_port=%d, updated_at=%s WHERE id=%d;`, port, q(now), id))
}

func (s *Store) UsedTargetPorts(ctx context.Context, agentID string) (map[int]bool, error) {
	rows, err := s.query(ctx, fmt.Sprintf(`
SELECT target_port FROM deployments
WHERE assigned_agent_id=%s
  AND target_port > 0
  AND status IN ('building', 'deploying', 'running');
`, q(agentID)))
	if err != nil {
		return nil, err
	}
	ports := make(map[int]bool, len(rows))
	for _, row := range rows {
		ports[int(int64From(row, "target_port"))] = true
	}
	return ports, nil
}

func (s *Store) CreateTask(ctx context.Context, task Task) (Task, error) {
	now := timestamp(time.Now())
	if task.Status == "" {
		task.Status = "pending"
	}
	sql := fmt.Sprintf(`
INSERT INTO tasks(deployment_id, agent_id, type, status, payload_json, attempts, created_at, updated_at)
VALUES(%d, %s, %s, %s, %s, %d, %s, %s);
SELECT last_insert_rowid() AS id;
`, task.DeploymentID, q(task.AgentID), q(task.Type), q(task.Status), q(task.PayloadJSON), task.Attempts, q(now), q(now))
	rows, err := s.query(ctx, sql)
	if err != nil {
		return Task{}, err
	}
	if len(rows) == 0 {
		return Task{}, fmt.Errorf("task insert did not return an id")
	}
	task.ID = int64From(rows[0], "id")
	task.CreatedAt = parseTime(now)
	task.UpdatedAt = task.CreatedAt
	return task, nil
}

func (s *Store) ClaimNextTask(ctx context.Context, agentID string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queryLocked(ctx, fmt.Sprintf(`SELECT * FROM tasks WHERE agent_id=%s AND status='pending' ORDER BY created_at ASC LIMIT 1;`, q(agentID)))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	task := taskFromRow(rows[0])
	now := timestamp(time.Now())
	if err := s.execLocked(ctx, fmt.Sprintf(`UPDATE tasks SET status='in_progress', attempts=attempts+1, updated_at=%s WHERE id=%d;`, q(now), task.ID)); err != nil {
		return nil, err
	}
	task.Status = "in_progress"
	task.Attempts++
	task.UpdatedAt = parseTime(now)
	return &task, nil
}

func (s *Store) CompleteTask(ctx context.Context, id int64, status string) error {
	now := timestamp(time.Now())
	return s.exec(ctx, fmt.Sprintf(`UPDATE tasks SET status=%s, completed_at=%s, updated_at=%s WHERE id=%d;`, q(status), q(now), q(now), id))
}

func (s *Store) GetTask(ctx context.Context, id int64) (Task, bool, error) {
	rows, err := s.query(ctx, fmt.Sprintf(`SELECT * FROM tasks WHERE id=%d LIMIT 1;`, id))
	if err != nil {
		return Task{}, false, err
	}
	if len(rows) == 0 {
		return Task{}, false, nil
	}
	return taskFromRow(rows[0]), true, nil
}

func (s *Store) AddTaskEvent(ctx context.Context, event TaskEvent) (TaskEvent, error) {
	now := timestamp(time.Now())
	sql := fmt.Sprintf(`
INSERT INTO task_events(task_id, deployment_id, level, message, created_at)
VALUES(%d, %d, %s, %s, %s);
SELECT last_insert_rowid() AS id;
`, event.TaskID, event.DeploymentID, q(event.Level), q(event.Message), q(now))
	rows, err := s.query(ctx, sql)
	if err != nil {
		return TaskEvent{}, err
	}
	if len(rows) > 0 {
		event.ID = int64From(rows[0], "id")
	}
	event.CreatedAt = parseTime(now)
	return event, nil
}

func (s *Store) SetSecret(ctx context.Context, secret Secret) error {
	now := timestamp(time.Now())
	sql := fmt.Sprintf(`
INSERT INTO secrets(app_name, key, nonce, ciphertext, created_at, updated_at)
VALUES(%s, %s, %s, %s, %s, %s)
ON CONFLICT(app_name, key) DO UPDATE SET
  nonce=excluded.nonce,
  ciphertext=excluded.ciphertext,
  updated_at=excluded.updated_at;
`, q(secret.AppName), q(secret.Key), q(secret.Nonce), q(secret.Ciphertext), q(now), q(now))
	return s.exec(ctx, sql)
}

func (s *Store) GetSecret(ctx context.Context, appName string, key string) (Secret, bool, error) {
	rows, err := s.query(ctx, fmt.Sprintf(`SELECT * FROM secrets WHERE app_name=%s AND key=%s LIMIT 1;`, q(appName), q(key)))
	if err != nil {
		return Secret{}, false, err
	}
	if len(rows) == 0 {
		return Secret{}, false, nil
	}
	row := rows[0]
	return Secret{
		AppName:    stringFrom(row, "app_name"),
		Key:        stringFrom(row, "key"),
		Nonce:      stringFrom(row, "nonce"),
		Ciphertext: stringFrom(row, "ciphertext"),
		CreatedAt:  timeFrom(row, "created_at"),
		UpdatedAt:  timeFrom(row, "updated_at"),
	}, true, nil
}

func (s *Store) ListSecretKeys(ctx context.Context, appName string) ([]string, error) {
	rows, err := s.query(ctx, fmt.Sprintf(`SELECT key FROM secrets WHERE app_name=%s ORDER BY key;`, q(appName)))
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, stringFrom(row, "key"))
	}
	return keys, nil
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
	rows, err := s.query(ctx, fmt.Sprintf(`SELECT status, COUNT(*) AS count FROM %s GROUP BY status;`, table))
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[stringFrom(row, "status")] = int64From(row, "count")
	}
	return out, nil
}

func (s *Store) exec(ctx context.Context, sql string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.execLocked(ctx, sql)
}

func (s *Store) query(ctx context.Context, sql string) ([]map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queryLocked(ctx, sql)
}

func (s *Store) execLocked(ctx context.Context, sql string) error {
	cmd := exec.CommandContext(ctx, "sqlite3", s.path)
	cmd.Stdin = strings.NewReader(sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sqlite exec failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *Store) queryLocked(ctx context.Context, sql string) ([]map[string]interface{}, error) {
	cmd := exec.CommandContext(ctx, "sqlite3", "-json", s.path, sql)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sqlite query failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if strings.TrimSpace(stdout.String()) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(&stdout)
	decoder.UseNumber()
	var rows []map[string]interface{}
	if err := decoder.Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode sqlite json: %w", err)
	}
	return rows, nil
}

func agentsFromRows(rows []map[string]interface{}) []Agent {
	agents := make([]Agent, 0, len(rows))
	for _, row := range rows {
		agents = append(agents, Agent{
			ID:             stringFrom(row, "id"),
			Hostname:       stringFrom(row, "hostname"),
			Address:        stringFrom(row, "address"),
			CPUCapacity:    floatFrom(row, "cpu_capacity"),
			MemoryCapacity: int64From(row, "memory_capacity"),
			CPUUsed:        floatFrom(row, "cpu_used"),
			MemoryUsed:     int64From(row, "memory_used"),
			Status:         stringFrom(row, "status"),
			LastSeen:       timeFrom(row, "last_seen"),
			CreatedAt:      timeFrom(row, "created_at"),
			UpdatedAt:      timeFrom(row, "updated_at"),
		})
	}
	return agents
}

func deploymentsFromRows(rows []map[string]interface{}) []Deployment {
	deployments := make([]Deployment, 0, len(rows))
	for _, row := range rows {
		deployments = append(deployments, Deployment{
			ID:              int64From(row, "id"),
			AppName:         stringFrom(row, "app_name"),
			RepoURL:         stringFrom(row, "repo_url"),
			CommitSHA:       stringFrom(row, "commit_sha"),
			Branch:          stringFrom(row, "branch"),
			Status:          stringFrom(row, "status"),
			ConfigJSON:      stringFrom(row, "config_json"),
			AssignedAgentID: stringFrom(row, "assigned_agent_id"),
			Host:            stringFrom(row, "host"),
			TargetPort:      int(int64From(row, "target_port")),
			CreatedAt:       timeFrom(row, "created_at"),
			UpdatedAt:       timeFrom(row, "updated_at"),
		})
	}
	return deployments
}

func taskFromRow(row map[string]interface{}) Task {
	return Task{
		ID:           int64From(row, "id"),
		DeploymentID: int64From(row, "deployment_id"),
		AgentID:      stringFrom(row, "agent_id"),
		Type:         stringFrom(row, "type"),
		Status:       stringFrom(row, "status"),
		PayloadJSON:  stringFrom(row, "payload_json"),
		Attempts:     int(int64From(row, "attempts")),
		CreatedAt:    timeFrom(row, "created_at"),
		UpdatedAt:    timeFrom(row, "updated_at"),
		CompletedAt:  timeFrom(row, "completed_at"),
	}
}

func stringFrom(row map[string]interface{}, key string) string {
	value, ok := row[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func int64From(row map[string]interface{}, key string) int64 {
	value, ok := row[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case json.Number:
		n, _ := typed.Int64()
		return n
	case float64:
		return int64(typed)
	case string:
		n, _ := strconv.ParseInt(typed, 10, 64)
		return n
	default:
		return 0
	}
}

func floatFrom(row map[string]interface{}, key string) float64 {
	value, ok := row[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case json.Number:
		f, _ := typed.Float64()
		return f
	case float64:
		return typed
	case string:
		f, _ := strconv.ParseFloat(typed, 64)
		return f
	default:
		return 0
	}
}

func timeFrom(row map[string]interface{}, key string) time.Time {
	return parseTime(stringFrom(row, key))
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

func q(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func floatSQL(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

const schemaSQL = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA foreign_keys=ON;

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
