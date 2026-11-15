import node_status


def test_main_starts_background_tasks_and_production_server(monkeypatch):
    threads = []
    served = {}

    class FakeThread:
        def __init__(self, target, args=(), daemon=None):
            threads.append((target, args, daemon))

        def start(self):
            pass

    monkeypatch.setattr(node_status.threading, "Thread", FakeThread)
    monkeypatch.setattr(node_status, "WATCH", [{"name": "Cold"}])
    monkeypatch.setattr(
        node_status,
        "serve",
        lambda app, **kwargs: served.update(app=app, **kwargs),
    )

    node_status.main()

    assert [target.__name__ for target, _, _ in threads] == [
        "monitor_loop",
        "fee_sampler_loop",
        "balance_sampler_loop",
        "_watch_setup",
    ]
    assert all(daemon is True for _, _, daemon in threads)
    assert served == {"app": node_status.app, "host": "0.0.0.0", "port": 8000}


# --- format_uptime ---

def test_uptime_none():
    assert node_status.format_uptime(None) == "—"


def test_uptime_not_a_number():
    assert node_status.format_uptime("soon") == "—"


def test_uptime_with_days():
    assert node_status.format_uptime(90061) == "1d 1h 1m"


def test_uptime_under_a_day():
    assert node_status.format_uptime(3660) == "1h 1m"


def test_uptime_under_an_hour():
    assert node_status.format_uptime(300) == "5m"


# --- get_rpc_data ---

def test_rpc_error_returns_none(monkeypatch):
    def boom(*args, **kwargs):
        raise node_status.requests.exceptions.ConnectionError("refused")

    monkeypatch.setattr(node_status.requests, "post", boom)
    assert node_status.get_rpc_data("uptime") is None


def test_rpc_non_200_returns_none(monkeypatch):
    class Resp:
        status_code = 500

    monkeypatch.setattr(node_status.requests, "post", lambda *a, **kw: Resp())
    assert node_status.get_rpc_data("uptime") is None


def test_rpc_uses_credentials_from_env(monkeypatch):
    seen = {}

    class Resp:
        status_code = 200

        @staticmethod
        def json():
            return {"result": {"ok": True}}

    def capture(url, auth, data, timeout):
        seen["auth"] = auth
        return Resp()

    monkeypatch.setattr(node_status.requests, "post", capture)
    assert node_status.get_rpc_data("uptime") == {"ok": True}
    assert seen["auth"] == ("testuser", "testpass")


# --- get_wallet_data (the wallet-scoped twin of get_rpc_data) ---

def test_wallet_rpc_error_returns_none(monkeypatch):
    def boom(*a, **k):
        raise node_status.requests.exceptions.ConnectionError("refused")

    monkeypatch.setattr(node_status.requests, "post", boom)
    assert node_status.get_wallet_data("watch_A", "getwalletinfo") is None


def test_wallet_rpc_non_200_returns_none(monkeypatch):
    class Resp:
        status_code = 500

    monkeypatch.setattr(node_status.requests, "post", lambda *a, **kw: Resp())
    assert node_status.get_wallet_data("watch_A", "getwalletinfo") is None


def test_wallet_rpc_hits_wallet_path_with_credentials(monkeypatch):
    seen = {}

    class Resp:
        status_code = 200

        @staticmethod
        def json():
            return {"result": {"ok": True}}

    def capture(url, auth, data, timeout):
        seen.update(url=url, auth=auth, timeout=timeout)
        return Resp()

    monkeypatch.setattr(node_status.requests, "post", capture)
    assert node_status.get_wallet_data("watch_A", "getwalletinfo", timeout=3) == {"ok": True}
    assert seen["url"].endswith("/wallet/watch_A")  # wallet-scoped endpoint
    assert seen["auth"] == ("testuser", "testpass")
    assert seen["timeout"] == 3


# --- _read_stack_version: env wins, else baked VERSION, else "dev" ---

def test_read_stack_version_prefers_env_then_dev(monkeypatch):
    monkeypatch.setenv("STACK_VERSION", "1.2.3")
    assert node_status._read_stack_version() == "1.2.3"
    monkeypatch.setenv("STACK_VERSION", "")  # empty -> fall through; no baked VERSION here
    assert node_status._read_stack_version() == "dev"


