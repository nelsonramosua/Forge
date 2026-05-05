package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "embed"
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

type DeploymentHealthObservation struct {
	DeploymentID int64     `json:"deployment_id"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason"`
	CheckedAt    time.Time `json:"checked_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type DeploymentRetryState struct {
	DeploymentID      int64     `json:"deployment_id"`
	AttemptCount      int       `json:"attempt_count"`
	NextRetryAt       time.Time `json:"next_retry_at"`
	LastFailureReason string    `json:"last_failure_reason"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
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
	RequestID    string    `json:"request_id,omitempty"`
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

type RepoCredential struct {
	RepoFullName string    `json:"repo_full_name"`
	Nonce        string    `json:"nonce"`
	Ciphertext   string    `json:"ciphertext"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type AllowedRepo struct {
	RepoFullName string    `json:"repo_full_name"`
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type scanner interface {
	Scan(dest ...interface{}) error
}

var ErrTaskAlreadyCompleted = errors.New("task already completed")

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
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
	if _, err := s.db.ExecContext(context.Background(), repoCredentialsMigrationSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := s.db.ExecContext(context.Background(), observabilityMigrationSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := s.db.ExecContext(context.Background(), deploymentRetryMigrationSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := s.db.ExecContext(context.Background(), allowedReposMigrationSQL); err != nil {
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
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agents(id, hostname, address, cpu_capacity, memory_capacity, cpu_used, memory_used, status, last_seen, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), 'online'), ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  hostname=excluded.hostname,
  address=excluded.address,
  cpu_capacity=excluded.cpu_capacity,
  memory_capacity=excluded.memory_capacity,
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
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = rows.Close() }()
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

func (s *Store) HasRunningDeploymentApp(ctx context.Context, appName string) (bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM deployments WHERE app_name=? AND status='running' LIMIT 1;`, appName).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) UpdateDeploymentAssignment(ctx context.Context, id int64, agentID string, status string) error {
	if status == "" {
		status = "building"
	}
	current, err := s.deploymentStatusByID(ctx, id)
	if err != nil {
		return err
	}
	if current != "pending" {
		return fmt.Errorf("invalid deployment status transition %q -> %q", current, status)
	}
	now := timestamp(time.Now())
	result, err := s.db.ExecContext(ctx, `UPDATE deployments SET assigned_agent_id=?, status=?, updated_at=? WHERE id=? AND status='pending';`, agentID, status, now, id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("deployment %d changed while updating assignment", id)
	}
	return nil
}

func (s *Store) UpdateDeploymentStatus(ctx context.Context, id int64, status string) error {
	current, err := s.deploymentStatusByID(ctx, id)
	if err != nil {
		return err
	}
	if !deploymentStatusTransitionAllowed(current, status) {
		return fmt.Errorf("invalid deployment status transition %q -> %q", current, status)
	}
	now := timestamp(time.Now())
	result, err := s.db.ExecContext(ctx, `UPDATE deployments SET status=?, updated_at=? WHERE id=? AND status=?;`, status, now, id, current)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("deployment %d changed while updating status", id)
	}
	return nil
}

func (s *Store) SetDeploymentTargetPort(ctx context.Context, id int64, port int) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET target_port=?, updated_at=? WHERE id=?;`, port, now, id)
	return err
}

func (s *Store) MarkDeploymentRunning(ctx context.Context, id int64, port int) error {
	current, err := s.deploymentStatusByID(ctx, id)
	if err != nil {
		return err
	}
	if current != "deploying" && current != "running" {
		return fmt.Errorf("invalid deployment status transition %q -> running", current)
	}
	now := timestamp(time.Now())
	result, err := s.db.ExecContext(ctx, `UPDATE deployments SET status='running', target_port=?, updated_at=? WHERE id=? AND status=?;`, port, now, id, current)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("deployment %d changed while marking running", id)
	}
	return nil
}

func (s *Store) deploymentStatusByID(ctx context.Context, id int64) (string, error) {
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM deployments WHERE id=? LIMIT 1;`, id).Scan(&status); err != nil {
		return "", err
	}
	return status, nil
}

func deploymentStatusTransitionAllowed(current, next string) bool {
	if current == next {
		return true
	}
	switch next {
	case "building":
		return current == "pending"
	case "deploying":
		return current == "building"
	case "running":
		return current == "deploying"
	case "stopping":
		return current == "running"
	case "stopped":
		return current == "stopping"
	case "failed":
		return current == "pending" || current == "building" || current == "deploying" || current == "running" || current == "stopping"
	case "pending":
		return current == "failed" || current == "stopped"
	default:
		return false
	}
}

