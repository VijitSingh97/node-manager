import hmac
import json
import os
import re
import shutil
import threading
from datetime import datetime

import requests
from flask import Flask, Response, jsonify, render_template_string, request
from waitress import serve

import balance_history
import fee_history
import monitor
import watch

app = Flask(__name__, static_folder=os.path.join(os.path.dirname(__file__), "..", "..", "static"))
# Revalidate static assets every load (cheap 304s) so a deploy's new CSS/JS is
# picked up immediately — otherwise browsers heuristically cache the old files
# and show a stale dashboard until a hard refresh.
app.config["SEND_FILE_MAX_AGE_DEFAULT"] = 0

# --- CONFIGURATION ---
RPC_USER = os.environ["RPC_USER"]
RPC_PASSWORD = os.environ["RPC_PASSWORD"]
DASHBOARD_PASSWORD = os.environ.get("DASHBOARD_PASSWORD", "")
RPC_URL = 'http://172.29.0.26:8332'
BITCOIN_DIR = '/data'
DISK_WARN_FREE_GB = 50


def _read_stack_version():
    # configure.sh computes this from the git checkout: the release number for a
    # clean tagged checkout, else branch-commit for a dev build. Prefer it so a
    # pulled release image (VERSION baked in) still shows the dev id when run off
    # an unreleased checkout. Fall back to the baked VERSION, then "dev".
    v = os.environ.get("STACK_VERSION", "").strip()
    if v:
        return v
    try:
        with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "VERSION")) as f:
            v = f.read().strip()
            if v:
                return v
    except OSError:
        pass
    return "dev"


STACK_VERSION = _read_stack_version()
monitor.STACK_VERSION = STACK_VERSION  # keep the update-checker in sync


def version_label():
    # "v1.3.0" for a release (semver), the raw "branch-commit" for a dev build,
    # "dev" for an unversioned checkout
    if not STACK_VERSION or STACK_VERSION == "dev":
        return "dev"
    return f"v{STACK_VERSION}" if re.fullmatch(r"\d+\.\d+\.\d+", STACK_VERSION) else STACK_VERSION


@app.before_request
def check_auth():
    if request.path == "/static/favicon.png" and request.method in ("GET", "HEAD"):
        return None
    if not DASHBOARD_PASSWORD:
        return None
    auth = request.authorization
    if auth and auth.password and hmac.compare_digest(auth.password, DASHBOARD_PASSWORD):
        return None
    return Response("Authentication required", 401,
                    {"WWW-Authenticate": 'Basic realm="Bitcoin Node Status"'})

def get_rpc_data(method, params=None):
    payload = json.dumps({"jsonrpc": "1.0", "id": "dashboard", "method": method, "params": params or []})
    try:
        response = requests.post(RPC_URL, auth=(RPC_USER, RPC_PASSWORD), data=payload, timeout=3)
        if response.status_code == 200:
            return response.json().get('result', {})
        return None
    except Exception as e:
        print(f"RPC Error ({method}): {e}")
        return None

# The saved watch-only wallet list (managed from the UI, seeded once from
# config.json). Empty unless the operator adds any, so this is inert otherwise.
WATCH = watch.load_store()

def get_wallet_data(wallet, method, params=None, timeout=8):
    # Wallet-scoped RPC lives at /wallet/<name>. Balance reads are quick;
    # importdescriptors blocks on a rescan, so that caller passes a long timeout.
    payload = json.dumps({"jsonrpc": "1.0", "id": "dashboard", "method": method, "params": params or []})
    try:
        r = requests.post(f"{RPC_URL}/wallet/{wallet}", auth=(RPC_USER, RPC_PASSWORD), data=payload, timeout=timeout)
        return r.json().get("result") if r.status_code == 200 else None
    except Exception as e:
        print(f"Wallet RPC {method}@{wallet}: {e}")
        return None

def fee_sat_vb(blocks):
    # estimatesmartfee returns BTC/kvB (or an error during sync / with too
    # little data); convert to sat/vB (1 decimal, so a quiet-mempool sub-1
    # estimate shows as e.g. 0.4 rather than rounding to 0), or None when
    # unavailable
    est = get_rpc_data("estimatesmartfee", [blocks])
    rate = est.get("feerate") if isinstance(est, dict) else None
    if not rate:
        return None
    v = round(rate * 100000, 1)
    return int(v) if v == int(v) else v  # "10" for a whole value, "0.4" for sub-1


