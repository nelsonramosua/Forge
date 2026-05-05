package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr          string
	DBPath        string
	WorkDir       string
	BaseDomain    string
	WebhookSecret string
	AgentToken    string
	AdminToken    string
	MasterKey     []byte
	AllowedRepos  []string
	AdminAppName  string
	AdminAppRepo  string

	CaddyAdminURL string

	OnlineWindow           time.Duration
	SchedulerTick          time.Duration
	TaskPollTimeout        time.Duration
	DeploymentLeaseTimeout time.Duration
	TaskLeaseTimeout       time.Duration
	DefaultAgentRoot       string
	AppPortStart           int
	AppPortEnd             int
	AllowedBranches        []string
	AllowLocalRepos        bool
	MaxScheduleBatch       int
	MaxTasksPerAgent       int
}

func FromEnv() (Config, error) {
	cfg := Config{
		Addr:                   env("FORGE_ADDR", ":8080"),
		DBPath:                 env("FORGE_DB_PATH", "data/forge.db"),
		WorkDir:                env("FORGE_WORK_DIR", "data/work"),
		BaseDomain:             env("FORGE_BASE_DOMAIN", "forge.localhost"),
		WebhookSecret:          os.Getenv("FORGE_GITHUB_WEBHOOK_SECRET"),
		AgentToken:             os.Getenv("FORGE_AGENT_TOKEN"),
		AdminToken:             os.Getenv("FORGE_ADMIN_TOKEN"),
		AdminAppName:           env("FORGE_ADMIN_APP_NAME", "admin"),
		AdminAppRepo:           strings.TrimSpace(os.Getenv("FORGE_ADMIN_APP_REPO")),
		CaddyAdminURL:          strings.TrimRight(os.Getenv("FORGE_CADDY_ADMIN_URL"), "/"),
		OnlineWindow:           envDuration("FORGE_ONLINE_WINDOW", 15*time.Second),
		SchedulerTick:          envDuration("FORGE_SCHEDULER_TICK", 2*time.Second),
		TaskPollTimeout:        envDuration("FORGE_TASK_POLL_TIMEOUT", 25*time.Second),
		DeploymentLeaseTimeout: envDuration("FORGE_DEPLOYMENT_LEASE_TIMEOUT", 15*time.Minute),
		TaskLeaseTimeout:       envDuration("FORGE_TASK_LEASE_TIMEOUT", 15*time.Minute),
		DefaultAgentRoot:       env("FORGE_AGENT_APP_ROOT", "/var/lib/forge-agent/apps"),
		AppPortStart:           envInt("FORGE_APP_PORT_START", 20000),
		AppPortEnd:             envInt("FORGE_APP_PORT_END", 39999),
		AllowedRepos:           envList("FORGE_ALLOWED_REPOS"),
		AllowedBranches:        envList("FORGE_ALLOWED_BRANCHES"),
		AllowLocalRepos:        envBool("FORGE_ALLOW_LOCAL_REPOS", false),
		MaxScheduleBatch:       envInt("FORGE_MAX_SCHEDULE_BATCH", 20),
		MaxTasksPerAgent:       envInt("FORGE_MAX_TASKS_PER_AGENT", 1),
	}
	if cfg.AppPortStart <= 0 || cfg.AppPortEnd <= 0 || cfg.AppPortStart > cfg.AppPortEnd {
		cfg.AppPortStart = 20000
		cfg.AppPortEnd = 39999
	}
	if len(cfg.AllowedBranches) == 0 {
		cfg.AllowedBranches = []string{"main"}
	}
	if cfg.MaxScheduleBatch <= 0 {
		cfg.MaxScheduleBatch = 20
	}
	if cfg.MaxTasksPerAgent <= 0 {
		cfg.MaxTasksPerAgent = 1
	}
	if cfg.DeploymentLeaseTimeout <= 0 {
		cfg.DeploymentLeaseTimeout = 15 * time.Minute
	}
	if cfg.TaskLeaseTimeout <= 0 {
		cfg.TaskLeaseTimeout = 15 * time.Minute
	}
	masterKey, err := parseMasterKey(os.Getenv("FORGE_MASTER_KEY"))
	if err != nil {
		return Config{}, err
	}
	cfg.MasterKey = masterKey
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	required := map[string]string{
		"FORGE_AGENT_TOKEN":           cfg.AgentToken,
		"FORGE_ADMIN_TOKEN":           cfg.AdminToken,
		"FORGE_GITHUB_WEBHOOK_SECRET": cfg.WebhookSecret,
	}
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", key)
		}
	}
	if len(cfg.MasterKey) != 32 {
		return fmt.Errorf("FORGE_MASTER_KEY must decode to exactly 32 bytes")
	}
	if len(cfg.AllowedRepos) == 0 {
		return fmt.Errorf("FORGE_ALLOWED_REPOS must contain at least one owner/repo entry")
	}
	for _, repo := range cfg.AllowedRepos {
		if !isSafeRepoName(repo) {
			return fmt.Errorf("FORGE_ALLOWED_REPOS contains invalid repo %q", repo)
		}
	}
	if !isSafeAppName(cfg.AdminAppName) {
		return fmt.Errorf("FORGE_ADMIN_APP_NAME contains invalid app name %q", cfg.AdminAppName)
	}
	if cfg.AdminAppRepo != "" && !isSafeRepoName(cfg.AdminAppRepo) {
		return fmt.Errorf("FORGE_ADMIN_APP_REPO contains invalid repo %q", cfg.AdminAppRepo)
	}
	if len(cfg.AllowedBranches) == 0 {
		return fmt.Errorf("FORGE_ALLOWED_BRANCHES must contain at least one branch")
	}
	for _, branch := range cfg.AllowedBranches {
		if !isSafeBranchName(branch) {
			return fmt.Errorf("FORGE_ALLOWED_BRANCHES contains invalid branch %q", branch)
		}
	}
	return nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if d, err := time.ParseDuration(value); err == nil {
		return d
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func envList(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		folded := strings.ToLower(item)
		if seen[folded] {
			continue
		}
		seen[folded] = true
		out = append(out, item)
	}
	return out
}

func parseMasterKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("FORGE_MASTER_KEY is required")
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(value) == 32 {
		return []byte(value), nil
	}
	return nil, fmt.Errorf("FORGE_MASTER_KEY must be base64, hex, or raw text that decodes to exactly 32 bytes")
}

func isSafeRepoName(value string) bool {
	owner, repo, ok := strings.Cut(value, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return false
	}
	return isSafePathPart(owner) && isSafePathPart(repo)
}

func isSafePathPart(value string) bool {
	if value == "" || value == "." || value == ".." || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "-") {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func isSafeAppName(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func isSafeBranchName(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	if strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.Contains(value, "\\") {
		return false
	}
	if strings.HasSuffix(value, ".lock") {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '/', '.', '_', '-':
			continue
		default:
			return false
		}
	}
	return true
}