# --- index route ---

FAKE_RPC = {
    "getblockchaininfo": {
        "blocks": 900000,
        "headers": 900000,
        "verificationprogress": 1.0,
        "size_on_disk": 800 * 1024**3,
    },
    "getnetworkinfo": {"subversion": "/Satoshi:28.0.0/", "connections": 10},
    "getpeerinfo": [{"inbound": True}, {"inbound": False}, {"inbound": False}],
    "uptime": 90061,
    "getmempoolinfo": {"size": 4200, "bytes": 12 * 1024**2},
    "estimatesmartfee": {"feerate": 0.0001, "blocks": 1},  # 0.0001 BTC/kvB = 10 sat/vB
}

BIG_DISK = (2 * 1024**4, 1 * 1024**4, 1 * 1024**4)  # 1 TB free


def render_index(monkeypatch, rpc=FAKE_RPC, disk=BIG_DISK):
    monkeypatch.setattr(node_status, "get_rpc_data", lambda method, params=None: rpc.get(method))
    monkeypatch.setattr(node_status.shutil, "disk_usage", lambda path: disk)
    return node_status.app.test_client().get("/")


def test_index_shows_loading_page_when_node_unreachable(monkeypatch):
    monkeypatch.setattr(node_status, "get_rpc_data", lambda method, params=None: None)
    resp = node_status.app.test_client().get("/")
    assert resp.status_code == 200
    assert b"Initializing" in resp.data


def test_index_renders_node_stats(monkeypatch):
    resp = render_index(monkeypatch)
    body = resp.data.decode()
    assert resp.status_code == 200
    assert "900000 / 900000" in body
    assert "/Satoshi:28.0.0/" in body
    assert "1d 1h 1m" in body
    assert "100%" in body  # sync progress
    assert ">1</span>" in body  # 1 inbound peer
    assert "800.0 GB" in body  # size on disk
    assert "Pruned" not in body  # full node: no badge
    assert "LOW" not in body  # 1 TB free: no warning
    # regression: "update" as a stats key shadowed dict.update in Jinja and
    # rendered "<built-in method update of dict object ...>" on every page
    assert "🆕" not in body
    assert "built-in method" not in body
    # a full node shows a "Full" badge, never "Pruned"
    assert ">Full<" in body
    assert "Pruned" not in body


def test_index_shows_stack_version(monkeypatch):
    monkeypatch.setattr(node_status, "STACK_VERSION", "1.3.0")
    body = render_index(monkeypatch).data.decode()
    assert "v1.3.0" in body


def test_index_shows_dev_when_unversioned(monkeypatch):
    monkeypatch.setattr(node_status, "STACK_VERSION", "dev")
    body = render_index(monkeypatch).data.decode()
    assert ">dev<" in body
    assert "vdev" not in body  # not v-prefixed


def test_index_shows_branch_commit_for_dev_build(monkeypatch):
    # a non-semver version (branch-commit) renders as-is, no "v" prefix
    monkeypatch.setattr(node_status, "STACK_VERSION", "main-abc1234")
    body = render_index(monkeypatch).data.decode()
    assert "main-abc1234" in body
    assert "vmain-abc1234" not in body


def test_loading_page_shows_version(monkeypatch):
    monkeypatch.setattr(node_status, "get_rpc_data", lambda method, params=None: None)
    monkeypatch.setattr(node_status, "STACK_VERSION", "1.3.0")
    body = node_status.app.test_client().get("/").data.decode()
    assert "Initializing" in body
    assert "v1.3.0" in body


def test_update_badge_shows_when_update_available(monkeypatch):
    monkeypatch.setattr(node_status.monitor, "update_available", "stack v1.3.0 available (running v1.2.0)")
    body = render_index(monkeypatch).data.decode()
    assert "🆕 stack v1.3.0 available" in body
    assert "docs/operations.md#upgrading" in body


# --- mempool + fees ---

def test_index_shows_mempool_and_fees(monkeypatch):
    body = render_index(monkeypatch).data.decode()
    assert "Mempool:" in body
    assert "4200 tx" in body
    assert "12.0 MiB" in body
    assert "10 / 10 / 10" in body  # 0.0001 BTC/kvB -> 10 sat/vB (whole -> no ".0")


