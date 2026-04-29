import os
import platform
import socket
import time
from datetime import datetime, timezone
from html import escape

from fastapi import FastAPI
from fastapi.responses import HTMLResponse


STARTED_AT = time.time()
STARTED_ISO = datetime.now(timezone.utc).isoformat(timespec="seconds")

app = FastAPI(title="Forge Mission Control")


def dashboard_snapshot():
    uptime_seconds = int(time.time() - STARTED_AT)
    rollout_stage = os.getenv("APP_ENV", "production")
    team_name = os.getenv("TEAM_NAME", "platform")
    region = os.getenv("REGION", "unknown")
    message = os.getenv("APP_MESSAGE", "Release pipeline is green")
    version = os.getenv("APP_VERSION", "v1")
    commit = os.getenv("GIT_COMMIT", "unknown")

    return {
        "app": "mission-control",
        "status": "running",
        "message": message,
        "version": version,
        "commit": commit,
        "team_name": team_name,
        "region": region,
        "rollout_stage": rollout_stage,
        "worker": socket.gethostname(),
        "python": platform.python_version(),
        "started_at": STARTED_ISO,
        "uptime_seconds": uptime_seconds,
        "milestones": [
            {"label": "build", "state": "passed", "detail": "dependencies installed in a venv"},
            {"label": "canary", "state": "healthy", "detail": "health checks stayed green"},
            {"label": "traffic", "state": "shifted", "detail": "requests now go to the new revision"},
            {"label": "signal", "state": "watching", "detail": "deployment metadata and runtime state"},
        ],
        "events": [
            {"time": "T+00s", "title": "deployment accepted", "body": "Forge started the rollout and cloned the repository."},
            {"time": "T+12s", "title": "build complete", "body": "Application dependencies were installed inside the workdir."},
            {"time": "T+24s", "title": "health verified", "body": "The worker confirmed the app responds on the configured port."},
        ],
    }


@app.get("/healthz")
def healthz():
    return {"status": "ok"}


@app.get("/api/dashboard")
def api_dashboard():
    return dashboard_snapshot()