def format_uptime(seconds):
    if seconds is None or not isinstance(seconds, (int, float)):
        return "N/A"
    days, rem = divmod(int(seconds), 86400)
    hours, rem = divmod(rem, 3600)
    minutes, _ = divmod(rem, 60)
    return f"{days}d {hours}h {minutes}m" if days > 0 else f"{hours}h {minutes}m"

def fee_sampler_loop():
    import time
    while True:
        try:
            bc = get_rpc_data("getblockchaininfo")
            if bc and not bc.get("initialblockdownload", False):
                fee_history.record(fee_sat_vb(1))
        except Exception as e:
            print(f"Fee sampler failed: {e}")
        time.sleep(60)


@app.route('/api/fees')
def api_fees():
    return {"fee": fee_history.series()}


def balance_sampler_loop():
    # Persist each watch wallet's balance so the trend survives restarts.
    # Runs every 10 min; balance_history.record() throttles writes to hourly.
    import time
    while True:
        try:
            if WATCH:
                bc = get_rpc_data("getblockchaininfo")
                if bc and not bc.get("initialblockdownload", False):
                    watch.balances(get_wallet_data, WATCH, on_sample=balance_history.record)
        except Exception as e:
            print(f"Balance sampler failed: {e}")
        time.sleep(600)


@app.route('/metrics')
def metrics():
    """Prometheus text format, hand-rolled — what the dashboard already knows."""
    lines = []

    def m(name, value):
        lines.append(f"# TYPE {name} gauge")
        lines.append(f"{name} {value}")

    blockchain = get_rpc_data("getblockchaininfo")
    if blockchain is None:
        m("bitcoin_node_up", 0)
    else:
        network = get_rpc_data("getnetworkinfo") or {}
        peers = get_rpc_data("getpeerinfo") or []
        inbound = sum(1 for p in peers if isinstance(p, dict) and p.get("inbound"))
        _total, _used, free = shutil.disk_usage(BITCOIN_DIR)
        m("bitcoin_node_up", 1)
        m("bitcoin_blocks", blockchain.get("blocks", 0))
        m("bitcoin_headers", blockchain.get("headers", 0))
        m("bitcoin_verification_progress", blockchain.get("verificationprogress", 0))
        m("bitcoin_pruned", int(blockchain.get("pruned", False)))
        m("bitcoin_size_on_disk_bytes", blockchain.get("size_on_disk", 0))
        m("bitcoin_disk_free_bytes", free)
        m("bitcoin_uptime_seconds", get_rpc_data("uptime") or 0)
        m("bitcoin_connections", network.get("connections", 0))
        lines.append("# TYPE bitcoin_peers gauge")
        lines.append(f'bitcoin_peers{{direction="in"}} {inbound}')
        lines.append(f'bitcoin_peers{{direction="out"}} {len(peers) - inbound}')

        # mempool + fees, once synced
        if not blockchain.get("initialblockdownload", False):
            mp = get_rpc_data("getmempoolinfo")
            if isinstance(mp, dict):
                m("bitcoin_mempool_txs", mp.get("size", 0))
                m("bitcoin_mempool_bytes", mp.get("bytes", 0))
            lines.append("# TYPE bitcoin_fee_sat_vb gauge")
            for n in (1, 3, 6):
                rate = fee_sat_vb(n)
                if rate is not None:
                    lines.append(f'bitcoin_fee_sat_vb{{blocks="{n}"}} {rate}')
    return Response("\n".join(lines) + "\n", mimetype="text/plain; version=0.0.4")


# --- watch-only wallets: list + add + remove --------------------------------
# All routes are already gated by the optional dashboard password (before_request).
# Writes additionally require the X-Requested-With header the front-end sends —
# a browser can't set it on a cross-site form post without a CORS preflight we
# never grant, so this blocks CSRF against the add/remove actions.

def _reject_csrf():
    if request.headers.get("X-Requested-With") != "fetch":
        return Response("missing X-Requested-With", 403)
    return None