def test_fee_sat_vb_rounding(monkeypatch):
    def with_rate(est):
        monkeypatch.setattr(node_status, "get_rpc_data", lambda m, params=None: est)
        return node_status.fee_sat_vb(1)

    assert with_rate({"feerate": 0.0001}) == 10  # whole -> int, renders "10" not "10.0"
    assert isinstance(with_rate({"feerate": 0.0001}), int)
    assert with_rate({"feerate": 0.00000442}) == 0.4  # sub-1 kept (the "1h = —" bug)
    assert with_rate({"errors": ["x"]}) is None  # no estimate -> None (shown —), not 0
    assert with_rate(None) is None


def test_index_shows_dash_only_when_no_estimate(monkeypatch):
    # 1h target returns a sub-1 fee; it must show "0.4", not "—"
    def rpc(method, params=None):
        if method == "estimatesmartfee":
            return {"feerate": 0.00000442} if params[0] == 6 else {"feerate": 0.00001075}
        return FAKE_RPC.get(method)

    monkeypatch.setattr(node_status, "get_rpc_data", rpc)
    monkeypatch.setattr(node_status.shutil, "disk_usage", lambda p: BIG_DISK)
    body = node_status.app.test_client().get("/").data.decode()
    assert "1.1 / 1.1 / 0.4" in body
    assert "/ —" not in body  # sub-1 is a number, not a dash


def test_index_hides_mempool_during_sync(monkeypatch):
    rpc = dict(FAKE_RPC)
    rpc["getblockchaininfo"] = dict(FAKE_RPC["getblockchaininfo"], initialblockdownload=True)
    body = render_index(monkeypatch, rpc=rpc).data.decode()
    assert "Mempool:" not in body


# --- theming ---

def test_theme_wired_on_both_pages(monkeypatch):
    # theme-init runs before paint (no flash), the toggle button and the
    # module are present, and no palette is hard-coded in the page anymore
    full = render_index(monkeypatch).data.decode()
    monkeypatch.setattr(node_status, "get_rpc_data", lambda method, params=None: None)
    loading = node_status.app.test_client().get("/").data.decode()
    for body in (full, loading):
        assert '/static/dashboard.css' in body
        assert 'data-theme' in body
        assert 'id="theme-toggle"' in body
        assert '/static/theme.js' in body
        assert '#0f0f0f' not in body  # colours live in the stylesheet now


def test_tower_and_live_refresh_wired_on_both_pages(monkeypatch):
    # the tower canvas + module are present, the live panel is swappable,
    # and the page updates by polling (no full-page meta refresh that would
    # reset the animation)
    full = render_index(monkeypatch).data.decode()
    monkeypatch.setattr(node_status, "get_rpc_data", lambda method, params=None: None)
    loading = node_status.app.test_client().get("/").data.decode()
    for body in (full, loading):
        assert '<canvas id="tower">' in body
        assert '/static/tower.js' in body
        assert 'id="live"' in body
        assert '/static/refresh.js' in body
        assert 'http-equiv="refresh"' not in body
    # the live panel carries the block height for the tower + label
    assert 'data-blocks="900000"' in full
    assert 'data-next-block="900001"' in full  # the block being loaded
    assert 'id="tower-label"' in full
    # the fee sparkline is back (fee-only); the tower no longer tracks history
    assert 'id="spark-fee"' in full
    assert '/static/sparkline.js' in full
    assert '/api/history' not in full  # only /api/fees now
    assert 'id="spark-height"' not in full


def test_api_fees_endpoint():
    body = node_status.app.test_client().get("/api/fees")
    assert body.status_code == 200
    assert "fee" in body.get_json()


def test_api_fees_respects_auth(monkeypatch):
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "hunter2")
    assert node_status.app.test_client().get("/api/fees").status_code == 401


def test_favicon_linked_and_served(monkeypatch):
    monkeypatch.setattr(node_status, "get_rpc_data", lambda m, params=None: None)
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "hunter2")
    client = node_status.app.test_client()
    assert '<link rel="icon" type="image/png" sizes="32x32" href="/static/favicon.png?v=tower-1">' in client.get("/", auth=("anyuser", "hunter2")).data.decode()
    icon = client.get("/static/favicon.png?v=tower-1")
    assert icon.status_code == 200
    assert icon.content_type == "image/png"
    assert icon.data.startswith(b"\x89PNG\r\n\x1a\n")


