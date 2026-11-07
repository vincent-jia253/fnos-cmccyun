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

# A tiny cloud tree: 根 → 影视 → 电影 → 测试片.mkv / 海报.jpg.
# /file/list returns the children of parentFileId; /file/getDownloadUrl returns a
# fake CDN link.  Keeping the tree here lets tests/strm-e2e.sh drive the
# real daemon's STRM generator end to end (list → resolve → scan → play 302).
TREE = {
    "/": [
        ("dir-1", "来自云端", "folder", 0, "2026-10-01T10:00:00"),
        ("file-1", "鉴权恢复.txt", "file", 12, "2026-10-01T10:00:00"),
        ("dir-v", "影视", "folder", 0, "2026-10-01T10:00:00"),
    ],
    "dir-v": [
        ("dir-v2", "电影", "folder", 0, "2026-10-01T10:00:00"),
    ],
    "dir-v2": [
        ("file-v", "测试片.mkv", "file", 8 << 30, "2026-10-01T10:00:00"),
        ("file-p", "海报.jpg", "file", 1 << 20, "2026-10-01T10:00:00"),
    ],
}

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
        try:
            payload = json.loads(raw or b"{}")
        except Exception:
            payload = {}
        if not isinstance(payload, dict):
            payload = {}

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
            parent = str(payload.get("parentFileId") or "/")
            items = []
            for fid, name, typ, size, ts in TREE.get(parent, []):
                items.append({"fileId": fid, "name": name, "type": typ,
                              "size": size, "createdAt": ts, "updatedAt": ts})
            self._send({"success": True, "code": "0", "message": "成功", "data": {
                "items": items, "nextPageCursor": ""}})
            return
        if self.path.endswith("/file/getDownloadUrl"):
            fid = str(payload.get("fileId") or "")
            self._send({"success": True, "code": "0", "message": "成功", "data": {
                "cdnUrl": "http://127.0.0.1:%d/cdn/%s?sig=stub" % (PORT, fid)}})
            return
        # Anything else: a permissive empty success, so the mount can proceed.
        self._send({"success": True, "code": "0", "message": "成功", "data": {}})

    def do_PUT(self):
        self._handle()


if __name__ == "__main__":
    open(LOGF, "w").close()
    log("stub 139 已启动: http://127.0.0.1:%d  (旧令牌会被拒，/refresh 发新令牌)" % PORT)
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
