#!/usr/bin/env python3
import base64
import json
import os
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PASSWORD = "fakepw"
USERS = {"go", "py"}
RESULTS = {
    "getblockchaininfo": {
        "blocks": 973456,
        "headers": 973456,
        "verificationprogress": 0.999999,
        "initialblockdownload": False,
        "pruned": False,
        "prune_target_size": 0,
        "size_on_disk": 888_000_000_000,
    },
    "getnetworkinfo": {"subversion": "/Satoshi:31.1.0/", "connections": 10},
    "getpeerinfo": [{"inbound": False} for _ in range(10)],
    "uptime": 4_700_000,
    "getmempoolinfo": {"size": 4096, "bytes": 12_582_912},
    "estimatesmartfee": {"feerate": 0.000021},
}

counts = {user: {} for user in USERS}
rejected = 0
lock = threading.Lock()


def authenticate(value):
    try:
        kind, encoded = value.split(" ", 1)
        user, password = base64.b64decode(encoded).decode().split(":", 1)
    except (ValueError, UnicodeDecodeError):
        return None
    return user if kind == "Basic" and user in USERS and password == PASSWORD else None


class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        return

    def send_json(self, status, value):
        body = json.dumps(value, sort_keys=True).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path != "/counts":
            self.send_error(404)
            return
        with lock:
            self.send_json(200, {"users": counts, "rejected": rejected})

    def do_POST(self):
        global rejected
        if self.path != "/":
            with lock:
                rejected += 1
            self.send_error(404)
            return
        user = authenticate(self.headers.get("Authorization", ""))
        try:
            length = int(self.headers.get("Content-Length", "0"))
            request = json.loads(self.rfile.read(min(length, 1_048_576)))
            method = request["method"]
            request_id = request.get("id")
        except (ValueError, KeyError, json.JSONDecodeError):
            with lock:
                rejected += 1
            self.send_json(400, {"error": "invalid request"})
            return
        if user is None or method not in RESULTS:
            with lock:
                rejected += 1
            self.send_json(401 if user is None else 400, {"error": "rejected"})
            return
        with lock:
            counts[user][method] = counts[user].get(method, 0) + 1
        self.send_json(200, {"result": RESULTS[method], "error": None, "id": request_id})


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", int(os.environ.get("PORT", "8001"))), Handler).serve_forever()