def test_tower_and_refresh_assets_served():
    client = node_status.app.test_client()
    tower = client.get("/static/tower.js")
    assert tower.status_code == 200
    assert b"project" in tower.data
    assert client.get("/static/refresh.js").status_code == 200


def test_static_css_and_js_are_served():
    client = node_status.app.test_client()
    css = client.get("/static/dashboard.css")
    assert css.status_code == 200
    assert b"--accent" in css.data
    js = client.get("/static/theme.js")
    assert js.status_code == 200
    assert b"nextTheme" in js.data


def test_index_panel_shared_labels_and_formats(monkeypatch):
    rpc = dict(FAKE_RPC)
    rpc["getblockchaininfo"] = dict(FAKE_RPC["getblockchaininfo"], size_on_disk=0, verificationprogress=0.99999, blocks=900000, headers=900000)
    body = render_index(monkeypatch, rpc=rpc).data.decode()
    assert "Version:</span>" in body and "Bitcoin Core:" not in body
    assert "Node Data Size:</span> <span>—</span>" in body
    assert "Sync Progress:</span> <span>100%</span>" in body


def test_static_assets_respect_auth(monkeypatch):
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "hunter2")
    assert node_status.app.test_client().get("/static/dashboard.css").status_code == 401


# --- pruned badge and disk warning ---

def test_index_shows_pruned_badge(monkeypatch):
    rpc = dict(FAKE_RPC)
    rpc["getblockchaininfo"] = dict(
        FAKE_RPC["getblockchaininfo"],
        pruned=True, prune_target_size=10 * 1024**3, size_on_disk=11 * 1024**3,
    )
    body = render_index(monkeypatch, rpc=rpc).data.decode()
    assert "Pruned" in body
    assert ">Pruned<" in body  # bare badge, no prune target
    assert ">Full<" not in body  # pruned and full badges are exclusive


def test_index_warns_on_low_disk(monkeypatch):
    low = (2 * 1024**4, 2 * 1024**4 - 20 * 1024**3, 20 * 1024**3)  # 20 GB free
    body = render_index(monkeypatch, disk=low).data.decode()
    assert "LOW" in body
    assert 'class="warn"' in body


# --- prometheus metrics ---

def test_metrics_when_node_up(monkeypatch):
    monkeypatch.setattr(node_status, "get_rpc_data", lambda method, params=None: FAKE_RPC.get(method))
    monkeypatch.setattr(node_status.shutil, "disk_usage", lambda path: BIG_DISK)
    resp = node_status.app.test_client().get("/metrics")
    body = resp.data.decode()
    assert resp.status_code == 200
    assert "bitcoin_node_up 1" in body
    assert "bitcoin_blocks 900000" in body
    assert 'bitcoin_peers{direction="in"} 1' in body
    assert 'bitcoin_peers{direction="out"} 2' in body
    assert "bitcoin_pruned 0" in body


def test_metrics_when_node_down(monkeypatch):
    monkeypatch.setattr(node_status, "get_rpc_data", lambda method, params=None: None)
    body = node_status.app.test_client().get("/metrics").data.decode()
    assert "bitcoin_node_up 0" in body
    assert "bitcoin_blocks" not in body


def test_metrics_includes_mempool_and_fees(monkeypatch):
    monkeypatch.setattr(node_status, "get_rpc_data", lambda method, params=None: FAKE_RPC.get(method))
    monkeypatch.setattr(node_status.shutil, "disk_usage", lambda path: BIG_DISK)
    body = node_status.app.test_client().get("/metrics").data.decode()
    assert "bitcoin_mempool_txs 4200" in body
    assert 'bitcoin_fee_sat_vb{blocks="1"} 10' in body


def test_metrics_respects_auth(monkeypatch):
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "hunter2")
    resp = node_status.app.test_client().get("/metrics")
    assert resp.status_code == 401


# --- optional basic auth ---

def test_no_password_means_no_auth(monkeypatch):
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "")
    resp = render_index(monkeypatch)
    assert resp.status_code == 200


