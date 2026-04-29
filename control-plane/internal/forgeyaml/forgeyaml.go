package forgeyaml

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Name      string         `json:"name"`
	Runtime   string         `json:"runtime"`
	Build     BuildConfig    `json:"build"`
	Run       RunConfig      `json:"run"`
	Resources ResourceConfig `json:"resources"`
	Health    HealthConfig   `json:"health"`
	Env       []string       `json:"env"`
}

type BuildConfig struct {
	Commands []string `json:"commands"`
}

type RunConfig struct {
	Command string `json:"command"`
	Port    int    `json:"port"`
}

type ResourceConfig struct {
	Memory      string  `json:"memory"`
	MemoryBytes int64   `json:"memory_bytes"`
	CPU         float64 `json:"cpu"`
}

type HealthConfig struct {
	Path     string `json:"path"`
	Interval string `json:"interval"`
	Timeout  string `json:"timeout"`
	Retries  int    `json:"retries"`
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

	var section string
	var subsection string
	lines := strings.Split(string(data), "\n")
	for lineNumber, raw := range lines {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}

		indent := countIndent(line)
		trimmed := strings.TrimSpace(line)
		if indent == 0 {
			section = ""
			subsection = ""
			key, value, ok := splitKeyValue(trimmed)
			if !ok {
				return Config{}, fmt.Errorf("line %d: expected key: value", lineNumber+1)
			}
			switch key {
			case "name":
				cfg.Name = value
			case "runtime":
				cfg.Runtime = value
			case "build", "run", "resources", "health", "env":
				section = key
				if value != "" {
					return Config{}, fmt.Errorf("line %d: section %q cannot have an inline value", lineNumber+1, key)
				}
			default:
				return Config{}, fmt.Errorf("line %d: unknown top-level key %q", lineNumber+1, key)
			}
			continue
		}

		switch section {
		case "build":
			if indent == 2 {
				key, value, ok := splitKeyValue(trimmed)
				if !ok {
					return Config{}, fmt.Errorf("line %d: expected build key", lineNumber+1)
				}
				if key != "commands" || value != "" {
					return Config{}, fmt.Errorf("line %d: build supports only commands:", lineNumber+1)
				}
				subsection = "build.commands"
				continue
			}
			if indent == 4 && subsection == "build.commands" && strings.HasPrefix(trimmed, "- ") {
				cfg.Build.Commands = append(cfg.Build.Commands, unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
				continue
			}
			return Config{}, fmt.Errorf("line %d: invalid build entry", lineNumber+1)
		case "run":
			key, value, ok := splitKeyValue(trimmed)
			if indent != 2 || !ok {
				return Config{}, fmt.Errorf("line %d: invalid run entry", lineNumber+1)
			}
			switch key {
			case "command":
				cfg.Run.Command = unquote(value)
			case "port":
				port, err := strconv.Atoi(value)
				if err != nil || port <= 0 || port > 65535 {
					return Config{}, fmt.Errorf("line %d: invalid run port %q", lineNumber+1, value)
				}
				cfg.Run.Port = port
			default:
				return Config{}, fmt.Errorf("line %d: unknown run key %q", lineNumber+1, key)
			}
		case "resources":
			key, value, ok := splitKeyValue(trimmed)
			if indent != 2 || !ok {
				return Config{}, fmt.Errorf("line %d: invalid resources entry", lineNumber+1)
			}
			switch key {
			case "memory":
				bytes, err := ParseMemory(value)
				if err != nil {
					return Config{}, fmt.Errorf("line %d: %w", lineNumber+1, err)
				}
				cfg.Resources.Memory = value
				cfg.Resources.MemoryBytes = bytes
			case "cpu":
				cpu, err := strconv.ParseFloat(value, 64)
				if err != nil || cpu <= 0 {
					return Config{}, fmt.Errorf("line %d: invalid cpu value %q", lineNumber+1, value)
				}
				cfg.Resources.CPU = cpu
			default:
				return Config{}, fmt.Errorf("line %d: unknown resources key %q", lineNumber+1, key)
			}
		case "health":
			key, value, ok := splitKeyValue(trimmed)
			if indent != 2 || !ok {
				return Config{}, fmt.Errorf("line %d: invalid health entry", lineNumber+1)
			}
			switch key {
			case "path":
				cfg.Health.Path = value
			case "interval":
				if _, err := time.ParseDuration(value); err != nil {
					return Config{}, fmt.Errorf("line %d: invalid health interval %q", lineNumber+1, value)
				}
				cfg.Health.Interval = value
			case "timeout":
				if _, err := time.ParseDuration(value); err != nil {
					return Config{}, fmt.Errorf("line %d: invalid health timeout %q", lineNumber+1, value)
				}
				cfg.Health.Timeout = value
			case "retries":
				retries, err := strconv.Atoi(value)
				if err != nil || retries <= 0 {
					return Config{}, fmt.Errorf("line %d: invalid health retries %q", lineNumber+1, value)
				}
				cfg.Health.Retries = retries
			default:
				return Config{}, fmt.Errorf("line %d: unknown health key %q", lineNumber+1, key)
			}
		case "env":
			if indent != 2 || !strings.HasPrefix(trimmed, "- ") {
				return Config{}, fmt.Errorf("line %d: env entries must be a list", lineNumber+1)
			}
			key := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if key == "" {
				return Config{}, fmt.Errorf("line %d: env key cannot be empty", lineNumber+1)
			}
			cfg.Env = append(cfg.Env, key)
		default:
			return Config{}, fmt.Errorf("line %d: indented entry without section", lineNumber+1)
		}
	}

	if cfg.Name == "" {
		return Config{}, fmt.Errorf("name is required")
	}
	if cfg.Runtime == "" {
		return Config{}, fmt.Errorf("runtime is required")
	}
	if len(cfg.Build.Commands) == 0 {
		return Config{}, fmt.Errorf("build.commands must contain at least one command")
	}
	if cfg.Run.Command == "" {
		return Config{}, fmt.Errorf("run.command is required")
	}
	if cfg.Run.Port == 0 {
		return Config{}, fmt.Errorf("run.port is required")
	}
	if cfg.Resources.MemoryBytes == 0 {
		return Config{}, fmt.Errorf("resources.memory is required")
	}
	if cfg.Resources.CPU == 0 {
		return Config{}, fmt.Errorf("resources.cpu is required")
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

func stripComment(line string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inDouble {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if r == '#' && !inSingle && !inDouble {
			return strings.TrimRight(line[:i], " \t")
		}
	}
	return strings.TrimRight(line, " \t")
}

func countIndent(line string) int {
	count := 0
	for _, r := range line {
		if r != ' ' {
			break
		}
		count++
	}
	return count
}

func splitKeyValue(line string) (key string, value string, ok bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key = strings.TrimSpace(parts[0])
	value = strings.TrimSpace(parts[1])
	return key, unquote(value), key != ""
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
