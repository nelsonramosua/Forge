import os
import platform
import socket
import time
from datetime import datetime, timezone

from fastapi import FastAPI
from fastapi.responses import HTMLResponse
from fastapi.staticfiles import StaticFiles


STARTED_AT = time.time()
STARTED_ISO = datetime.now(timezone.utc).isoformat(timespec="seconds")

app = FastAPI(title="Forge Release Board")
app.mount("/static", StaticFiles(directory="static"), name="static")


def status_payload():
    uptime_seconds = int(time.time() - STARTED_AT)
    return {
        "app": "release-board",
        "status": "running",
        "message": os.getenv("APP_MESSAGE", "Deployed by Forge"),
        "environment": os.getenv("APP_ENV", "production"),
        "version": os.getenv("APP_VERSION", "v1"),
        "commit": os.getenv("GIT_COMMIT", "unknown"),
        "worker": socket.gethostname(),
        "python": platform.python_version(),
        "started_at": STARTED_ISO,
        "uptime_seconds": uptime_seconds,
    }


@app.get("/health")
def health():
    return {"status": "ok"}


@app.get("/api/status")
def api_status():
    return status_payload()


@app.get("/", response_class=HTMLResponse)
def index():
    status = status_payload()
    return f"""<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Forge Release Board</title>
  <link rel="stylesheet" href="/static/style.css">
</head>
<body>
  <main class="shell">
    <section class="panel">
      <p class="eyebrow">Forge deployment</p>
      <h1>Release Board</h1>
      <p class="message">{escape(status["message"])}</p>
      <dl class="grid">
        <div>
          <dt>Status</dt>
          <dd><span class="dot"></span>{escape(status["status"])}</dd>
        </div>
        <div>
          <dt>Environment</dt>
          <dd>{escape(status["environment"])}</dd>
        </div>
        <div>
          <dt>Version</dt>
          <dd>{escape(status["version"])}</dd>
        </div>
        <div>
          <dt>Commit</dt>
          <dd>{escape(status["commit"])}</dd>
        </div>
        <div>
          <dt>Worker</dt>
          <dd>{escape(status["worker"])}</dd>
        </div>
        <div>
          <dt>Uptime</dt>
          <dd>{status["uptime_seconds"]}s</dd>
        </div>
      </dl>
      <a class="link" href="/api/status">/api/status</a>
    </section>
  </main>
</body>
</html>"""


def escape(value):
    return (
        str(value)
        .replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
    )
