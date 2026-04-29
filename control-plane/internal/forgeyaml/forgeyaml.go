package forgeyaml

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Name      string         `json:"name" yaml:"name"`
	Runtime   string         `json:"runtime" yaml:"runtime"`
	Build     BuildConfig    `json:"build" yaml:"build"`
	Run       RunConfig      `json:"run" yaml:"run"`
	Resources ResourceConfig `json:"resources" yaml:"resources"`
	Health    HealthConfig   `json:"health" yaml:"health"`
	Env       []string       `json:"env" yaml:"env"`
}

type BuildConfig struct {
	Commands []string `json:"commands" yaml:"commands"`
}

type RunConfig struct {
	Command string `json:"command" yaml:"command"`
	Port    int    `json:"port" yaml:"port"`
}

type ResourceConfig struct {
	Memory      string  `json:"memory" yaml:"memory"`
	MemoryBytes int64   `json:"memory_bytes" yaml:"-"`
	CPU         float64 `json:"cpu" yaml:"cpu"`
}

type HealthConfig struct {
	Path     string `json:"path" yaml:"path"`
	Interval string `json:"interval" yaml:"interval"`
	Timeout  string `json:"timeout" yaml:"timeout"`
	Retries  int    `json:"retries" yaml:"retries"`
}

func Parse(data []byte) (Config, error) {
	cfg := Config{
		Health: HealthConfig{
			Path:     "/",
			Interval: "10s",
			Timeout:  "3s",
			Retries:  3,
		},
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse forge.yaml: %w", err)
	}
	return validate(cfg)
}

func validate(cfg Config) (Config, error) {
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Runtime = strings.TrimSpace(cfg.Runtime)
	cfg.Resources.Memory = strings.TrimSpace(cfg.Resources.Memory)
	cfg.Health.Path = strings.TrimSpace(cfg.Health.Path)
	cfg.Health.Interval = strings.TrimSpace(cfg.Health.Interval)
	cfg.Health.Timeout = strings.TrimSpace(cfg.Health.Timeout)

	if cfg.Name == "" {
		return Config{}, fmt.Errorf("name is required")
	}
	if cfg.Runtime == "" {
		return Config{}, fmt.Errorf("runtime is required")
	}
	if len(cfg.Build.Commands) == 0 {
		return Config{}, fmt.Errorf("build.commands must contain at least one command")
	}
	for i, command := range cfg.Build.Commands {
		if strings.TrimSpace(command) == "" {
			return Config{}, fmt.Errorf("build.commands[%d] cannot be empty", i)
		}
	}
	if strings.TrimSpace(cfg.Run.Command) == "" {
		return Config{}, fmt.Errorf("run.command is required")
	}
	if cfg.Run.Port <= 0 || cfg.Run.Port > 65535 {
		return Config{}, fmt.Errorf("run.port must be between 1 and 65535")
	}
	if cfg.Resources.Memory == "" {
		return Config{}, fmt.Errorf("resources.memory is required")
	}
	memoryBytes, err := ParseMemory(cfg.Resources.Memory)
	if err != nil {
		return Config{}, err
	}
	cfg.Resources.MemoryBytes = memoryBytes
	if cfg.Resources.CPU <= 0 {
		return Config{}, fmt.Errorf("resources.cpu must be greater than 0")
	}
	if cfg.Health.Path == "" {
		cfg.Health.Path = "/"
	}
	if _, err := time.ParseDuration(cfg.Health.Interval); err != nil {
		return Config{}, fmt.Errorf("invalid health interval %q", cfg.Health.Interval)
	}
	if _, err := time.ParseDuration(cfg.Health.Timeout); err != nil {
		return Config{}, fmt.Errorf("invalid health timeout %q", cfg.Health.Timeout)
	}
	if cfg.Health.Retries <= 0 {
		return Config{}, fmt.Errorf("health.retries must be greater than 0")
	}
	for i, key := range cfg.Env {
		key = strings.TrimSpace(key)
		if key == "" {
			return Config{}, fmt.Errorf("env[%d] cannot be empty", i)
		}
		cfg.Env[i] = key
	}
	return cfg, nil
}

func ParseMemory(value string) (int64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return 0, fmt.Errorf("memory cannot be empty")
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(value, "GI"):
		multiplier = 1024 * 1024 * 1024
		value = strings.TrimSuffix(value, "GI")
	case strings.HasSuffix(value, "G"):
		multiplier = 1024 * 1024 * 1024
		value = strings.TrimSuffix(value, "G")
	case strings.HasSuffix(value, "MI"):
		multiplier = 1024 * 1024
		value = strings.TrimSuffix(value, "MI")
	case strings.HasSuffix(value, "M"):
		multiplier = 1024 * 1024
		value = strings.TrimSuffix(value, "M")
	case strings.HasSuffix(value, "KI"):
		multiplier = 1024
		value = strings.TrimSuffix(value, "KI")
	case strings.HasSuffix(value, "K"):
		multiplier = 1024
		value = strings.TrimSuffix(value, "K")
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid memory value")
	}
	return int64(number * float64(multiplier)), nil
}
