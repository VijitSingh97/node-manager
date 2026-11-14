import base64
import json

import watch

# --- base58check (the foundation the xpub conversion rests on) ---

def test_b58_decode_known_address():
    # a canonical P2PKH address: version byte 0x00 + 20-byte hash
    data = watch._b58decode_check("1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2")
    assert data[0] == 0 and len(data) == 21


def test_b58_roundtrip():
    payload = bytes(range(25))
    assert watch._b58decode_check(watch._b58encode_check(payload)) == payload


def test_b58_rejects_tampered_checksum():
    good = watch._b58encode_check(b"hello world payload!!")
    bad = good[:-1] + ("Z" if good[-1] != "Z" else "Y")
    try:
        watch._b58decode_check(bad)
        assert False, "tampered string should not verify"
    except ValueError:
        pass


# --- SLIP-132 zpub -> xpub (Core rejects zpub; the key must survive intact) ---

# BIP84 account-0 extended public key (from the BIP84 test vectors)
BIP84_ZPUB = ("zpub6rFR7y4Q2AijBEqTUquhVz398htDFrtymD9xYYfG1m4wAcvPhXNfE3EfH1r1"
              "ADqtfSdVCToUG868RvUUkgDKf31mGDtKsAYz2oz2AGutZYs")


def test_zpub_converts_to_xpub_without_touching_the_key():
    xpub, wrap = watch._to_xpub(BIP84_ZPUB)
    assert xpub.startswith("xpub")
    assert wrap == "wpkh({})"  # zpub -> native segwit
    # the only difference is the 4 version bytes: the 74-byte key body is identical
    assert watch._b58decode_check(xpub)[4:] == watch._b58decode_check(BIP84_ZPUB)[4:]
    assert watch._b58decode_check(xpub)[:4].hex() == "0488b21e"


def test_ypub_maps_to_p2sh_segwit():
    # re-encode the same key body under the ypub version to exercise that branch
    body = watch._b58decode_check(BIP84_ZPUB)[4:]
    ypub = watch._b58encode_check(bytes.fromhex("049d7cb2") + body)
    _, wrap = watch._to_xpub(ypub)
    assert wrap == "sh(wpkh({}))"


def test_unknown_version_rejected():
    try:
        watch._to_xpub("1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2")  # valid b58, not an extkey
        assert False
    except ValueError:
        pass


# --- descriptor building ---

def test_bare_zpub_builds_receive_and_change_wpkh():
    descs = watch.descriptors_for(BIP84_ZPUB)
    assert len(descs) == 2
    (recv, recv_internal), (change, change_internal) = descs
    assert recv.startswith("wpkh(") and recv.endswith("/0/*)") and recv_internal is False
    assert change.endswith("/1/*)") and change_internal is True


def test_bare_xpub_is_legacy_pkh():
    body = watch._b58decode_check(BIP84_ZPUB)[4:]
    xpub = watch._b58encode_check(bytes.fromhex("0488b21e") + body)
    recv = watch.descriptors_for(xpub)[0][0]
    assert recv.startswith("pkh(")


def test_full_descriptor_passthrough_strips_checksum():
    d = "wpkh([abcd1234/84h/0h/0h]xpub6C.../0/*)#aaaaaaaa"
    out = watch.descriptors_for(d)
    assert out == [("wpkh([abcd1234/84h/0h/0h]xpub6C.../0/*)", False)]


def test_multipath_descriptor_expands_to_two_branches():
    d = "wpkh([abcd1234/84h/0h/0h]xpub6C.../<0;1>/*)"
    out = watch.descriptors_for(d)
    assert out[0] == ("wpkh([abcd1234/84h/0h/0h]xpub6C.../0/*)", False)
    assert out[1] == ("wpkh([abcd1234/84h/0h/0h]xpub6C.../1/*)", True)


def test_bare_address_becomes_a_single_address_descriptor():
    # legacy, p2sh, and bech32 addresses all wrap into addr()
    for addr in ["1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
                 "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy",
                 "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"]:
        assert watch.descriptors_for(addr) == [(f"addr({addr})", False)]