def test_auth_rejects_missing_credentials(monkeypatch):
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "hunter2")
    resp = node_status.app.test_client().get("/")
    assert resp.status_code == 401
    assert "WWW-Authenticate" in resp.headers


def test_auth_rejects_wrong_password(monkeypatch):
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "hunter2")
    resp = node_status.app.test_client().get("/", auth=("x", "wrong"))
    assert resp.status_code == 401


def test_auth_accepts_correct_password(monkeypatch):
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "hunter2")
    monkeypatch.setattr(node_status, "get_rpc_data", lambda method, params=None: FAKE_RPC.get(method))
    monkeypatch.setattr(node_status.shutil, "disk_usage", lambda path: BIG_DISK)
    resp = node_status.app.test_client().get("/", auth=("anyuser", "hunter2"))
    assert resp.status_code == 200
    assert b"Sync Progress" in resp.data


# --- watch-only wallets (client-rendered card + /api/watch) ---

BIP84_ZPUB = ("zpub6rFR7y4Q2AijBEqTUquhVz398htDFrtymD9xYYfG1m4wAcvPhXNfE3EfH1r1"
              "ADqtfSdVCToUG868RvUUkgDKf31mGDtKsAYz2oz2AGutZYs")


def test_index_has_watch_card_and_loads_module(monkeypatch):
    body = render_index(monkeypatch).data.decode()
    assert 'id="watch"' in body
    assert "/static/watch.js" in body


def test_api_watch_list_returns_balances_and_total(monkeypatch):
    monkeypatch.setattr(node_status, "WATCH",
                        [{"name": "Cold", "key": "z"}, {"name": "Hot", "key": "z"}])
    monkeypatch.setattr(node_status, "get_wallet_data",
                        lambda w, m, p=None, timeout=8: {"scanning": False} if m == "getwalletinfo"
                        else {"mine": {"trusted": 1.0}})
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "")
    data = node_status.app.test_client().get("/api/watch", headers={"X-Requested-With": "fetch"}).get_json()
    assert data["show_total"] is True and data["total"] == "2"
    assert data["has_password"] is False
    assert [w["name"] for w in data["wallets"]] == ["Cold", "Hot"]


def test_api_watch_add_requires_csrf_header(monkeypatch):
    monkeypatch.setattr(node_status, "WATCH", [])
    resp = node_status.app.test_client().post("/api/watch", json={"name": "X", "key": BIP84_ZPUB})
    assert resp.status_code == 403  # no X-Requested-With


def test_api_watch_remove_requires_csrf_header(monkeypatch):
    # DELETE without X-Requested-With must be rejected (CSRF guard), like POST.
    resp = node_status.app.test_client().delete("/api/watch/Cold")
    assert resp.status_code == 403  # no X-Requested-With header


def test_api_watch_add_rejects_bad_key(monkeypatch):
    monkeypatch.setattr(node_status, "WATCH", [])
    resp = node_status.app.test_client().post(
        "/api/watch", json={"name": "X", "key": "not-a-key"},
        headers={"X-Requested-With": "fetch"})
    assert resp.status_code == 400


def test_api_watch_add_rejects_pruned_node(monkeypatch):
    # A pruned node can't rescan for a new import — provision_one would just
    # silently skip it forever, so the API must refuse before the wallet is
    # ever added to the store.
    store = []
    monkeypatch.setattr(node_status, "WATCH", store)
    monkeypatch.setattr(node_status, "get_rpc_data",
                        lambda m, p=None: {"pruned": True} if m == "getblockchaininfo" else None)
    resp = node_status.app.test_client().post(
        "/api/watch", json={"name": "X", "key": BIP84_ZPUB},
        headers={"X-Requested-With": "fetch"})
    assert resp.status_code == 400
    assert "pruned" in resp.get_data(as_text=True).lower()
    assert store == []  # never added


