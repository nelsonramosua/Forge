package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreDeploymentAndTaskLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgent(ctx, Agent{
		ID:             "worker-1",
		Hostname:       "worker-1",
		Address:        "127.0.0.1",
		CPUCapacity:    2,
		MemoryCapacity: 1024 * 1024 * 1024,
	}); err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateDeployment(ctx, Deployment{
		AppName:    "myapp",
		RepoURL:    "https://example.com/repo.git",
		CommitSHA:  "abc123",
		Branch:     "main",
		Status:     "pending",
		ConfigJSON: "{}",
		Host:       "myapp.forge.localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	if deployment.ID == 0 {
		t.Fatal("expected deployment id")
	}
	if err := st.UpdateDeploymentAssignment(ctx, deployment.ID, "worker-1", "building"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDeploymentTargetPort(ctx, deployment.ID, 20000); err != nil {
		t.Fatal(err)
	}
	ports, err := st.UsedTargetPorts(ctx, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ports[20000] {
		t.Fatalf("expected target port to be tracked, got %+v", ports)
	}
	task, err := st.CreateTask(ctx, Task{
		DeploymentID: deployment.ID,
		AgentID:      "worker-1",
		Type:         "build",
		Status:       "pending",
		PayloadJSON:  "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == 0 {
		t.Fatal("expected task id")
	}
	claimed, err := st.ClaimNextTask(ctx, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != task.ID || claimed.Status != "in_progress" {
		t.Fatalf("unexpected claimed task: %+v", claimed)
	}
	active, err := st.ActiveTaskCountsByAgent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active["worker-1"] != 1 {
		t.Fatalf("expected one active task for worker-1, got %+v", active)
	}
	if err := st.CompleteTask(ctx, task.ID, "succeeded"); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteTask(ctx, task.ID, "succeeded"); err != ErrTaskAlreadyCompleted {
		t.Fatalf("expected ErrTaskAlreadyCompleted, got %v", err)
	}
	counts, err := st.TaskCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts["succeeded"] != 1 {
		t.Fatalf("unexpected task counts: %+v", counts)
	}
}

func TestRepoCredentialLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	cred := RepoCredential{
		RepoFullName: "example/private",
		Nonce:        "nonce",
		Ciphertext:   "ciphertext",
	}
	if err := st.SetRepoCredential(ctx, cred); err != nil {
		t.Fatal(err)
	}
	if has, err := st.HasRepoCredential(ctx, cred.RepoFullName); err != nil || !has {
		t.Fatalf("expected credential to exist, has=%v err=%v", has, err)
	}
	got, ok, err := st.GetRepoCredential(ctx, cred.RepoFullName)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.Nonce != cred.Nonce || got.Ciphertext != cred.Ciphertext {
		t.Fatalf("unexpected credential: ok=%v got=%+v", ok, got)
	}
	if err := st.DeleteRepoCredential(ctx, cred.RepoFullName); err != nil {
		t.Fatal(err)
	}
	if has, err := st.HasRepoCredential(ctx, cred.RepoFullName); err != nil || has {
		t.Fatalf("expected credential to be deleted, has=%v err=%v", has, err)
	}
}

func TestDeploymentRetryStateLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateDeployment(ctx, Deployment{
		AppName:    "myapp",
		RepoURL:    "https://example.com/repo.git",
		CommitSHA:  "abc123",
		Branch:     "main",
		Status:     "pending",
		ConfigJSON: "{}",
		Host:       "myapp.forge.localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateDeploymentStatus(ctx, deployment.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	nextRetryAt := time.Now().Add(5 * time.Minute)
	if err := st.UpsertDeploymentRetryState(ctx, deployment.ID, 2, nextRetryAt, "temporary failure"); err != nil {
		t.Fatal(err)
	}
	state, ok, err := st.DeploymentRetryStateByDeploymentID(ctx, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected retry state to exist")
	}
	if state.AttemptCount != 2 || state.LastFailureReason != "temporary failure" || state.NextRetryAt.IsZero() {
		t.Fatalf("unexpected retry state: %+v", state)
	}
	if err := st.QueueDeploymentRetry(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	queued, ok, err := st.GetDeployment(ctx, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || queued.Status != "pending" {
		t.Fatalf("expected deployment to be requeued, got %+v", queued)
	}
	state, ok, err = st.DeploymentRetryStateByDeploymentID(ctx, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || state.AttemptCount != 2 {
		t.Fatalf("expected retry state to be preserved on auto retry, got ok=%v state=%+v", ok, state)
	}
	if err := st.UpdateDeploymentStatus(ctx, deployment.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	if err := st.RetryDeployment(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.DeploymentRetryStateByDeploymentID(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("expected retry state to be cleared after manual retry")
	}
}

func TestRunningDeploymentsByHostExcludingReturnsAllStaleRunning(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.CreateDeployment(ctx, Deployment{
		AppName:   "app",
		RepoURL:   "https://example.com/repo.git",
		CommitSHA: "abc123",
		Branch:    "main",
		Status:    "running",
		Host:      "app.forge.localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateDeployment(ctx, Deployment{
		AppName:   "app",
		RepoURL:   "https://example.com/repo.git",
		CommitSHA: "def456",
		Branch:    "main",
		Status:    "running",
		Host:      "app.forge.localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := st.CreateDeployment(ctx, Deployment{
		AppName:   "app",
		RepoURL:   "https://example.com/repo.git",
		CommitSHA: "fedcba",
		Branch:    "main",
		Status:    "running",
		Host:      "app.forge.localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	deployments, err := st.RunningDeploymentsByHostExcluding(ctx, "app.forge.localhost", current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 2 {
		t.Fatalf("expected two previous running deployments, got %+v", deployments)
	}
	if deployments[0].ID != second.ID || deployments[1].ID != first.ID {
		t.Fatalf("expected newest previous deployments first, got %+v", deployments)
	}
}

func TestLatestRunningDeploymentPerAppReturnsActiveDeployment(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.CreateDeployment(ctx, Deployment{
		AppName:   "app",
		RepoURL:   "https://example.com/repo.git",
		CommitSHA: "abc123",
		Branch:    "main",
		Status:    "running",
		Host:      "app.forge.localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateDeployment(ctx, Deployment{
		AppName:   "app",
		RepoURL:   "https://example.com/repo.git",
		CommitSHA: "def456",
		Branch:    "main",
		Status:    "failed",
		Host:      "app.forge.localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateDeployment(ctx, Deployment{
		AppName:   "app",
		RepoURL:   "https://example.com/repo.git",
		CommitSHA: "fedcba",
		Branch:    "main",
		Status:    "running",
		Host:      "app.forge.localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	deployments, err := st.LatestRunningDeploymentPerApp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 1 {
		t.Fatalf("expected one active deployment, got %+v", deployments)
	}
	if deployments[0].ID != second.ID {
		t.Fatalf("expected newest running deployment %d, got %+v", second.ID, deployments)
	}
	if deployments[0].AppName != "app" {
		t.Fatalf("unexpected app view: %+v", deployments[0])
	}
	if first.ID == 0 {
		t.Fatal("expected first deployment to exist")
	}
}

func TestRepoCredentialMigrationRunsOnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "forge.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS legacy_marker (id INTEGER PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoCredential(ctx, RepoCredential{
		RepoFullName: "example/private",
		Nonce:        "nonce",
		Ciphertext:   "ciphertext",
	}); err != nil {
		t.Fatal(err)
	}
}
