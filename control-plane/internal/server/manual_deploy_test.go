package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"forge/control-plane/internal/config"
	"forge/control-plane/internal/store"
	"forge/control-plane/internal/vault"
)

func newManualDeployTestServer(t *testing.T) (*Server, http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	vt, err := vault.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New(config.Config{
		BaseDomain:       "nforge.space",
		AgentToken:       "agent-token",
		AdminToken:       "admin-token",
		AllowedRepos:     []string{"example/app", "example/private"},
		AllowedBranches:  []string{"main"},
		DefaultAgentRoot: "/tmp/forge-agent/apps",
		OnlineWindow:     15,
		AdminAppName:     "admin",
		AdminAppRepo:     "example/admin",
		WorkDir:          t.TempDir(),
	}, st, vt)
	return srv, srv.routes(context.Background()), st
}

func TestManualDeploymentResolvesHeadServerSide(t *testing.T) {
	_, handler, st := newManualDeployTestServer(t)
	repo := newLocalForgeRepo(t, "app", "main")
	configureGitInsteadOf(t, "https://github.com/example/app.git", repo)
	head := gitOutput(t, repo, "rev-parse", "HEAD")

	res := doJSON(handler, http.MethodPost, "/api/v1/deployments", `{"repo":"example/app","branch":"main"}`, "admin-token")
	if res.Code != http.StatusAccepted {
		t.Fatalf("manual deploy: expected %d, got %d body=%s", http.StatusAccepted, res.Code, res.Body.String())
	}
	var view deploymentView
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.CommitSHA != head {
		t.Fatalf("expected resolved commit %s, got %s", head, view.CommitSHA)
	}
	if view.Status != "pending" || view.AppName != "app" || view.Host != "app.nforge.space" {
		t.Fatalf("unexpected deployment view: %+v", view)
	}
	deployments, err := st.ListDeployments(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 1 || deployments[0].CommitSHA != head {
		t.Fatalf("unexpected deployments: %+v", deployments)
	}
}

func TestManualDeploymentRejectsUnknownRepoBeforeCreatingDeployment(t *testing.T) {
	_, handler, st := newManualDeployTestServer(t)

	res := doJSON(handler, http.MethodPost, "/api/v1/deployments", `{"repo":"example/unknown","branch":"main"}`, "admin-token")
	if res.Code != http.StatusForbidden {
		t.Fatalf("manual deploy: expected %d, got %d body=%s", http.StatusForbidden, res.Code, res.Body.String())
	}
	deployments, err := st.ListDeployments(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 0 {
		t.Fatalf("expected no deployments, got %+v", deployments)
	}
}

func TestManualDeploymentCanceledContextDoesNotCreateDeployment(t *testing.T) {
	srv, _, st := newManualDeployTestServer(t)
	repo := newLocalForgeRepo(t, "app", "main")
	configureGitInsteadOf(t, "https://github.com/example/app.git", repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := srv.createDeploymentFromRepo(ctx, "https://github.com/example/app.git", "example/app", "main", "", manualDeployCloneTimeout); err == nil {
		t.Fatal("expected canceled context to fail")
	}
	deployments, err := st.ListDeployments(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 0 {
		t.Fatalf("expected no deployments, got %+v", deployments)
	}
}

func TestDeploymentRollbackUsesSourceRepoURLCommitAndReparsesForgeYAML(t *testing.T) {
	_, handler, st := newManualDeployTestServer(t)
	repo := newLocalForgeRepo(t, "app", "main")
	firstCommit := gitOutput(t, repo, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(repo, "forge.yaml"), forgeYAML("rollback-app"))
	gitRun(t, repo, "add", "forge.yaml")
	gitRun(t, repo, "commit", "-m", "rename app")
	configureGitInsteadOf(t, "https://github.com/example/app.git", repo)
	source, err := st.CreateDeployment(context.Background(), store.Deployment{
		AppName:    "stale-app",
		RepoURL:    "https://github.com/example/app.git",
		CommitSHA:  firstCommit,
		Branch:     "main",
		Status:     "failed",
		ConfigJSON: forgeYAML("stale-app"),
		Host:       "stale-app.nforge.space",
	})
	if err != nil {
		t.Fatal(err)
	}

	res := doJSON(handler, http.MethodPost, "/api/v1/deployments/"+strconvFormatInt(source.ID)+"/rollback", "", "admin-token")
	if res.Code != http.StatusAccepted {
		t.Fatalf("rollback: expected %d, got %d body=%s", http.StatusAccepted, res.Code, res.Body.String())
	}
	var view deploymentView
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.CommitSHA != firstCommit || view.AppName != "app" {
		t.Fatalf("rollback used wrong commit/config: %+v", view)
	}
	deployments, err := st.ListDeployments(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 2 {
		t.Fatalf("expected source plus rollback deployment, got %+v", deployments)
	}
	if strings.Contains(deployments[0].ConfigJSON, "stale-app") {
		t.Fatalf("rollback reused stale config json: %s", deployments[0].ConfigJSON)
	}
}

func TestRepoListingShowsCredentialStatusWithoutSecret(t *testing.T) {
	srv, handler, st := newManualDeployTestServer(t)
	nonce, ciphertext, err := srv.vault.Encrypt("github_pat_example_private_repo_token", repoCredentialAAD("example/private"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoCredential(context.Background(), store.RepoCredential{
		RepoFullName: "example/private",
		Nonce:        nonce,
		Ciphertext:   ciphertext,
	}); err != nil {
		t.Fatal(err)
	}

	res := doJSON(handler, http.MethodGet, "/api/v1/repos", "", "admin-token")
	if res.Code != http.StatusOK {
		t.Fatalf("repo listing: expected %d, got %d body=%s", http.StatusOK, res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"repo":"example/app"`) || !strings.Contains(body, `"repo":"example/private"`) || !strings.Contains(body, `"has_credential":true`) {
		t.Fatalf("unexpected repo listing: %s", body)
	}
	if strings.Contains(body, "github_pat_") {
		t.Fatalf("repo listing leaked credential: %s", body)
	}
}

func newLocalForgeRepo(t *testing.T, appName string, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", branch)
	gitRun(t, dir, "config", "user.email", "forge@example.test")
	gitRun(t, dir, "config", "user.name", "Forge Test")
	writeFile(t, filepath.Join(dir, "forge.yaml"), forgeYAML(appName))
	gitRun(t, dir, "add", "forge.yaml")
	gitRun(t, dir, "commit", "-m", "initial")
	return dir
}

func configureGitInsteadOf(t *testing.T, remote string, localRepo string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+localRepo+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", remote)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func forgeYAML(appName string) string {
	return "name: " + appName + "\n" +
		"runtime: python3.11\n" +
		"build:\n" +
		"  commands:\n" +
		"    - python3 -m py_compile app.py\n" +
		"run:\n" +
		"  command: python3 app.py\n" +
		"  port: 8000\n" +
		"resources:\n" +
		"  memory: 128M\n" +
		"  cpu: 0.25\n" +
		"health:\n" +
		"  path: /health\n" +
		"  interval: 10s\n" +
		"  timeout: 3s\n" +
		"  retries: 3\n"
}

func strconvFormatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