def test_satoshi_genesis_pubkey_descriptor_passes_through():
    # Satoshi's genesis key has no xpub/zpub (pre-BIP32); its pubkey form is a
    # plain pkh() descriptor, which passes straight through (un-ranged).
    d = ("pkh(04678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61de"
         "b649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5f)")
    assert watch.descriptors_for(d) == [(d, False)]


def test_garbage_key_is_rejected():
    for bad in ["hello world", "not-a-key", "xpub-but-broken", "12345"]:
        try:
            watch.descriptors_for(bad)
            assert False, f"accepted garbage {bad!r}"
        except ValueError:
            pass


# --- small helpers ---

def test_wallet_name_is_namespaced_and_sanitized():
    assert watch.wallet_name("Cold Storage!") == "watch_Cold_Storage_"
    assert watch.wallet_name("a/b\\c").startswith("watch_")


def test_birthday_ts():
    assert watch.birthday_ts("2021-01-01") == 1609459200
    assert watch.birthday_ts("") == 0
    assert watch.birthday_ts("garbage") == 0


def test_fmt_btc_trims_zeros():
    assert watch.fmt_btc(0.5) == "0.5"
    assert watch.fmt_btc(1.0) == "1"
    assert watch.fmt_btc(0) == "0"
    assert watch.fmt_btc(1.23456789) == "1.23456789"


# --- config loading ---

def test_load_config_from_b64(monkeypatch):
    blob = [{"name": "Cold", "key": "zpub...", "birthday": "2021-01-01"},
            {"name": "", "key": "x"},          # dropped: no name
            {"name": "y", "key": ""}]          # dropped: no key
    monkeypatch.setenv("WATCH_WALLETS_B64", base64.b64encode(json.dumps(blob).encode()).decode())
    cfg = watch.load_config()
    assert len(cfg) == 1 and cfg[0]["name"] == "Cold"


def test_load_config_empty_and_malformed(monkeypatch):
    monkeypatch.delenv("WATCH_WALLETS_B64", raising=False)
    assert watch.load_config() == []
    monkeypatch.setenv("WATCH_WALLETS_B64", "not base64 json!!")
    assert watch.load_config() == []


# --- balances aggregation ---

class FakeWalletRpc:
    def __init__(self, table):
        self.table = table  # {wallet: {"getwalletinfo": ..., "getbalances": ...}}

    def __call__(self, wallet, method, params=None):
        return self.table.get(wallet, {}).get(method)


def test_balances_sums_only_ready_wallets():
    wallets = [{"name": "A", "key": "z"}, {"name": "B", "key": "z"}, {"name": "C", "key": "z"}]
    table = {
        "watch_A": {"getwalletinfo": {"scanning": False}, "getbalances": {"mine": {"trusted": 0.5}}},
        "watch_B": {"getwalletinfo": {"scanning": {"duration": 10}}},  # rescanning
        "watch_C": {"getwalletinfo": None},                            # error
    }
    rows, total = watch.balances(FakeWalletRpc(table), wallets)
    assert total == "0.5"  # only A counts
    assert rows[0] == {"name": "A", "state": "ok", "btc": "0.5", "key": "z"}
    assert rows[1]["state"] == "scanning"
    assert rows[2]["state"] == "error"
    assert all("key" in r for r in rows)  # every row carries its key for the UI


def test_balances_on_sample_fires_only_for_ready_wallets():
    # on_sample feeds balance_history — it must fire only for "ok" rows, keyed by
    # the wallet's KEY, with a numeric balance (never for scanning/error rows)
    wallets = [{"name": "A", "key": "keyA"}, {"name": "B", "key": "keyB"}, {"name": "C", "key": "keyC"}]
    table = {
        "watch_A": {"getwalletinfo": {"scanning": False}, "getbalances": {"mine": {"trusted": 1.25}}},
        "watch_B": {"getwalletinfo": {"scanning": {"duration": 5}}},  # scanning
        "watch_C": {"getwalletinfo": None},                           # error
    }
    samples = []
    watch.balances(FakeWalletRpc(table), wallets, on_sample=lambda k, btc: samples.append((k, btc)))
    assert samples == [("keyA", 1.25)]  # only the ok wallet, by key, numeric


