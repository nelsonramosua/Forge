package config

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

	CaddyAdminURL string

	OnlineWindow     time.Duration
	SchedulerTick    time.Duration
	TaskPollTimeout  time.Duration
	DefaultAgentRoot string
	AppPortStart     int
	AppPortEnd       int
}

func FromEnv() Config {
	cfg := Config{
		Addr:             env("FORGE_ADDR", ":8080"),
		DBPath:           env("FORGE_DB_PATH", "data/forge.db"),
		WorkDir:          env("FORGE_WORK_DIR", "data/work"),
		BaseDomain:       env("FORGE_BASE_DOMAIN", "forge.localhost"),
		WebhookSecret:    os.Getenv("FORGE_GITHUB_WEBHOOK_SECRET"),
		AgentToken:       os.Getenv("FORGE_AGENT_TOKEN"),
		AdminToken:       os.Getenv("FORGE_ADMIN_TOKEN"),
		CaddyAdminURL:    strings.TrimRight(os.Getenv("FORGE_CADDY_ADMIN_URL"), "/"),
		OnlineWindow:     envDuration("FORGE_ONLINE_WINDOW", 15*time.Second),
		SchedulerTick:    envDuration("FORGE_SCHEDULER_TICK", 2*time.Second),
		TaskPollTimeout:  envDuration("FORGE_TASK_POLL_TIMEOUT", 25*time.Second),
		DefaultAgentRoot: env("FORGE_AGENT_APP_ROOT", "/var/lib/forge-agent/apps"),
		AppPortStart:     envInt("FORGE_APP_PORT_START", 20000),
		AppPortEnd:       envInt("FORGE_APP_PORT_END", 39999),
	}
	if cfg.AppPortStart <= 0 || cfg.AppPortEnd <= 0 || cfg.AppPortStart > cfg.AppPortEnd {
		cfg.AppPortStart = 20000
		cfg.AppPortEnd = 39999
	}
	cfg.MasterKey = parseMasterKey(os.Getenv("FORGE_MASTER_KEY"))
	return cfg
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

func parseMasterKey(value string) []byte {
	value = strings.TrimSpace(value)
	if value == "" {
		sum := sha256.Sum256([]byte("forge-development-master-key"))
		return sum[:]
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded
	}
	if len(value) == 32 {
		return []byte(value)
	}
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}
