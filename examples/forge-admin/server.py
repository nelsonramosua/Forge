import base64
import binascii
import hashlib
import hmac
import http.server
import json
import os
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request
from http.cookies import SimpleCookie
from ipaddress import ip_address

def control_plane_url() -> str:
    explicit_url = os.environ.get("FORGE_CONTROL_PLANE_URL", "").strip()
    if explicit_url:
        return explicit_url.rstrip("/")
    private_ip = os.environ.get("FORGE_CONTROL_PLANE_PRIVATE_IP", "").strip()
    if private_ip:
        return "http://{}:8080".format(private_ip)
    raise RuntimeError("set FORGE_CONTROL_PLANE_URL or FORGE_CONTROL_PLANE_PRIVATE_IP")


ADMIN_TOKEN = os.environ["FORGE_ADMIN_TOKEN"]
CONSOLE_PASSWORD = os.environ["FORGE_ADMIN_CONSOLE_PASSWORD"]
CP_URL = control_plane_url()
PORT = int(os.environ.get("PORT", 8000))
SESSION_TTL_SECONDS = int(os.environ.get("FORGE_ADMIN_SESSION_TTL_SECONDS", 3600))
SESSION_SIGNING_KEY = os.environ.get("FORGE_ADMIN_SESSION_KEY", ADMIN_TOKEN + ":" + CONSOLE_PASSWORD).encode()
STATIC_DIR = os.path.join(os.path.dirname(__file__), "static")
FAILED_LOGINS: dict[str, dict[str, object]] = {}
LOGIN_FAIL_LIMIT = 5
LOGIN_FAIL_WINDOW_SECONDS = 60
LOGIN_BLOCK_SECONDS = 900
MAX_PROXY_BODY_BYTES = 1 << 20
TRUSTED_PROXY_IPS = set()

for raw_ip in ("127.0.0.1", "::1", os.environ.get("FORGE_CONTROL_PLANE_PRIVATE_IP", "")):
    raw_ip = raw_ip.strip()
    if raw_ip:
        try:
            TRUSTED_PROXY_IPS.add(ip_address(raw_ip))
        except ValueError:
            pass

for raw_ip in os.environ.get("FORGE_TRUSTED_PROXY_IPS", "").split(","):
    raw_ip = raw_ip.strip()
    if raw_ip:
        try:
            TRUSTED_PROXY_IPS.add(ip_address(raw_ip))
        except ValueError:
            pass

try:
    cp_host = urllib.parse.urlparse(CP_URL).hostname or ""
    if cp_host:
        TRUSTED_PROXY_IPS.add(ip_address(cp_host))
except ValueError:
    pass