@app.get("/", response_class=HTMLResponse)
def index():
    data = dashboard_snapshot()
    milestones = "".join(
        f'''<li class="milestone"><span>{escape(item["label"])}</span><strong>{escape(item["state"])}</strong><p>{escape(item["detail"])}</p></li>'''
        for item in data["milestones"]
    )
    events = "".join(
        f'''<article class="event"><span>{escape(item["time"])}</span><h3>{escape(item["title"])}</h3><p>{escape(item["body"])}</p></article>'''
        for item in data["events"]
    )

    return f'''<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Forge Mission Control</title>
  <style>
    :root {{
      color-scheme: dark;
      --bg: #08111f;
      --bg2: #12243f;
      --panel: rgba(11, 19, 34, 0.82);
      --panel-border: rgba(154, 198, 255, 0.16);
      --text: #e8eef8;
      --muted: #9bb0cc;
      --accent: #6ee7ff;
      --accent2: #8b5cf6;
      --good: #5eead4;
      --shadow: 0 30px 80px rgba(0, 0, 0, 0.35);
    }}
    * {{ box-sizing: border-box; }}
    body {{
      margin: 0;
      min-height: 100vh;
      font-family: Inter, ui-sans-serif, system-ui, sans-serif;
      color: var(--text);
      background:
        radial-gradient(circle at top left, rgba(110, 231, 255, 0.16), transparent 30%),
        radial-gradient(circle at 80% 20%, rgba(139, 92, 246, 0.18), transparent 26%),
        linear-gradient(160deg, var(--bg), var(--bg2));
    }}
    .shell {{ max-width: 1160px; margin: 0 auto; padding: 48px 20px 56px; }}
    .hero {{
      display: grid;
      gap: 20px;
      grid-template-columns: 1.4fr 0.8fr;
      align-items: start;
      margin-bottom: 24px;
    }}
    .card {{
      background: var(--panel);
      border: 1px solid var(--panel-border);
      border-radius: 24px;
      box-shadow: var(--shadow);
      backdrop-filter: blur(18px);
    }}
    .intro {{ padding: 32px; }}
    .eyebrow {{
      margin: 0 0 14px;
      text-transform: uppercase;
      letter-spacing: 0.18em;
      color: var(--accent);
      font-size: 0.74rem;
      font-weight: 700;
    }}
    h1 {{ margin: 0; font-size: clamp(2.4rem, 6vw, 4.5rem); line-height: 0.95; }}
    .lede {{ margin: 16px 0 0; max-width: 54ch; color: var(--muted); font-size: 1.03rem; line-height: 1.65; }}
    .status {{ padding: 24px; display: grid; gap: 14px; }}
    .status-row {{ display: flex; justify-content: space-between; gap: 12px; align-items: center; }}
    .pill {{
      display: inline-flex; align-items: center; gap: 8px;
      border-radius: 999px; padding: 8px 12px;
      background: rgba(94, 234, 212, 0.1); color: var(--good); font-weight: 700;
    }}
    .dot {{ width: 10px; height: 10px; border-radius: 50%; background: var(--good); box-shadow: 0 0 16px var(--good); }}
    .meta {{ display: grid; gap: 10px; color: var(--muted); font-size: 0.95rem; }}
    .meta div {{ display: flex; justify-content: space-between; gap: 16px; }}
    .grid {{ display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 16px; margin-top: 18px; }}
    .metric {{ padding: 18px; }}
    .metric label {{ display: block; color: var(--muted); font-size: 0.78rem; letter-spacing: 0.12em; text-transform: uppercase; margin-bottom: 8px; }}
    .metric strong {{ font-size: 1.5rem; }}
    .split {{ display: grid; grid-template-columns: 0.9fr 1.1fr; gap: 16px; margin-top: 16px; }}
    .section {{ padding: 24px; }}
    .section h2 {{ margin: 0 0 18px; font-size: 1.2rem; }}
    .milestones {{ list-style: none; margin: 0; padding: 0; display: grid; gap: 12px; }}
    .milestone {{
      padding: 14px 16px; border-radius: 18px; background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.06);
    }}
    .milestone span {{ color: var(--accent); text-transform: uppercase; letter-spacing: 0.12em; font-size: 0.75rem; }}
    .milestone strong {{ display: block; margin: 8px 0 4px; font-size: 1rem; }}
    .milestone p, .event p {{ margin: 0; color: var(--muted); line-height: 1.6; }}
    .feed {{ display: grid; gap: 12px; }}
    .event {{ padding: 16px 18px; border-radius: 18px; background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.06); }}
    .event span {{ color: var(--accent2); text-transform: uppercase; font-size: 0.75rem; letter-spacing: 0.12em; }}
    .event h3 {{ margin: 8px 0 6px; font-size: 1rem; }}
    @media (max-width: 900px) {{
      .hero, .split {{ grid-template-columns: 1fr; }}
      .grid {{ grid-template-columns: repeat(2, minmax(0, 1fr)); }}
    }}
    @media (max-width: 640px) {{
      .shell {{ padding: 24px 14px 40px; }}
      .intro, .status, .section {{ padding: 20px; }}
      .grid {{ grid-template-columns: 1fr; }}
    }}
  </style>
</head>
<body>
  <main class="shell">
    <section class="hero">
      <article class="card intro">
        <p class="eyebrow">Forge showcase</p>
        <h1>Mission Control</h1>
        <p class="lede">A richer demo app for rolling out release metadata, runtime state, and a compact event trail. It is intentionally a little more alive than a plain status page.</p>
      </article>
      <aside class="card status">
        <div class="status-row">
          <span class="pill"><span class="dot"></span>{escape(data["status"])}</span>
          <span>{escape(data["rollout_stage"])} rollout</span>
        </div>
        <div class="meta">
          <div><span>Team</span><strong>{escape(data["team_name"])}</strong></div>
          <div><span>Region</span><strong>{escape(data["region"])}</strong></div>
          <div><span>Worker</span><strong>{escape(data["worker"])}</strong></div>
          <div><span>Uptime</span><strong>{data["uptime_seconds"]}s</strong></div>
        </div>
      </aside>
    </section>

    <section class="grid">
      <article class="card metric"><label>Version</label><strong>{escape(data["version"])}</strong></article>
      <article class="card metric"><label>Commit</label><strong>{escape(data["commit"])}</strong></article>
      <article class="card metric"><label>Python</label><strong>{escape(data["python"])}</strong></article>
      <article class="card metric"><label>Started</label><strong>{escape(data["started_at"])}</strong></article>
    </section>

    <section class="split">
      <article class="card section">
        <h2>Rollout milestones</h2>
        <ul class="milestones">{milestones}</ul>
      </article>
      <article class="card section">
        <h2>Live event feed</h2>
        <div class="feed">{events}</div>
      </article>
    </section>
  </main>
</body>
</html>'''