def test_balances_view_show_total_only_when_more_than_one():
    one = [{"name": "A", "key": "z"}]
    two = [{"name": "A", "key": "z"}, {"name": "B", "key": "z"}]
    table = {
        "watch_A": {"getwalletinfo": {"scanning": False}, "getbalances": {"mine": {"trusted": 1}}},
        "watch_B": {"getwalletinfo": {"scanning": False}, "getbalances": {"mine": {"trusted": 2}}},
    }
    assert watch.balances_view(FakeWalletRpc(table), one)["show_total"] is False
    v = watch.balances_view(FakeWalletRpc(table), two)
    assert v["show_total"] is True and v["total"] == "3"


# --- persistent store + CRUD ---

def test_store_seeds_from_env_then_persists(monkeypatch, tmp_path):
    path = tmp_path / "wallets.json"
    monkeypatch.setenv("WATCH_STORE", str(path))
    blob = [{"name": "Seed", "key": "zpub123", "birthday": "2020-01-01"}]
    monkeypatch.setenv("WATCH_WALLETS_B64", base64.b64encode(json.dumps(blob).encode()).decode())
    store = watch.load_store()
    assert [w["name"] for w in store] == ["Seed"]
    assert path.exists()  # seed was written through
    # a second load reads the file, not the env (env authority ends after seed)
    monkeypatch.delenv("WATCH_WALLETS_B64", raising=False)
    assert [w["name"] for w in watch.load_store()] == ["Seed"]


def test_add_and_remove_entry_roundtrip(monkeypatch, tmp_path):
    monkeypatch.setenv("WATCH_STORE", str(tmp_path / "w.json"))
    store = []
    watch.add_entry(store, "Cold", BIP84_ZPUB, "2021-01-01")
    assert len(store) == 1 and store[0]["name"] == "Cold"
    # persisted and reloadable
    assert [w["name"] for w in watch.load_store()] == ["Cold"]
    assert watch.remove_entry(store, "Cold") is True
    assert store == [] and watch.load_store() == []
    assert watch.remove_entry(store, "Cold") is False  # already gone


def test_add_entry_rejects_bad_input(monkeypatch, tmp_path):
    monkeypatch.setenv("WATCH_STORE", str(tmp_path / "w.json"))
    store = []
    for bad in [("", BIP84_ZPUB, ""), ("A", "", ""), ("A", "not-a-key", ""),
                ("A", BIP84_ZPUB, "nonsense-date")]:
        try:
            watch.add_entry(store, *bad)
            assert False, f"accepted bad input {bad}"
        except ValueError:
            pass
    assert store == []  # nothing was saved


def test_add_entry_rejects_duplicate_and_cap(monkeypatch, tmp_path):
    monkeypatch.setenv("WATCH_STORE", str(tmp_path / "w.json"))
    store = []
    watch.add_entry(store, "Cold", BIP84_ZPUB)
    try:
        watch.add_entry(store, "cold", BIP84_ZPUB)  # case-insensitive dup
        assert False
    except ValueError:
        pass
    monkeypatch.setattr(watch, "MAX_WALLETS", 1)
    try:
        watch.add_entry(store, "Hot", BIP84_ZPUB)
        assert False
    except ValueError:
        pass


def test_save_store_survives_unwritable_path(monkeypatch):
    # a missing/unwritable volume must not raise — the dashboard keeps running
    monkeypatch.setenv("WATCH_STORE", "/nonexistent-dir-xyz/no/perms/w.json")
    watch.save_store([{"name": "A", "key": "z", "birthday": ""}])  # no exception


# --- wallet provisioning ---

class FakeRpc:
    def __init__(self, existing=(), descriptor_ok=True):
        self.existing = list(existing)
        self.descriptor_ok = descriptor_ok
        self.calls = []

    def __call__(self, method, params=None):
        self.calls.append((method, params))
        if method == "listwallets":
            return self.existing
        if method == "createwallet":
            return {"name": params[0]}
        if method == "getdescriptorinfo":
            # ranged iff the descriptor derives (…/*), like a real node reports
            return ({"descriptor": params[0] + "#ck", "isrange": "*" in params[0]}
                    if self.descriptor_ok else None)
        return None


