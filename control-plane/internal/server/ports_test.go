package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"forge/control-plane/internal/config"
	"forge/control-plane/internal/store"
)

func TestChooseDeploymentPortStartsFromDeploymentID(t *testing.T) {
	port, err := chooseDeploymentPort(3, map[int]bool{}, 20000, 20010)
	if err != nil {
		t.Fatal(err)
	}
	if port != 20002 {
		t.Fatalf("expected port 20002, got %d", port)
	}
}

func TestChooseDeploymentPortSkipsUsedPorts(t *testing.T) {
	port, err := chooseDeploymentPort(1, map[int]bool{20000: true, 20001: true}, 20000, 20003)
	if err != nil {
		t.Fatal(err)
	}
	if port != 20002 {
		t.Fatalf("expected port 20002, got %d", port)
	}
}

func TestChooseDeploymentPortWraps(t *testing.T) {
	port, err := chooseDeploymentPort(4, map[int]bool{20002: true}, 20000, 20002)
	if err != nil {
		t.Fatal(err)
	}
	if port != 20000 {
		t.Fatalf("expected port 20000, got %d", port)
	}
}

func TestChooseDeploymentPortFull(t *testing.T) {
	_, err := chooseDeploymentPort(1, map[int]bool{20000: true, 20001: true}, 20000, 20001)
	if err == nil {
		t.Fatal("expected error for full port range")
	}
}

func TestChooseDeploymentPortForAgentSkipsBoundPort(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	start := port
	end := port + 1
	if port == 65535 {
		start = port - 1
		end = port
	}
	chosen, err := chooseDeploymentPortForAgent(1, map[int]bool{}, start, end, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if chosen == port {
		t.Fatalf("expected bound port %d to be skipped", port)
	}
	if port == 65535 {
		if chosen != port-1 {
			t.Fatalf("expected fallback port %d, got %d", port-1, chosen)
		}
		return
	}
	if chosen != port+1 {
		t.Fatalf("expected fallback port %d, got %d", port+1, chosen)
	}
}

func TestValidCommitSHA(t *testing.T) {
	if !validCommitSHA("0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("expected 40-char hex sha to be valid")
	}
	if validCommitSHA("--upload-pack=sh") {
		t.Fatal("expected git option injection to be invalid")
	}
}

func TestBranchFromRef(t *testing.T) {
	branch, ok := branchFromRef("refs/heads/main")
	if !ok || branch != "main" {
		t.Fatalf("expected main branch, got %q ok=%v", branch, ok)
	}
	if _, ok := branchFromRef("refs/tags/v1.0.0"); ok {
		t.Fatal("expected tags to be rejected")
	}
	if _, ok := branchFromRef("refs/heads/--upload-pack"); ok {
		t.Fatal("expected unsafe branch to be rejected")
	}
}

func TestValidRepoName(t *testing.T) {
	if !validRepoName("example/release-board") {
		t.Fatal("expected owner/repo to be valid")
	}
	if validRepoName("example/repo/extra") {
		t.Fatal("expected nested repo path to be invalid")
	}
	if validRepoName("../repo") {
		t.Fatal("expected path traversal repo to be invalid")
	}
}

func TestAgentsWithTaskCapacity(t *testing.T) {
	agents := []store.Agent{{ID: "busy"}, {ID: "free"}}
	available := agentsWithTaskCapacity(agents, map[string]int{"busy": 1}, 1)
	if len(available) != 1 || available[0].ID != "free" {
		t.Fatalf("unexpected available agents: %+v", available)
	}
}

func TestTLSAskAllowsBaseDomainAndRunningDeploymentHost(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateDeployment(ctx, store.Deployment{
		AppName:   "release-board",
		RepoURL:   "https://github.com/example/release-board.git",
		CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		Branch:    "main",
		Status:    "pending",
		Host:      "release-board.nforge.space",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateDeploymentAssignment(ctx, deployment.ID, "worker-1", "building"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateDeploymentStatus(ctx, deployment.ID, "deploying"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDeploymentRunning(ctx, deployment.ID, 20000); err != nil {
		t.Fatal(err)
	}
	srv := New(config.Config{BaseDomain: "nforge.space"}, st, nil)
	handler := srv.routes(ctx)

	tests := []struct {
		domain string
		code   int
	}{
		{"nforge.space", http.StatusOK},
		{"release-board.nforge.space", http.StatusOK},
		{"missing.nforge.space", http.StatusForbidden},
		{"evil.example.com", http.StatusForbidden},
		{"*.nforge.space", http.StatusForbidden},
	}
	for _, tt := range tests {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/tls/ask?domain="+tt.domain, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != tt.code {
			t.Fatalf("domain %q: expected %d, got %d", tt.domain, tt.code, res.Code)
		}
	}
}
