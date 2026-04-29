#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
CONTROL_PID=""
AGENT_PID=""

cleanup() {
  if [[ -n "${AGENT_PID}" ]]; then kill "${AGENT_PID}" 2>/dev/null || true; fi
  if [[ -n "${CONTROL_PID}" ]]; then kill "${CONTROL_PID}" 2>/dev/null || true; fi
  pkill -f "${TMP_DIR}/agent-apps" 2>/dev/null || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

cd "${ROOT_DIR}"
make build >/dev/null

APP_REPO="${TMP_DIR}/app"
mkdir -p "${APP_REPO}"
cat > "${APP_REPO}/forge.yaml" <<EOF_APP
name: smokeapp
runtime: python3
build:
  commands:
    - python3 --version
run:
  command: python3 app.py
  port: 8000
resources:
  memory: 128M
  cpu: 0.2
health:
  path: /health
  interval: 1s
  timeout: 1s
  retries: 10
EOF_APP

cat > "${APP_REPO}/app.py" <<'PY'
import os
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"hello from forge")

    def log_message(self, fmt, *args):
        return

port = int(os.environ["PORT"])
HTTPServer(("0.0.0.0", port), Handler).serve_forever()
PY

git -C "${APP_REPO}" init -q
git -C "${APP_REPO}" checkout -q -b main
git -C "${APP_REPO}" add forge.yaml app.py
git -C "${APP_REPO}" -c user.name=Forge -c user.email=forge@example.test commit -q -m "initial smoke app"
COMMIT_SHA="$(git -C "${APP_REPO}" rev-parse HEAD)"

MASTER_KEY="$(openssl rand -base64 32)"
AGENT_TOKEN="$(openssl rand -hex 32)"
ADMIN_TOKEN="$(openssl rand -hex 32)"
WEBHOOK_SECRET="$(openssl rand -hex 32)"
CONTROL_URL="http://127.0.0.1:18080"

FORGE_ADDR=127.0.0.1:18080 \
FORGE_DB_PATH="${TMP_DIR}/forge.db" \
FORGE_WORK_DIR="${TMP_DIR}/work" \
FORGE_AGENT_APP_ROOT="${TMP_DIR}/agent-apps" \
FORGE_BASE_DOMAIN=forge.localhost \
FORGE_MASTER_KEY="${MASTER_KEY}" \
FORGE_AGENT_TOKEN="${AGENT_TOKEN}" \
FORGE_ADMIN_TOKEN="${ADMIN_TOKEN}" \
FORGE_GITHUB_WEBHOOK_SECRET="${WEBHOOK_SECRET}" \
FORGE_ALLOWED_REPOS=local/smokeapp \
FORGE_ALLOWED_BRANCHES=main \
FORGE_ALLOW_LOCAL_REPOS=true \
"${ROOT_DIR}/bin/forge-control-plane" >"${TMP_DIR}/control-plane.log" 2>&1 &
CONTROL_PID="$!"

CONTROL_READY=0
for _ in $(seq 1 40); do
  if curl -fsS "${CONTROL_URL}/healthz" >/dev/null 2>&1; then
    CONTROL_READY=1
    break
  fi
  sleep 0.25
done
if [[ "${CONTROL_READY}" -ne 1 ]]; then
  echo "control plane did not become healthy" >&2
  cat "${TMP_DIR}/control-plane.log" >&2
  exit 1
fi

FORGE_CONTROL_PLANE_URL="${CONTROL_URL}" \
FORGE_RUNNER_PATH="${ROOT_DIR}/bin/forge-build-runner" \
FORGE_METRICS_SOCKET="${TMP_DIR}/agent-metrics.sock" \
FORGE_AGENT_TOKEN="${AGENT_TOKEN}" \
FORGE_AGENT_ID=local-agent \
FORGE_AGENT_ADDRESS=127.0.0.1 \
FORGE_AGENT_POLL_SECONDS=1 \
"${ROOT_DIR}/bin/forge-agent" >"${TMP_DIR}/agent.log" 2>&1 &
AGENT_PID="$!"

sleep 1

PAYLOAD="${TMP_DIR}/payload.json"
cat > "${PAYLOAD}" <<EOF_PAYLOAD
{
  "ref": "refs/heads/main",
  "after": "${COMMIT_SHA}",
  "repository": {
    "clone_url": "${APP_REPO}",
    "html_url": "${APP_REPO}",
    "url": "${APP_REPO}",
    "full_name": "local/smokeapp"
  }
}
EOF_PAYLOAD

SIGNATURE="$(
  WEBHOOK_SECRET="${WEBHOOK_SECRET}" PAYLOAD="${PAYLOAD}" python3 - <<'PY'
import hashlib
import hmac
import os

secret = os.environ["WEBHOOK_SECRET"].encode()
with open(os.environ["PAYLOAD"], "rb") as fh:
    body = fh.read()
print(hmac.new(secret, body, hashlib.sha256).hexdigest())
PY
)"

curl -fsS \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: push" \
  -H "X-Hub-Signature-256: sha256=${SIGNATURE}" \
  --data-binary @"${PAYLOAD}" \
  "${CONTROL_URL}/api/v1/webhook/github" >/dev/null

for _ in $(seq 1 80); do
  DEPLOYMENTS="$(curl -fsS -H "Authorization: Bearer ${ADMIN_TOKEN}" "${CONTROL_URL}/api/v1/deployments")"
  if grep -q '"status":"running"' <<<"${DEPLOYMENTS}"; then
    APP_PORT="$(DEPLOYMENTS="${DEPLOYMENTS}" python3 - <<'PY'
import json
import os

deployments = json.loads(os.environ["DEPLOYMENTS"])
for deployment in deployments:
    if deployment.get("status") == "running":
        print(deployment["target_port"])
        break
PY
)"
    curl -fsS "http://127.0.0.1:${APP_PORT}/health" >/dev/null
    echo "local e2e passed: deployment reached running and app health responded on ${APP_PORT}"
    exit 0
  fi
  if grep -q '"status":"failed"' <<<"${DEPLOYMENTS}"; then
    echo "deployment failed" >&2
    echo "${DEPLOYMENTS}" >&2
    echo "--- control-plane log ---" >&2
    cat "${TMP_DIR}/control-plane.log" >&2
    echo "--- agent log ---" >&2
    cat "${TMP_DIR}/agent.log" >&2
    exit 1
  fi
  sleep 1
done

echo "deployment did not reach running in time" >&2
echo "--- control-plane log ---" >&2
cat "${TMP_DIR}/control-plane.log" >&2
echo "--- agent log ---" >&2
cat "${TMP_DIR}/agent.log" >&2
exit 1
