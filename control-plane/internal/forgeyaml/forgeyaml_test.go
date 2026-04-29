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
