package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("FORGE_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("FORGE_AGENT_TOKEN", "agent-token")
	t.Setenv("FORGE_ADMIN_TOKEN", "admin-token")
	t.Setenv("FORGE_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("FORGE_ALLOWED_REPOS", "example/release-board")
	t.Setenv("FORGE_ALLOWED_BRANCHES", "main")
}

func TestFromEnvRequiresSecretsAndAllowlist(t *testing.T) {
	setValidEnv(t)

	tests := []string{
		"FORGE_MASTER_KEY",
		"FORGE_AGENT_TOKEN",
		"FORGE_ADMIN_TOKEN",
		"FORGE_GITHUB_WEBHOOK_SECRET",
		"FORGE_ALLOWED_REPOS",
	}

	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv(key, "")
			if _, err := FromEnv(); err == nil {
				t.Fatalf("expected %s to be required", key)
			}
		})
	}
}

func TestFromEnvAcceptsValidProductionConfig(t *testing.T) {
	setValidEnv(t)
	t.Setenv("FORGE_ADMIN_APP_REPO", "example/admin")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MasterKey) != 32 {
		t.Fatalf("expected 32-byte master key, got %d", len(cfg.MasterKey))
	}
	if got := strings.Join(cfg.AllowedRepos, ","); got != "example/release-board" {
		t.Fatalf("unexpected allowed repos %q", got)
	}
	if got := strings.Join(cfg.AllowedBranches, ","); got != "main" {
		t.Fatalf("unexpected allowed branches %q", got)
	}
	if cfg.AdminAppName != "admin" || cfg.AdminAppRepo != "example/admin" {
		t.Fatalf("unexpected admin app config: name=%q repo=%q", cfg.AdminAppName, cfg.AdminAppRepo)
	}
}

func TestFromEnvRejectsUnsafeAllowlistValues(t *testing.T) {
	setValidEnv(t)
	t.Setenv("FORGE_ALLOWED_REPOS", "../repo")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected invalid repo to be rejected")
	}

	setValidEnv(t)
	t.Setenv("FORGE_ALLOWED_BRANCHES", "--upload-pack")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected invalid branch to be rejected")
	}

	setValidEnv(t)
	t.Setenv("FORGE_ADMIN_APP_REPO", "../admin")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected invalid admin app repo to be rejected")
	}
}
