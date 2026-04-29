package store

import (
	"context"
	"path/filepath"
	"testing"
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
	if err := st.CompleteTask(ctx, task.ID, "succeeded"); err != nil {
		t.Fatal(err)
	}
	counts, err := st.TaskCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts["succeeded"] != 1 {
		t.Fatalf("unexpected task counts: %+v", counts)
	}
}