class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=STATIC_DIR, **kwargs)

    def do_GET(self):
        if self.path == "/health":
            self._send_json(200, {"status": "ok"})
            return
        if self.path.startswith("/api/"):
            if not self._check_session():
                self._send_json(401, {"error": "not authenticated"})
                return
            self._proxy_api()
            return
        super().do_GET()

    def do_POST(self):
        if self.path == "/login":
            self._handle_login()
            return
        if self.path == "/logout":
            self._handle_logout()
            return
        if self.path.startswith("/api/"):
            if not self._check_session():
                self._send_json(401, {"error": "not authenticated"})
                return
            self._proxy_api()
            return
        self._send_json(404, {"error": "not found"})

    def do_PUT(self):
        self._authenticated_proxy()

    def do_DELETE(self):
        self._authenticated_proxy()

    def _authenticated_proxy(self):
        if self.path.startswith("/api/") and self._check_session():
            self._proxy_api()
            return
        self._send_json(401, {"error": "not authenticated"})

    def _handle_login(self):
        client = self._client_ip()
        if self._login_blocked(client):
            self._send_json(429, {"error": "too many failed login attempts"})
            return
        length = int(self.headers.get("Content-Length", 0))
        try:
            body = json.loads(self.rfile.read(length) or b"{}")
        except json.JSONDecodeError:
            self._send_json(400, {"error": "invalid json"})
            return
        provided = str(body.get("password", ""))
        expected = CONSOLE_PASSWORD.encode()
        provided_bytes = provided.encode()
        if len(provided_bytes) != len(expected) or not secrets.compare_digest(provided_bytes, expected):
            self._record_login_failure(client)
            self._send_json(401, {"error": "invalid credentials"})
            return
        FAILED_LOGINS.pop(client, None)
        session_cookie = self._make_session_cookie()
        data = json.dumps({"status": "ok"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.send_header(
            "Set-Cookie",
            "forge_session={}; HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age={}".format(
                session_cookie,
                SESSION_TTL_SECONDS,
            ),
        )
        self.end_headers()
        self.wfile.write(data)

    def _handle_logout(self):
        data = json.dumps({"status": "ok"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("Set-Cookie", "forge_session=; HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age=0")
        self.end_headers()
        self.wfile.write(data)

    def _check_session(self) -> bool:
        cookie = self._get_session_cookie()
        if not cookie:
            return False
        try:
            payload_token, signature = cookie.rsplit(".", 1)
        except ValueError:
            return False
        expected = hmac.new(SESSION_SIGNING_KEY, payload_token.encode(), hashlib.sha256).hexdigest()
        if not hmac.compare_digest(signature, expected):
            return False
        try:
            payload = json.loads(_b64decode(payload_token))
        except (ValueError, json.JSONDecodeError, binascii.Error):
            return False
        try:
            expires_at = int(payload.get("exp", 0))
        except (TypeError, ValueError):
            return False
        return expires_at >= int(time.time())

    def _make_session_cookie(self) -> str:
        payload = json.dumps(
            {"exp": int(time.time()) + SESSION_TTL_SECONDS, "nonce": secrets.token_hex(16)},
            separators=(",", ":"),
        ).encode()
        payload_token = _b64encode(payload)
        signature = hmac.new(SESSION_SIGNING_KEY, payload_token.encode(), hashlib.sha256).hexdigest()
        return "{}.{}".format(payload_token, signature)

    def _get_session_cookie(self) -> str | None:
        raw = self.headers.get("Cookie", "")
        cookies = SimpleCookie()
        cookies.load(raw)
        morsel = cookies.get("forge_session")
        return morsel.value if morsel else None

    def _client_ip(self) -> str:
        remote = self.client_address[0] if self.client_address else ""
        try:
            remote_ip = ip_address(remote)
        except ValueError:
            return remote or "unknown"
        if remote_ip in TRUSTED_PROXY_IPS:
            forwarded_for = self.headers.get("X-Forwarded-For", "")
            first = forwarded_for.split(",", 1)[0].strip()
            if first:
                try:
                    return str(ip_address(first))
                except ValueError:
                    pass
        return str(remote_ip)

    def _login_blocked(self, client: str) -> bool:
        entry = FAILED_LOGINS.get(client)
        if not entry:
            return False
        blocked_until = float(entry.get("blocked_until", 0))
        if blocked_until > time.time():
            return True
        if blocked_until:
            FAILED_LOGINS.pop(client, None)
        return False

    def _record_login_failure(self, client: str):
        now = time.time()
        entry = FAILED_LOGINS.setdefault(client, {"attempts": [], "blocked_until": 0})
        attempts = [ts for ts in entry.get("attempts", []) if now - float(ts) <= LOGIN_FAIL_WINDOW_SECONDS]
        attempts.append(now)
        entry["attempts"] = attempts
        if len(attempts) >= LOGIN_FAIL_LIMIT:
            entry["blocked_until"] = now + LOGIN_BLOCK_SECONDS

    def _proxy_api(self):
        url = CP_URL + self.path
        length = int(self.headers.get("Content-Length", 0))
        if length > MAX_PROXY_BODY_BYTES:
            self._send_json(413, {"error": "request too large"})
            return
        body = self.rfile.read(length) if length > 0 else None
        headers = {"Authorization": "Bearer {}".format(ADMIN_TOKEN)}
        content_type = self.headers.get("Content-Type")
        if content_type:
            headers["Content-Type"] = content_type
        request = urllib.request.Request(
            url,
            data=body,
            method=self.command,
            headers=headers,
        )
        if self.command == "GET" and self.path.startswith("/api/v1/events"):
            self._proxy_stream(request)
            return
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                data = response.read()
                self.send_response(response.status)
                self.send_header("Content-Type", response.headers.get("Content-Type", "application/json"))
                self.send_header("Content-Length", str(len(data)))
                self.send_header("Cache-Control", "no-store")
                self.end_headers()
                self.wfile.write(data)
        except urllib.error.HTTPError as err:
            data = err.read()
            self.send_response(err.code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(data)
        except urllib.error.URLError:
            self._send_json(502, {"error": "control plane unavailable"})

    def _proxy_stream(self, request):
        try:
            with urllib.request.urlopen(request, timeout=60) as response:
                self.send_response(response.status)
                self.send_header("Content-Type", response.headers.get("Content-Type", "text/event-stream"))
                self.send_header("Cache-Control", "no-cache")
                self.end_headers()
                while True:
                    data = response.read(4096)
                    if not data:
                        break
                    self.wfile.write(data)
                    self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            return
        except urllib.error.URLError:
            return

    def _send_json(self, status: int, body: dict):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):
        pass


def _b64encode(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).decode().rstrip("=")


def _b64decode(value: str) -> bytes:
    return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))


if __name__ == "__main__":
    with http.server.ThreadingHTTPServer(("0.0.0.0", PORT), Handler) as httpd:
        print("forge-admin listening on :{}".format(PORT))
        httpd.serve_forever()