def test_ensure_creates_and_imports_new_wallet():
    rpc = FakeRpc()
    imports = []
    wallet_rpc = lambda w, m, p=None: (
        None if m == "getwalletinfo" else imports.append((w, m, p)))
    watch.ensure_wallets(rpc, wallet_rpc, [{"name": "Cold", "key": BIP84_ZPUB, "birthday": "2021-01-01"}])
    methods = [c[0] for c in rpc.calls]
    assert "createwallet" in methods
    assert imports and imports[0][1] == "importdescriptors"
    reqs = imports[0][2][0]
    assert len(reqs) == 2 and all(r["timestamp"] == 1609459200 for r in reqs)
    assert [r["internal"] for r in reqs] == [False, True]


def test_import_sends_range_only_for_ranged_descriptors():
    # a ranged xpub gets range:1000; a single-address addr() must NOT (Core
    # rejects a range on an un-ranged descriptor)
    def run(key):
        rpc = FakeRpc()
        captured = []
        wallet_rpc = lambda w, m, p=None: (
            None if m == "getwalletinfo" else captured.append(p[0]))
        watch.provision_one(rpc, wallet_rpc, {"name": "W", "key": key})
        return captured[0]  # the importdescriptors request list

    ranged = run(BIP84_ZPUB)
    assert all("range" in r for r in ranged)
    addr = run("1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa")
    assert len(addr) == 1 and "range" not in addr[0]
    assert addr[0]["desc"].startswith("addr(1A1zP1eP5")


def test_ensure_skips_already_loaded_wallet():
    rpc = FakeRpc()
    # getwalletinfo returns a dict -> already loaded -> provision returns at once
    watch.ensure_wallets(rpc, lambda w, m, p=None: {"scanning": False},
                         [{"name": "Cold", "key": BIP84_ZPUB}])
    assert rpc.calls == []  # nothing created or loaded


def test_ensure_loads_existing_wallet_without_reimport():
    # a removed-then-re-added wallet exists on disk: loadwallet succeeds, so we
    # never re-create or re-import (no second rescan)
    rpc = FakeRpc()
    rpc.existing = []  # listwallets unused now

    def rpc_with_load(method, params=None):
        rpc.calls.append((method, params))
        if method == "loadwallet":
            return {"name": params[0]}      # exists on disk -> loads
        return None
    watch.ensure_wallets(rpc_with_load, lambda w, m, p=None: None,
                         [{"name": "Cold", "key": BIP84_ZPUB}])
    methods = [c[0] for c in rpc.calls]
    assert "loadwallet" in methods and "createwallet" not in methods


def test_ensure_does_not_import_on_pruned_node():
    rpc = FakeRpc()
    watch.ensure_wallets(rpc, lambda w, m, p=None: None,
                         [{"name": "Cold", "key": BIP84_ZPUB}], pruned=True)
    methods = [c[0] for c in rpc.calls]
    assert "createwallet" not in methods  # no fresh import while pruned


def test_ensure_skips_bad_key_without_crashing_or_orphaning():
    # a typo'd key must not create an orphan wallet, kill the thread, or block
    # the good wallets that follow it
    rpc = FakeRpc()
    imports = []
    wallet_rpc = lambda w, m, p=None: (
        None if m == "getwalletinfo" else imports.append((w, m, p)))
    watch.ensure_wallets(rpc, wallet_rpc, [
        {"name": "Bad", "key": "not-a-valid-key"},
        {"name": "Good", "key": BIP84_ZPUB},
    ])
    created = [p[0] for m, p in rpc.calls if m == "createwallet"]
    assert created == ["watch_Good"]          # bad one skipped, no orphan
    assert imports and imports[0][0] == "watch_Good"  # good one still imported

    # The createwallet call must disable private keys (arg index 1) — this is
    # the "watch-only, cannot spend" guarantee. Pin it here, not only in the e2e.
    cw_params = next(p for m, p in rpc.calls if m == "createwallet")
    assert cw_params[1] is True, "watch-only wallet must be created with disable_private_keys=True"
