#!/usr/bin/env python3
"""Stub of the 139 personal-cloud API used to prove the on-demand credential
recovery end to end against the *real* daemon binary.

Behaviour
---------
* Every API call with the "old" credential is rejected with the same
  business-level auth error the service really returns
  (``code 10001 / 登录状态已失效，请重新登录``).
* ``/refresh`` hands out a new ticket (the AAS endpoint the browser client
  uses), which the client is expected to persist and retry with.
* Calls carrying the new ticket succeed.

Usage:  fake139.py <port> <old-ticket> <new-ticket> [logfile]
"""
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1])
OLD = sys.argv[2]        # base64("pc:<acct>:<token>")
NEW = sys.argv[3]        # base64("pc:<acct>:<token>") handed out by /refresh
LOGF = sys.argv[4] if len(sys.argv) > 4 else "/tmp/fake139.log"

lock = threading.Lock()
counts = {"old_rejected": 0, "new_ok": 0, "refresh": 0, "other": 0}


def log(line):
    with lock:
        with open(LOGF, "a") as fh:
            fh.write(line + "\n")
        sys.stdout.write(line + "\n")
        sys.stdout.flush()


def token_part(auth_b64):
    import base64
    try:
        raw = base64.b64decode(auth_b64).decode()
    except Exception:
        return auth_b64
    return raw.split(":", 2)[-1]


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):  # keep the console clean; we log ourselves
        pass

    def _send(self, obj, status=200):
        body = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _auth(self):
        return self.headers.get("Authorization", "").replace("Basic ", "", 1)

    def do_GET(self):
        self._handle()

    def do_POST(self):
        self._handle()

    def _handle(self):
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n) if n else b""

        if self.path.startswith("/refresh"):
            # AAS authTokenRefresh.do — the request carries the ticket, not the
            # Authorization header.
            with lock:
                counts["refresh"] += 1
            got = OLD
            try:
                got = raw.decode().split("<token>")[1].split("</token>")[0]
            except Exception:
                pass
            log("REFRESH  ticket=%s…  -> 发新令牌" % got[:24])
            body = ("<root><return>0</return><desc></desc><token>%s</token></root>"
                    % token_part(NEW)).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/xml; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        auth = self._auth()
        if auth != NEW:
            with lock:
                counts["old_rejected"] += 1
            log("REJECT   %-42s cred=%s…  (code=10001 登录状态已失效)"
                % (self.path, auth[:20] or "<empty>"))
            self._send({"success": False, "code": "10001",
                        "message": "登录状态已失效，请重新登录"})
            return

        with lock:
            counts["new_ok"] += 1
        log("OK       %-42s cred=新令牌" % self.path)

        if self.path.endswith("/user/route/qryRoutePolicy"):
            self._send({"success": True, "code": "0", "message": "成功",
                        "data": {"routePolicyList": [
                            {"modName": "personal", "httpsUrl": "http://127.0.0.1:%d" % PORT}]}})
            return
        if self.path.endswith("/file/list"):
            self._send({"success": True, "code": "0", "message": "成功", "data": {
                "items": [
                    {"fileId": "dir-1", "name": "来自云端", "type": "folder",
                     "size": 0, "createdAt": "2026-10-01T10:00:00", "updatedAt": "2026-10-01T10:00:00"},
                    {"fileId": "file-1", "name": "鉴权恢复.txt", "type": "file",
                     "size": 12, "createdAt": "2026-10-01T10:00:00", "updatedAt": "2026-10-01T10:00:00"},
                ],
                "nextPageCursor": ""}})
            return
        # Anything else: a permissive empty success, so the mount can proceed.
        self._send({"success": True, "code": "0", "message": "成功", "data": {}})

    def do_PUT(self):
        self._handle()


if __name__ == "__main__":
    open(LOGF, "w").close()
    log("stub 139 已启动: http://127.0.0.1:%d  (旧令牌会被拒，/refresh 发新令牌)" % PORT)
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