@app.route('/api/watch', methods=['GET'])
def api_watch_list():
    view = watch.balances_view(get_wallet_data, WATCH)
    for row in view["wallets"]:
        hist = balance_history.series(row["key"])  # persisted trend (keyed by key)
        row["history"] = hist
        # Node unreachable but we have a cached balance → show the last known
        # value, flagged stale, instead of a dash.
        if row["state"] == "error" and hist:
            row["state"] = "stale"
            row["btc"] = watch.fmt_btc(hist[-1])
    view["has_password"] = bool(DASHBOARD_PASSWORD)
    return jsonify(view)


@app.route('/api/watch', methods=['POST'])
def api_watch_add():
    bad = _reject_csrf()
    if bad:
        return bad
    data = request.get_json(silent=True) or {}
    # A new import needs a full-chain rescan, which a pruned node can't do —
    # check before validating/starting the thread, so the wallet is never
    # added to the store only to sit unprovisioned forever (provision_one
    # would otherwise silently skip the import — watch.py:178).
    pruned = (get_rpc_data("getblockchaininfo") or {}).get("pruned", False)
    if pruned:
        return Response("This node is pruned — a full node is required to add watch-only wallets", 400)
    try:
        entry = watch.add_entry(WATCH, data.get("name"), data.get("key"), data.get("birthday"))
    except ValueError as e:
        return Response(str(e), 400)
    # Import rescans the chain — do it off the request thread with a long
    # timeout; the UI shows "scanning…" until it finishes.
    threading.Thread(
        target=lambda: watch.provision_one(
            get_rpc_data,
            lambda w, m, p=None: get_wallet_data(w, m, p, timeout=3600),
            entry, pruned),
        daemon=True,
    ).start()
    return jsonify({"ok": True})


@app.route('/api/watch/<name>', methods=['DELETE'])
def api_watch_remove(name):
    bad = _reject_csrf()
    if bad:
        return bad
    if watch.remove_entry(WATCH, name):
        watch.deprovision(get_rpc_data, name)
        # Keep the history (and the unloaded Core wallet) so re-adding the same
        # key restores the sparkline and reloads instantly — no rescan.
        return jsonify({"ok": True})
    return Response("no such wallet", 404)


