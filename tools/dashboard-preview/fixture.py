import json
from http.server import BaseHTTPRequestHandler

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


class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        return

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        body = json.dumps({"result": RESULTS[request["method"]], "error": None, "id": request.get("id")}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