func (s *Store) LatestRunningDeploymentByHostExcluding(ctx context.Context, host string, excludedID int64) (Deployment, bool, error) {
	deployments, err := s.RunningDeploymentsByHostExcluding(ctx, host, excludedID)
	if err != nil {
		return Deployment{}, false, err
	}
	if len(deployments) == 0 {
		return Deployment{}, false, nil
	}
	return deployments[0], true, nil
}

func (s *Store) RunningDeploymentsByHostExcluding(ctx context.Context, host string, excludedID int64) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT * FROM deployments
WHERE host=? AND status='running' AND id != ?
ORDER BY updated_at DESC, id DESC;
`, host, excludedID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDeployments(rows)
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
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = rows.Close() }()
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
	result, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?, completed_at=?, updated_at=? WHERE id=? AND (completed_at='' OR completed_at IS NULL);`, status, now, now, id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return ErrTaskAlreadyCompleted
	}
	return nil
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
	if event.RequestID != "" {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO task_event_metadata(task_event_id, request_id, created_at, updated_at) VALUES(?, ?, ?, ?) ON CONFLICT(task_event_id) DO UPDATE SET request_id=excluded.request_id, updated_at=excluded.updated_at;`, event.ID, event.RequestID, now, now); err != nil {
			return TaskEvent{}, err
		}
	}
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
	defer func() { _ = rows.Close() }()
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

func (s *Store) SetDeploymentHealthObservation(ctx context.Context, deploymentID int64, status string, reason string) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO deployment_health_observations(deployment_id, status, reason, checked_at, updated_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(deployment_id) DO UPDATE SET
  status=excluded.status,
  reason=excluded.reason,
  checked_at=excluded.checked_at,
  updated_at=excluded.updated_at;
`, deploymentID, status, reason, now, now)
	return err
}

func (s *Store) ClearDeploymentHealthObservation(ctx context.Context, deploymentID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deployment_health_observations WHERE deployment_id=?;`, deploymentID)
	return err
}

func (s *Store) DeploymentHealthObservationsByDeploymentIDs(ctx context.Context, deploymentIDs []int64) (map[int64]DeploymentHealthObservation, error) {
	if len(deploymentIDs) == 0 {
		return map[int64]DeploymentHealthObservation{}, nil
	}
	placeholders := make([]string, len(deploymentIDs))
	args := make([]interface{}, len(deploymentIDs))
	for i, deploymentID := range deploymentIDs {
		placeholders[i] = "?"
		args[i] = deploymentID
	}
	// #nosec G201 -- the placeholders are generated locally and values remain parameterized.
	query := fmt.Sprintf(`SELECT deployment_id, status, reason, checked_at, updated_at FROM deployment_health_observations WHERE deployment_id IN (%s);`, strings.Join(placeholders, ","))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	observations := make(map[int64]DeploymentHealthObservation, len(deploymentIDs))
	for rows.Next() {
		var observation DeploymentHealthObservation
		var checkedAt, updatedAt string
		if err := rows.Scan(&observation.DeploymentID, &observation.Status, &observation.Reason, &checkedAt, &updatedAt); err != nil {
			return nil, err
		}
		observation.CheckedAt = parseTime(checkedAt)
		observation.UpdatedAt = parseTime(updatedAt)
		observations[observation.DeploymentID] = observation
	}
	return observations, rows.Err()
}

func (s *Store) DeploymentRetryStateByDeploymentID(ctx context.Context, deploymentID int64) (DeploymentRetryState, bool, error) {
	var state DeploymentRetryState
	var nextRetryAt, createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT deployment_id, attempt_count, next_retry_at, last_failure_reason, created_at, updated_at
FROM deployment_retry_state
WHERE deployment_id=?
LIMIT 1;
`, deploymentID).Scan(&state.DeploymentID, &state.AttemptCount, &nextRetryAt, &state.LastFailureReason, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return DeploymentRetryState{}, false, nil
	}
	if err != nil {
		return DeploymentRetryState{}, false, err
	}
	state.NextRetryAt = parseTime(nextRetryAt)
	state.CreatedAt = parseTime(createdAt)
	state.UpdatedAt = parseTime(updatedAt)
	return state, true, nil
}

