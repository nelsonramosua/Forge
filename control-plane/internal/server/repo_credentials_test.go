package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"forge/control-plane/internal/config"
	"forge/control-plane/internal/forgeyaml"
	"forge/control-plane/internal/store"
	"forge/control-plane/internal/vault"
)

func newRepoCredentialTestServer(t *testing.T) (*Server, http.Handler, *store.Store) {
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
		AllowedRepos:     []string{"example/private"},
		AllowedBranches:  []string{"main"},
		DefaultAgentRoot: "/tmp/forge-agent/apps",
		OnlineWindow:     15,
		AdminAppName:     "admin",
		AdminAppRepo:     "example/admin",
	}, st, vt)
	handler := srv.routes(context.Background())
	return srv, handler, st
}

func doJSON(handler http.Handler, method string, path string, body string, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestRepoCredentialEndpointsDoNotRevealToken(t *testing.T) {
	_, handler, _ := newRepoCredentialTestServer(t)
	token := "github_pat_example_private_repo_token"
	res := doJSON(handler, http.MethodPut, "/api/v1/repos/example/private/credential", `{"token":"`+token+`"}`, "admin-token")
	if res.Code != http.StatusOK {
		t.Fatalf("PUT credential: expected %d, got %d body=%s", http.StatusOK, res.Code, res.Body.String())
	}
	res = doJSON(handler, http.MethodGet, "/api/v1/repos/example/private/credential", "", "admin-token")
	if res.Code != http.StatusOK {
		t.Fatalf("GET credential: expected %d, got %d body=%s", http.StatusOK, res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), token) || strings.Contains(res.Body.String(), "github_pat_") {
		t.Fatalf("credential GET leaked token: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"has_credential":true`) {
		t.Fatalf("expected has_credential true, got %s", res.Body.String())
	}
}

func TestTaskPayloadDoesNotPersistRepoCredential(t *testing.T) {
	srv, _, st := newRepoCredentialTestServer(t)
	ctx := context.Background()
	nonce, ciphertext, err := srv.vault.Encrypt("github_pat_example_private_repo_token", repoCredentialAAD("example/private"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoCredential(ctx, store.RepoCredential{
		RepoFullName: "example/private",
		Nonce:        nonce,
		Ciphertext:   ciphertext,
	}); err != nil {
		t.Fatal(err)
	}
	payload, err := srv.taskPayload(ctx, store.Deployment{
		ID:        1,
		AppName:   "private",
		RepoURL:   "https://github.com/example/private.git",
		CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		Branch:    "main",
		Host:      "private.nforge.space",
	}, forgeyaml.Config{Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "github_pat_") {
		t.Fatalf("task payload leaked token: %s", text)
	}
	if !strings.Contains(text, `"repo_credential_required":true`) {
		t.Fatalf("expected repo_credential_required true, got %s", text)
	}
}

func TestAgentRepoCredentialLeaseIsScopedToClaimedTask(t *testing.T) {
	srv, handler, st := newRepoCredentialTestServer(t)
	ctx := context.Background()
	if err := st.UpsertAgent(ctx, store.Agent{ID: "worker-1", Hostname: "worker-1", Address: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateDeployment(ctx, store.Deployment{
		AppName:    "private",
		RepoURL:    "https://github.com/example/private.git",
		CommitSHA:  "0123456789abcdef0123456789abcdef01234567",
		Branch:     "main",
		Status:     "building",
		ConfigJSON: "{}",
		Host:       "private.nforge.space",
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateTask(ctx, store.Task{
		DeploymentID: deployment.ID,
		AgentID:      "worker-1",
		Type:         "build",
		Status:       "in_progress",
		PayloadJSON:  "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "github_pat_example_private_repo_token"
	nonce, ciphertext, err := srv.vault.Encrypt(token, repoCredentialAAD("example/private"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoCredential(ctx, store.RepoCredential{
		RepoFullName: "example/private",
		Nonce:        nonce,
		Ciphertext:   ciphertext,
	}); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/agents/worker-1/tasks/" + strconv.FormatInt(task.ID, 10) + "/repo-credential"
	res := doJSON(handler, http.MethodPost, path, "", "agent-token")
	if res.Code != http.StatusOK {
		t.Fatalf("lease: expected %d, got %d body=%s", http.StatusOK, res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), token) {
		t.Fatalf("lease did not include token for claimed task: %s", res.Body.String())
	}
	res = doJSON(handler, http.MethodPost, strings.Replace(path, "worker-1", "worker-2", 1), "", "agent-token")
	if res.Code != http.StatusForbidden {
		t.Fatalf("wrong agent: expected %d, got %d", http.StatusForbidden, res.Code)
	}
}

func TestValidateReservedAdminSubdomain(t *testing.T) {
	srv, _, _ := newRepoCredentialTestServer(t)
	if err := srv.validateReservedSubdomain("admin", "example/private", "admin"); err == nil {
		t.Fatal("expected non-admin repo to be rejected for admin subdomain")
	}
	if err := srv.validateReservedSubdomain("admin", "example/admin", "admin"); err != nil {
		t.Fatalf("expected configured admin app to be allowed: %v", err)
	}
}