@app.route('/')
def index():
    # Fetch core data
    blockchain = get_rpc_data("getblockchaininfo")
    network = get_rpc_data("getnetworkinfo")
    peers_list = get_rpc_data("getpeerinfo")
    uptime_seconds = get_rpc_data("uptime")
    
    # If blockchain info is missing, the node isn't ready
    if blockchain is None:
        return render_template_string("""
            <html>
            <head>
                <meta charset="utf-8">
                <meta name="viewport" content="width=device-width, initial-scale=1">
                <title>Bitcoin Node Status</title>
                <link rel="icon" type="image/png" sizes="32x32" href="/static/favicon.png?v=tower-1">
                <link rel="stylesheet" href="/static/dashboard.css">
                <script>(()=>{try{const t=localStorage.getItem("dashboardTheme");document.documentElement.setAttribute("data-theme",(t==="light"||t==="dark")?t:"auto");}catch{document.documentElement.setAttribute("data-theme","auto");}})();</script>
            </head>
            <body class="center-screen">
                <canvas id="tower"></canvas>
                <div id="tower-label"></div>
                <button id="theme-toggle" class="theme-toggle" title="Toggle light / dark theme" aria-label="Toggle theme">Auto</button>
                <aside id="update-notice" class="update-notice update-notice-global" role="status"{% if not update_note %} hidden{% endif %}>{% if update_note %}<strong>🆕 {{update_note}}</strong> <a href="https://github.com/VijitSingh97/node-manager/blob/main/docs/operations.md#upgrading" target="_blank" rel="noopener noreferrer">Update instructions</a>{% endif %}</aside>
                <div id="live">
                <div class="loading-card">
                    <h2>Bitcoin Node Initializing</h2>
                    <div class="spinner"></div>
                    <p>Connecting to the Bitcoin node...</p>
                    <p style="color: var(--muted); font-size: 0.8rem;">The dashboard will load automatically when the node is ready.</p>
                    <p style="color: var(--muted); font-size: 0.8rem;">Still stuck after a few minutes? Run <code>./stack doctor</code> to check what's wrong.</p>
                    <p style="color: var(--faint); font-size: 0.7rem;">{{version}}</p>
                </div>
                </div>
                <script type="module" src="/static/tower.js"></script>
                <script type="module" src="/static/theme.js"></script>
                <script type="module" src="/static/refresh.js"></script>
            </body>
            </html>
        """, version=version_label(), update_note=monitor.update_available)

    # Calculate Inbound vs Outbound
    inbound, outbound = 0, 0
    if isinstance(peers_list, list):
        for peer in peers_list:
            if peer.get('inbound'): inbound += 1
            else: outbound += 1
    
    # Calculate Disk
    node_bytes = blockchain.get("size_on_disk", 0)
    total, _used, free = shutil.disk_usage(BITCOIN_DIR)
    free_gb = round(free / (1024**3), 2)
    last_update = datetime.now().strftime("%H:%M:%S")

    # Mempool + fee estimates — only once synced (estimatesmartfee has no data
    # during initial block download)
    ibd = blockchain.get("initialblockdownload", False)
    mempool = get_rpc_data("getmempoolinfo") if not ibd else None
    fees = {n: fee_sat_vb(n) for n in (1, 3, 6)} if not ibd else {}

    stats = {
        "version": network.get("subversion", "Unknown") if network else "Unknown",
        "uptime": format_uptime(uptime_seconds),
        "blocks": blockchain.get("blocks", "N/A"),
        "headers": blockchain.get("headers", "N/A"),
        "progress": round(blockchain.get("verificationprogress", 0) * 100, 4),
        "total_peers": network.get("connections", 0) if network else 0,
        "inbound": inbound,
        "outbound": outbound,
        "pruned": blockchain.get("pruned", False),
        "prune_target_gb": round(blockchain.get("prune_target_size", 0) / (1024**3), 1),
        "stack_version": version_label(),
        "node_gb": round(node_bytes / (1024**3), 2),
        "total_gb": round(total / (1024**3), 2),
        "free_gb": free_gb,
        "disk_warn": free_gb < DISK_WARN_FREE_GB,
        "disk_percent": round((node_bytes / total) * 100, 2) if total > 0 else 0,
        "show_mempool": bool(mempool),
        "mempool_txs": mempool.get("size") if mempool else None,
        "mempool_mb": round(mempool.get("bytes", 0) / (1024**2), 1) if mempool else None,
        "fee_next": fees.get(1),
        "fee_30m": fees.get(3),
        "fee_hour": fees.get(6),
        "next_block": (blockchain.get("blocks") + 1) if isinstance(blockchain.get("blocks"), int) else "",
        "last_update": last_update,
        # not "update": Jinja resolves stats.update to the dict METHOD, which
        # is always truthy and renders as its repr
        "update_note": monitor.update_available,
    }

    return render_template_string("""
    <html>
    <head>
        <meta charset="utf-8">
        <meta name="viewport" content="width=device-width, initial-scale=1">
        <title>Bitcoin Node Status</title>
                <link rel="icon" type="image/png" sizes="32x32" href="/static/favicon.png?v=tower-1">
        <link rel="stylesheet" href="/static/dashboard.css">
        <script>(()=>{try{const t=localStorage.getItem("dashboardTheme");document.documentElement.setAttribute("data-theme",(t==="light"||t==="dark")?t:"auto");}catch{document.documentElement.setAttribute("data-theme","auto");}})();</script>
    </head>
    <body class="center">
        <canvas id="tower"></canvas>
                <div id="tower-label"></div>
                <button id="theme-toggle" class="theme-toggle" title="Toggle light / dark theme" aria-label="Toggle theme">Auto</button>
                <aside id="update-notice" class="update-notice update-notice-global" role="status"{% if not stats.update_note %} hidden{% endif %}>{% if stats.update_note %}<strong>🆕 {{stats.update_note}}</strong> <a href="https://github.com/VijitSingh97/node-manager/blob/main/docs/operations.md#upgrading" target="_blank" rel="noopener noreferrer">Update instructions</a>{% endif %}</aside>
        <div class="stack-col">
        <div id="tower-stats" class="tower-stats" hidden></div>
        <div id="live" data-blocks="{{stats.blocks}}" data-next-block="{{stats.next_block}}">
        <div class="card">
            <h2>Bitcoin Node Status<span class="badge">{% if stats.pruned %}Pruned &middot; {{stats.prune_target_gb}} GB{% else %}Full{% endif %}</span></h2>
            <div class="row"><span class="label">Bitcoin Core:</span> <span>{{stats.version}}</span></div>
            <div class="row"><span class="label">Uptime:</span> <span>{{stats.uptime}}</span></div>
            <div class="row"><span class="label">Total Connections:</span> <span>{{stats.total_peers}}</span></div>
            <div class="peer-box">
                <div class="peer-tag">In: <span class="in">{{stats.inbound}}</span></div>
                <div class="peer-tag">Out: <span class="out">{{stats.outbound}}</span></div>
            </div>
            <hr>
            <div class="row"><span class="label">Blocks:</span> <span>{{stats.blocks}} / {{stats.headers}}</span></div>
            <div class="progress-bg"><div class="progress-fill" style="width: {{stats.progress}}%"></div></div>
            <div class="row"><span class="label">Sync Progress:</span> <span>{{stats.progress}}%</span></div>
            <hr>
            <div class="row"><span class="label">Node Data Size:</span> <span>{{stats.node_gb}} GB</span></div>
            <div class="row"><span class="label">Disk Capacity:</span> <span>{{stats.total_gb}} GB</span></div>
            <div class="row"><span class="label">Disk Usage:</span> <span>{{stats.disk_percent}}%</span></div>
            <div class="row"><span class="label">Disk Free:</span> <span {% if stats.disk_warn %}class="warn"{% endif %}>{{stats.free_gb}} GB{% if stats.disk_warn %} &#9888; LOW{% endif %}</span></div>
            {% if stats.show_mempool %}
            <hr>
            <div class="row"><span class="label">Mempool:</span> <span>{{stats.mempool_txs}} tx &middot; {{stats.mempool_mb}} MB</span></div>
            <div class="row"><span class="label">Fee sat/vB (next/30m/1h):</span> <span>{{ stats.fee_next if stats.fee_next is not none else '—' }} / {{ stats.fee_30m if stats.fee_30m is not none else '—' }} / {{ stats.fee_hour if stats.fee_hour is not none else '—' }}</span></div>
            <div class="spark-row"><span class="label">Fee (24h)</span><canvas class="spark" id="spark-fee"></canvas></div>
            {% endif %}
            <div class="footer">
                <span>Updated: {{stats.last_update}}</span>
                <span>{{stats.stack_version}}</span>
                <span>Auto-refresh: 30s</span>
            </div>
        </div>
        </div>
        <section id="watch" class="card watch-card" hidden></section>
        </div>
        <script type="module" src="/static/tower.js"></script>
        <script type="module" src="/static/theme.js"></script>
        <script type="module" src="/static/sparkline.js"></script>
        <script type="module" src="/static/refresh.js"></script>
        <script type="module" src="/static/watch.js"></script>
    </body>
    </html>
    """, stats=stats)

def main():
    # Always runs: alerts and pings no-op individually when unconfigured,
    # and the update checker works regardless.
    threading.Thread(
        target=monitor.monitor_loop,
        args=(
            lambda: get_rpc_data("getblockchaininfo"),
            lambda: get_rpc_data("getnetworkinfo"),
        ),
        daemon=True,
    ).start()
    # records the next-block fee once a minute for the sparkline
    threading.Thread(target=fee_sampler_loop, daemon=True).start()
    # persists watch-only balances so their trend survives restarts
    threading.Thread(target=balance_sampler_loop, daemon=True).start()
    # provision any watch-only wallets once bitcoind is answering; the first
    # import rescans the chain, so keep it off the request path
    if WATCH:
        import time

        def _watch_setup():
            while True:
                chain = get_rpc_data("getblockchaininfo")
                if chain:
                    watch.ensure_wallets(
                        get_rpc_data,
                        lambda w, m, p=None: get_wallet_data(w, m, p, timeout=3600),
                        WATCH, pruned=chain.get("pruned", False),
                    )
                    return
                time.sleep(10)  # node still starting

        threading.Thread(target=_watch_setup, daemon=True).start()
    serve(app, host="0.0.0.0", port=8000)


if __name__ == "__main__":
    main()