func (s *Store) UpsertDeploymentRetryState(ctx context.Context, deploymentID int64, attemptCount int, nextRetryAt time.Time, reason string) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO deployment_retry_state (deployment_id, attempt_count, next_retry_at, last_failure_reason, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(deployment_id) DO UPDATE SET
	attempt_count=excluded.attempt_count,
	next_retry_at=excluded.next_retry_at,
	last_failure_reason=excluded.last_failure_reason,
	updated_at=excluded.updated_at;
`, deploymentID, attemptCount, timestamp(nextRetryAt), reason, now, now)
	return err
}

func (s *Store) ClearDeploymentRetryState(ctx context.Context, deploymentID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deployment_retry_state WHERE deployment_id=?;`, deploymentID)
	return err
}

func (s *Store) RetryDeployment(ctx context.Context, id int64) error {
	if err := s.QueueDeploymentRetry(ctx, id); err != nil {
		return err
	}
	return s.ClearDeploymentRetryState(ctx, id)
}

func (s *Store) QueueDeploymentRetry(ctx context.Context, id int64) error {
	current, err := s.deploymentStatusByID(ctx, id)
	if err != nil {
		return err
	}
	if current != "failed" && current != "stopped" {
		return fmt.Errorf("invalid deployment status transition %q -> pending", current)
	}
	now := timestamp(time.Now())
	result, err := s.db.ExecContext(ctx, `UPDATE deployments SET status='pending', assigned_agent_id='', target_port=0, updated_at=? WHERE id=? AND status IN ('failed', 'stopped');`, now, id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("deployment %d changed while retrying", id)
	}
	if err := s.ClearDeploymentHealthObservation(ctx, id); err != nil {
		return err
	}
	return nil
}

func (s *Store) CancelTasksForDeployment(ctx context.Context, deploymentID int64) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET status='failed', completed_at=?, updated_at=? WHERE deployment_id=? AND status IN ('pending', 'in_progress');`, now, now, deploymentID)
	return err
}

func (s *Store) StaleDeploymentsByStatuses(ctx context.Context, statuses []string, cutoff time.Time) ([]Deployment, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]interface{}, 0, len(statuses)+1)
	for i, status := range statuses {
		placeholders[i] = "?"
		args = append(args, status)
	}
	args = append(args, timestamp(cutoff))
	// #nosec G201 -- the placeholders are generated locally and values remain parameterized.
	query := fmt.Sprintf(`SELECT * FROM deployments WHERE status IN (%s) AND updated_at < ? ORDER BY updated_at ASC, id ASC;`, strings.Join(placeholders, ","))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDeployments(rows)
}

func (s *Store) StaleInProgressTasks(ctx context.Context, cutoff time.Time) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM tasks WHERE status='in_progress' AND updated_at < ? ORDER BY updated_at ASC, id ASC;`, timestamp(cutoff))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) TouchTask(ctx context.Context, id int64) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=? AND status='in_progress';`, now, id)
	return err
}

func (s *Store) SetRepoCredential(ctx context.Context, cred RepoCredential) error {
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO repo_credentials(repo_full_name, nonce, ciphertext, created_at, updated_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(repo_full_name) DO UPDATE SET
  nonce=excluded.nonce,
  ciphertext=excluded.ciphertext,
  updated_at=excluded.updated_at;
`, cred.RepoFullName, cred.Nonce, cred.Ciphertext, now, now)
	return err
}

func (s *Store) GetRepoCredential(ctx context.Context, repoFullName string) (RepoCredential, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT repo_full_name, nonce, ciphertext, created_at, updated_at
FROM repo_credentials WHERE repo_full_name=? LIMIT 1;
`, repoFullName)
	cred, err := scanRepoCredential(row)
	if err == sql.ErrNoRows {
		return RepoCredential{}, false, nil
	}
	if err != nil {
		return RepoCredential{}, false, err
	}
	return cred, true, nil
}

func (s *Store) DeleteRepoCredential(ctx context.Context, repoFullName string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repo_credentials WHERE repo_full_name=?;`, repoFullName)
	return err
}

func (s *Store) HasRepoCredential(ctx context.Context, repoFullName string) (bool, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT repo_full_name FROM repo_credentials WHERE repo_full_name=? LIMIT 1;`, repoFullName).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) UpsertAllowedRepo(ctx context.Context, repoFullName string, source string) error {
	if source == "" {
		source = "admin"
	}
	now := timestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO allowed_repos(repo_full_name, source, created_at, updated_at)
VALUES(?, ?, ?, ?)
ON CONFLICT(repo_full_name) DO UPDATE SET
  source=excluded.source,
  updated_at=excluded.updated_at;
`, repoFullName, source, now, now)
	return err
}