def test_api_watch_accepts_every_key_type(monkeypatch, tmp_path):
    # the mock-data (CI) counterpart of the e2e provisioning test: each
    # supported input type is accepted and kicks off provisioning. Satoshi's
    # genesis key has no xpub/zpub, so its cases are the address + pubkey forms.
    monkeypatch.setenv("WATCH_STORE", str(tmp_path / "w.json"))
    store = []
    monkeypatch.setattr(node_status, "WATCH", store)
    provisioned = []
    monkeypatch.setattr(node_status.watch, "provision_one",
                        lambda rpc, wrpc, entry, pruned=False: provisioned.append(entry["key"]))
    monkeypatch.setattr(node_status, "get_rpc_data", lambda m, p=None: {"pruned": False})
    client = node_status.app.test_client()
    genesis_pk = ("04678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f6"
                  "1deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5f")
    cases = {
        "xpub": ("xpub6CatWdiZiodmUeTDp8LT5or8nmbKNcuyvz7WyksVFkKB4RHwCD3Xyuv"
                 "PEbvqAQY3rAPshWcMLoP2fMFMKHPJ4ZeZXYVUhLv1VMrjPC7PW6V"),
        "zpub": BIP84_ZPUB,
        "satoshi-address": "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
        "satoshi-pubkey": f"pkh({genesis_pk})",
    }
    for name, key in cases.items():
        r = client.post("/api/watch", json={"name": name, "key": key},
                        headers={"X-Requested-With": "fetch"})
        assert r.status_code == 200, (name, r.data)
    assert [w["name"] for w in store] == list(cases)
    assert len(provisioned) == len(cases)  # each type started provisioning


def test_api_watch_shows_stale_balance_when_node_down(monkeypatch, tmp_path):
    # node unreachable (getwalletinfo → None) but we have cached history → show
    # the last known value flagged stale, not a dash
    monkeypatch.setenv("BALANCE_HISTORY", str(tmp_path / "bh.json"))
    monkeypatch.setattr(node_status.balance_history, "_data", None)
    node_status.balance_history.record("zkey", 1.25, now=0)  # a cached point
    monkeypatch.setattr(node_status, "WATCH", [{"name": "Cold", "key": "zkey"}])
    monkeypatch.setattr(node_status, "get_wallet_data", lambda w, m, p=None, timeout=8: None)
    monkeypatch.setattr(node_status, "DASHBOARD_PASSWORD", "")
    row = node_status.app.test_client().get(
        "/api/watch", headers={"X-Requested-With": "fetch"}).get_json()["wallets"][0]
    assert row["state"] == "stale" and row["btc"] == "1.25"


def test_api_watch_remove_keeps_history(monkeypatch, tmp_path):
    monkeypatch.setenv("WATCH_STORE", str(tmp_path / "w.json"))
    monkeypatch.setenv("BALANCE_HISTORY", str(tmp_path / "bh.json"))
    monkeypatch.setattr(node_status.balance_history, "_data", None)
    node_status.balance_history.record("zkey", 2.5, now=0)
    monkeypatch.setattr(node_status, "WATCH", [{"name": "Cold", "key": "zkey"}])
    monkeypatch.setattr(node_status, "get_rpc_data", lambda m, p=None: None)
    node_status.app.test_client().delete("/api/watch/Cold", headers={"X-Requested-With": "fetch"})
    assert node_status.balance_history.series("zkey") == [2.5]  # retained, not forgotten


def test_api_watch_add_then_remove(monkeypatch, tmp_path):
    monkeypatch.setenv("WATCH_STORE", str(tmp_path / "w.json"))
    store = []
    monkeypatch.setattr(node_status, "WATCH", store)
    monkeypatch.setattr(node_status.watch, "provision_one", lambda *a, **k: None)  # no rescan
    calls = []

    def fake_rpc(method, params=None):
        calls.append((method, params))
        return {"pruned": False}
    monkeypatch.setattr(node_status, "get_rpc_data", fake_rpc)
    client = node_status.app.test_client()

    r = client.post("/api/watch", json={"name": "Cold", "key": BIP84_ZPUB, "birthday": "2021-01-01"},
                    headers={"X-Requested-With": "fetch"})
    assert r.status_code == 200 and [w["name"] for w in store] == ["Cold"]

    r = client.delete("/api/watch/Cold", headers={"X-Requested-With": "fetch"})
    assert r.status_code == 200 and store == []
    assert ("unloadwallet", ["watch_Cold", False]) in calls  # Core told to unload + forget

    r = client.delete("/api/watch/Cold", headers={"X-Requested-With": "fetch"})
    assert r.status_code == 404  # already gone
