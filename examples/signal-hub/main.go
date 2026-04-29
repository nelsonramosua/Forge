package main

import (
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"time"
)

var startedAt = time.Now()

type service struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

type snapshot struct {
	App        string    `json:"app"`
	Status     string    `json:"status"`
	Title      string    `json:"title"`
	Region     string    `json:"region"`
	Commit     string    `json:"commit"`
	Worker     string    `json:"worker"`
	GoVersion  string    `json:"go_version"`
	StartedAt  string    `json:"started_at"`
	Uptime     int64     `json:"uptime_seconds"`
	Services   []service `json:"services"`
	Highlights []string  `json:"highlights"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/api/snapshot", apiSnapshot)
	mux.HandleFunc("/", index)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout:  5 * time.Second,
		IdleTimeout:        30 * time.Second,
		WriteTimeout:       10 * time.Second,
		MaxHeaderBytes:     1 << 20,
	}

	_ = server.ListenAndServe()
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func apiSnapshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(buildSnapshot())
}

func index(w http.ResponseWriter, r *http.Request) {
	snap := buildSnapshot()
	_ = pageTemplate.Execute(w, snap)
}

func buildSnapshot() snapshot {
	services := []service{
		{Name: "webhook", Status: "healthy", Note: "receiving deployment events"},
		{Name: "scheduler", Status: "healthy", Note: "queue and task dispatch are active"},
		{Name: "runtime", Status: "warming", Note: "application dependencies live inside the workdir"},
	}

	started := startedAt.UTC().Format(time.RFC3339)
	highlights := []string{
		"Go example with no third-party dependencies",
		"Builds to a single binary for deployment",
		"Shows status, API, and uptime data from the runtime",
	}
	sort.Strings(highlights)

	return snapshot{
		App:        "signal-hub",
		Status:     "running",
		Title:      envOr("HUB_TITLE", "Signal Hub"),
		Region:     envOr("HUB_REGION", "unknown"),
		Commit:     envOr("GIT_COMMIT", "unknown"),
		Worker:     hostname(),
		GoVersion:  runtime.Version(),
		StartedAt:  started,
		Uptime:     int64(time.Since(startedAt).Seconds()),
		Services:   services,
		Highlights: highlights,
	}
}

func hostname() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

func envOr(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

var pageTemplate = template.Must(template.New("page").Funcs(template.FuncMap{
	"lower": func(value string) string { return value },
	"formatDuration": func(seconds int64) string {
		return strconv.FormatInt(seconds, 10) + "s"
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{ .Title }}</title>
  <style>
    :root {
      color-scheme: dark;
      --bg: #071018;
      --bg2: #0d1e2e;
      --panel: rgba(8, 15, 24, 0.8);
      --border: rgba(148, 163, 184, 0.16);
      --text: #e5eef9;
      --muted: #97aac2;
      --accent: #34d399;
      --accent2: #f59e0b;
      --shadow: 0 30px 80px rgba(0, 0, 0, 0.35);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: Inter, ui-sans-serif, system-ui, sans-serif;
      color: var(--text);
      background:
        radial-gradient(circle at top right, rgba(52, 211, 153, 0.16), transparent 28%),
        radial-gradient(circle at 15% 20%, rgba(245, 158, 11, 0.14), transparent 28%),
        linear-gradient(160deg, var(--bg), var(--bg2));
    }
    .wrap { max-width: 1120px; margin: 0 auto; padding: 44px 20px 56px; }
    .hero {
      display: grid;
      grid-template-columns: 1.2fr 0.8fr;
      gap: 18px;
      margin-bottom: 18px;
    }
    .panel {
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 24px;
      box-shadow: var(--shadow);
      backdrop-filter: blur(18px);
      padding: 28px;
    }
    .eyebrow {
      margin: 0 0 12px;
      text-transform: uppercase;
      letter-spacing: 0.16em;
      color: var(--accent);
      font-size: 0.75rem;
      font-weight: 700;
    }
    h1 { margin: 0; font-size: clamp(2.4rem, 7vw, 4.4rem); line-height: 0.95; }
    .lede { margin: 14px 0 0; color: var(--muted); line-height: 1.65; max-width: 58ch; }
    .stat-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; margin-top: 16px; }
    .stat { padding: 16px; background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.06); border-radius: 18px; }
    .stat span { display: block; color: var(--muted); font-size: 0.76rem; text-transform: uppercase; letter-spacing: 0.12em; margin-bottom: 8px; }
    .stat strong { font-size: 1.25rem; }
    .badge { display: inline-flex; align-items: center; gap: 8px; padding: 8px 12px; border-radius: 999px; background: rgba(52, 211, 153, 0.11); color: var(--accent); font-weight: 700; }
    .dot { width: 10px; height: 10px; border-radius: 50%; background: var(--accent); box-shadow: 0 0 16px var(--accent); }
    .cols { display: grid; grid-template-columns: 0.9fr 1.1fr; gap: 18px; margin-top: 18px; }
    .list { display: grid; gap: 12px; }
    .item { padding: 14px 16px; border-radius: 16px; background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.06); }
    .item strong { display: block; margin-bottom: 6px; }
    .item p { margin: 0; color: var(--muted); line-height: 1.5; }
    .service { display: flex; justify-content: space-between; gap: 16px; align-items: start; }
    .service small { display: block; color: var(--muted); margin-top: 4px; }
    .service span { text-transform: uppercase; font-size: 0.75rem; letter-spacing: 0.12em; color: var(--accent2); }
    a { color: inherit; }
    @media (max-width: 900px) {
      .hero, .cols, .stat-grid { grid-template-columns: 1fr; }
    }
    @media (max-width: 640px) {
      .wrap { padding: 24px 14px 40px; }
      .panel { padding: 20px; }
    }
  </style>
</head>
<body>
  <main class="wrap">
    <section class="hero">
      <article class="panel">
        <p class="eyebrow">Forge showcase in Go</p>
        <h1>{{ .Title }}</h1>
        <p class="lede">A single-binary deployment that demonstrates Forge outside Python. It exposes a live health endpoint, a JSON snapshot API, and a compact operations dashboard.</p>
      </article>
      <aside class="panel">
        <div class="badge"><span class="dot"></span>{{ .Status }}</div>
        <div class="stat-grid">
          <div class="stat"><span>Region</span><strong>{{ .Region }}</strong></div>
          <div class="stat"><span>Commit</span><strong>{{ .Commit }}</strong></div>
          <div class="stat"><span>Worker</span><strong>{{ .Worker }}</strong></div>
          <div class="stat"><span>Uptime</span><strong>{{ formatDuration .Uptime }}</strong></div>
        </div>
      </aside>
    </section>

    <section class="cols">
      <article class="panel">
        <h2>Services</h2>
        <div class="list">
          {{ range .Services }}
          <div class="item service">
            <div>
              <strong>{{ .Name }}</strong>
              <small>{{ .Note }}</small>
            </div>
            <span>{{ .Status }}</span>
          </div>
          {{ end }}
        </div>
      </article>
      <article class="panel">
        <h2>Highlights</h2>
        <div class="list">
          {{ range .Highlights }}
          <div class="item"><p>{{ . }}</p></div>
          {{ end }}
        </div>
        <div class="item" style="margin-top: 12px;">
          <strong>API</strong>
          <p><a href="/api/snapshot">/api/snapshot</a> and <a href="/healthz">/healthz</a></p>
        </div>
      </article>
    </section>
  </main>
</body>
</html>`))