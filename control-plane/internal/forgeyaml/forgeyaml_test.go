package forgeyaml

import "testing"

func TestParseForgeYAML(t *testing.T) {
	input := []byte(`name: myapp
runtime: python3.11
build:
  commands:
    - pip install -r requirements.txt
    - python manage.py collectstatic --noinput
run:
  command: uvicorn app:main --host 0.0.0.0 --port $PORT
  port: 8000
resources:
  memory: 256M
  cpu: 0.5
health:
  path: /health
  interval: 10s
  timeout: 3s
  retries: 3
env:
  - DATABASE_URL
  - SECRET_KEY
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "myapp" || cfg.Run.Port != 8000 || cfg.Resources.MemoryBytes != 256*1024*1024 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if len(cfg.Build.Commands) != 2 || len(cfg.Env) != 2 {
		t.Fatalf("unexpected lists: %+v", cfg)
	}
}

func TestParseForgeYAMLRejectsUnknownFields(t *testing.T) {
	input := []byte(`name: myapp
runtime: python3.11
surprise: true
build:
  commands:
    - echo ok
run:
  command: python app.py
  port: 8000
resources:
  memory: 128M
  cpu: 0.2
`)
	if _, err := Parse(input); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestParseForgeYAMLSupportsQuotedColons(t *testing.T) {
	input := []byte(`name: myapp
runtime: python3.11
build:
  commands:
    - "python -c 'print(\"a:b\")'"
run:
  command: "python -m http.server --bind 0.0.0.0 $PORT"
  port: 8000
resources:
  memory: "128M"
  cpu: 0.2
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Build.Commands[0] != `python -c 'print("a:b")'` {
		t.Fatalf("unexpected command %q", cfg.Build.Commands[0])
	}
}

func TestParseForgeYAMLRejectsInvalidHealthPath(t *testing.T) {
	input := []byte(`name: myapp
runtime: python3.11
build:
  commands:
    - echo ok
run:
  command: python app.py
  port: 8000
resources:
  memory: 128M
  cpu: 0.2
health:
  path: health
`)
	if _, err := Parse(input); err == nil {
		t.Fatal("expected invalid health path to be rejected")
	}
}
