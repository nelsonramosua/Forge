import http.server
import json
import os
import secrets
import time
import urllib.error
import urllib.request
from http.cookies import SimpleCookie

ADMIN_TOKEN = os.environ["FORGE_ADMIN_TOKEN"]
CONSOLE_PASSWORD = os.environ["FORGE_ADMIN_CONSOLE_PASSWORD"]
CP_URL = os.environ["FORGE_CONTROL_PLANE_URL"].rstrip("/")
PORT = int(os.environ.get("PORT", 8000))
SESSION_TTL_SECONDS = int(os.environ.get("FORGE_ADMIN_SESSION_TTL_SECONDS", 3600))
STATIC_DIR = os.path.join(os.path.dirname(__file__), "static")
SESSIONS: dict[str, float] = {}

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
            self._send_json(401, {"error": "invalid credentials"})
            return
        session_id = secrets.token_hex(32)
        SESSIONS[session_id] = time.time() + SESSION_TTL_SECONDS
        data = json.dumps({"status": "ok"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.send_header(
            "Set-Cookie",
            "forge_session={}; HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age={}".format(
                session_id,
                SESSION_TTL_SECONDS,
            ),
        )
        self.end_headers()
        self.wfile.write(data)

    def _handle_logout(self):
        cookie = self._get_session_cookie()
        if cookie:
            SESSIONS.pop(cookie, None)
        self._send_json(200, {"status": "ok"})

    def _check_session(self) -> bool:
        cookie = self._get_session_cookie()
        expires_at = SESSIONS.get(cookie or "")
        if not expires_at:
            return False
        if expires_at < time.time():
            SESSIONS.pop(cookie, None)
            return False
        return True

    def _get_session_cookie(self) -> str | None:
        raw = self.headers.get("Cookie", "")
        cookies = SimpleCookie()
        cookies.load(raw)
        morsel = cookies.get("forge_session")
        return morsel.value if morsel else None

    def _proxy_api(self):
        url = CP_URL + self.path
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length) if length > 0 else None
        request = urllib.request.Request(
            url,
            data=body,
            method=self.command,
            headers={
                "Authorization": "Bearer {}".format(ADMIN_TOKEN),
                "Content-Type": self.headers.get("Content-Type", "application/json"),
            },
        )
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


if __name__ == "__main__":
    with http.server.ThreadingHTTPServer(("0.0.0.0", PORT), Handler) as httpd:
        print("forge-admin listening on :{}".format(PORT))
        httpd.serve_forever()