func (s *Store) DeleteAllowedRepo(ctx context.Context, repoFullName string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM allowed_repos WHERE repo_full_name=?;`, repoFullName)
	return err
}

func (s *Store) ListAllowedRepos(ctx context.Context) ([]AllowedRepo, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT repo_full_name, source, created_at, updated_at
FROM allowed_repos ORDER BY repo_full_name;
`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var repos []AllowedRepo
	for rows.Next() {
		repo, err := scanAllowedRepo(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func (s *Store) ListTaskEventsByDeployment(ctx context.Context, deploymentID int64, limit int, offset int) ([]TaskEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT e.id, e.task_id, e.deployment_id, COALESCE(m.request_id, ''), e.level, e.message, e.created_at
FROM task_events e
LEFT JOIN task_event_metadata m ON m.task_event_id = e.id
WHERE e.deployment_id=?
ORDER BY e.created_at ASC, e.id ASC
LIMIT ? OFFSET ?;
`, deploymentID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var events []TaskEvent
	for rows.Next() {
		var event TaskEvent
		var requestID string
		var createdAt string
		if err := rows.Scan(&event.ID, &event.TaskID, &event.DeploymentID, &requestID, &event.Level, &event.Message, &createdAt); err != nil {
			return nil, err
		}
		event.RequestID = requestID
		event.CreatedAt = parseTime(createdAt)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) DeploymentCounts(ctx context.Context) (map[string]int64, error) {
	return s.counts(ctx, "deployments")
}

func (s *Store) LatestDeploymentPerApp(ctx context.Context) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT *
FROM deployments d
WHERE id = (
  SELECT id
  FROM deployments d2
  WHERE d2.app_name = d.app_name
  ORDER BY created_at DESC, id DESC
  LIMIT 1
)
ORDER BY app_name ASC;
`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDeployments(rows)
}

func (s *Store) LatestRunningDeploymentPerApp(ctx context.Context) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT *
FROM deployments d
WHERE status='running' AND id = (
  SELECT id
  FROM deployments d2
  WHERE d2.app_name = d.app_name
    AND d2.status='running'
  ORDER BY created_at DESC, id DESC
  LIMIT 1
)
ORDER BY app_name ASC;
`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDeployments(rows)
}

func (s *Store) TaskCounts(ctx context.Context) (map[string]int64, error) {
	return s.counts(ctx, "tasks")
}

func (s *Store) counts(ctx context.Context, table string) (map[string]int64, error) {
	var query string
	switch table {
	case "deployments":
		query = `SELECT status, COUNT(*) AS count FROM deployments GROUP BY status;`
	case "tasks":
		query = `SELECT status, COUNT(*) AS count FROM tasks GROUP BY status;`
	default:
		return nil, fmt.Errorf("invalid table %q", table)
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

func scanRepoCredential(row scanner) (RepoCredential, error) {
	var cred RepoCredential
	var createdAt, updatedAt string
	err := row.Scan(&cred.RepoFullName, &cred.Nonce, &cred.Ciphertext, &createdAt, &updatedAt)
	if err != nil {
		return RepoCredential{}, err
	}
	cred.CreatedAt = parseTime(createdAt)
	cred.UpdatedAt = parseTime(updatedAt)
	return cred, nil
}

func scanAllowedRepo(row scanner) (AllowedRepo, error) {
	var repo AllowedRepo
	var createdAt, updatedAt string
	err := row.Scan(&repo.RepoFullName, &repo.Source, &createdAt, &updatedAt)
	if err != nil {
		return AllowedRepo{}, err
	}
	repo.CreatedAt = parseTime(createdAt)
	repo.UpdatedAt = parseTime(updatedAt)
	return repo, nil
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

//go:embed migrations/001_initial_schema.sql
var schemaSQL string

//go:embed migrations/002_repo_credentials.sql
var repoCredentialsMigrationSQL string

//go:embed migrations/003_observability.sql
var observabilityMigrationSQL string

//go:embed migrations/004_deployment_retry_state.sql
var deploymentRetryMigrationSQL string

//go:embed migrations/005_allowed_repos.sql
var allowedReposMigrationSQL string
