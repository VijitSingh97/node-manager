#!/usr/bin/env python3
import argparse
import base64
import contextlib
import hashlib
import io
import json
import os
import sys
import tempfile
from pathlib import Path

REFERENCE = Path(__file__).resolve().parent / "reference"
sys.path.insert(0, str(REFERENCE))

import balance_history
import watch

BIP84_ZPUB = (
    "zpub6rFR7y4Q2AijBEqTUquhVz398htDFrtymD9xYYfG1m4wAcvPhXNfE3EfH1r1"
    "ADqtfSdVCToUG868RvUUkgDKf31mGDtKsAYz2oz2AGutZYs"
)
VERSIONS = [
    ("xpub", "0488b21e"),
    ("ypub", "049d7cb2"),
    ("zpub", "04b24746"),
    ("tpub", "043587cf"),
    ("upub", "044a5262"),
    ("vpub", "045f1cf6"),
]


def caught(call):
    try:
        return {"value": call()}
    except Exception as exc:
        return {"error": str(exc)}


def slip132_vectors():
    body = watch._b58decode_check(BIP84_ZPUB)[4:]
    vectors = []
    for prefix, version in VERSIONS:
        key = watch._b58encode_check(bytes.fromhex(version) + body)
        normalized, wrapper = watch._to_xpub(key)
        vectors.append(
            {
                "prefix": prefix,
                "input": key,
                "normalized": normalized,
                "wrapper": wrapper,
                "descriptors": [
                    {"desc": desc, "internal": internal}
                    for desc, internal in watch.descriptors_for(key)
                ],
            }
        )
    bad = BIP84_ZPUB[:-1] + ("1" if BIP84_ZPUB[-1] != "1" else "2")
    return {
        "versions": vectors,
        "bad_checksum": {"input": bad, **caught(lambda: watch._to_xpub(bad))},
        "unknown_version": {
            "input": "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2",
            **caught(lambda: watch._to_xpub("1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2")),
        },
    }


def descriptor_vectors():
    keys = [
        "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
        "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy",
        "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4",
        "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx",
        "bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080",
        "wpkh([abcd1234/84h/0h/0h]xpub6C.../0/*)#aaaaaaaa",
        "wpkh([abcd1234/84h/0h/0h]xpub6C.../<0;1>/*)#aaaaaaaa",
    ]
    return [
        {
            "input": key,
            "descriptors": [
                {"desc": desc, "internal": internal}
                for desc, internal in watch.descriptors_for(key)
            ],
        }
        for key in keys
    ]


def helper_vectors():
    names = [
        "Cold Storage!",
        "a/b\\c",
        "München 冷蔵庫 🔐",
        "四十字を超える名前" * 6,
    ]
    birthdays = ["2021-01-01", "2009-01-03", "", "garbage", "2021-02-29"]
    btc = [0, 1, 0.5, 1.23456789, 1.234567895, -0.00000001]
    return {
        "wallet_names": [{"input": name, "output": watch.wallet_name(name)} for name in names],
        "birthdays": [{"input": value, "timestamp": watch.birthday_ts(value)} for value in birthdays],
        "btc_format": [{"input": value, "output": watch.fmt_btc(value)} for value in btc],
    }


class ProvisionRPC:
    def __init__(self, mode):
        self.mode = mode
        self.calls = []

    def __call__(self, method, params=None):
        self.calls.append({"method": method, "params": params or []})
        if method == "loadwallet":
            return {"name": params[0]} if self.mode == "readd" else None
        if method == "createwallet":
            return None if self.mode == "create_failure" else {"name": params[0]}
        if method == "getdescriptorinfo":
            if self.mode == "invalid_descriptor":
                return None
            return {
                "descriptor": params[0] + "#oracle",
                "isrange": "*" in params[0],
            }
        return None


def provision_case(mode, key=BIP84_ZPUB, pruned=False):
    rpc = ProvisionRPC(mode)
    wallet_calls = []

    def wallet_rpc(wallet, method, params=None):
        wallet_calls.append({"wallet": wallet, "method": method, "params": params or []})
        if method == "getwalletinfo" and mode == "loaded":
            return {"scanning": False}
        return None

    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        result = caught(
            lambda: watch.provision_one(
                rpc,
                wallet_rpc,
                {"name": "Cold", "key": key, "birthday": "2021-01-01"},
                pruned=pruned,
            )
        )
    return {
        **result,
        "node_calls": rpc.calls,
        "wallet_calls": wallet_calls,
        "stdout": output.getvalue().splitlines(),
    }


def bad_key_sequence():
    rpc = ProvisionRPC("fresh")
    wallet_calls = []

    def wallet_rpc(wallet, method, params=None):
        wallet_calls.append({"wallet": wallet, "method": method, "params": params or []})

    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        watch.ensure_wallets(
            rpc,
            wallet_rpc,
            [
                {"name": "Bad", "key": "not-a-valid-key"},
                {"name": "Good", "key": BIP84_ZPUB},
            ],
        )
    return {
        "node_calls": rpc.calls,
        "wallet_calls": wallet_calls,
        "stdout": output.getvalue().splitlines(),
    }


def provision_vectors():
    return {
        "loaded": provision_case("loaded"),
        "readd": provision_case("readd"),
        "pruned": provision_case("fresh", pruned=True),
        "fresh_watch_only": provision_case("fresh"),
        "bad_key_then_good": bad_key_sequence(),
        "create_failure": provision_case("create_failure"),
        "invalid_descriptors": provision_case("invalid_descriptor"),
    }


class BalanceRPC:
    def __init__(self, table):
        self.table = table

    def __call__(self, wallet, method, params=None):
        return self.table.get(wallet, {}).get(method)


def balance_vectors():
    wallets = [
        {"name": "Ready", "key": "ready-key"},
        {"name": "Scanning", "key": "scanning-key"},
        {"name": "Offline", "key": "offline-key"},
        {"name": "Empty", "key": "empty-key"},
    ]
    table = {
        "watch_Ready": {
            "getwalletinfo": {"scanning": False},
            "getbalances": {"mine": {"trusted": 1.25}},
        },
        "watch_Scanning": {"getwalletinfo": {"scanning": {"duration": 5}}},
        "watch_Offline": {"getwalletinfo": None},
        "watch_Empty": {"getwalletinfo": {"scanning": False}, "getbalances": None},
    }
    samples = []
    view = watch.balances_view(
        BalanceRPC(table), wallets, on_sample=lambda key, btc: samples.append([key, btc])
    )
    stale = dict(view["wallets"][2])
    stale_history = [0.75, 1.25]
    if stale["state"] == "error" and stale_history:
        stale["state"] = "stale"
        stale["btc"] = watch.fmt_btc(stale_history[-1])
    stale["history"] = stale_history
    return {"view": view, "samples": samples, "stale_api_row": stale}


@contextlib.contextmanager
def environment(**changes):
    previous = {key: os.environ.get(key) for key in changes}
    try:
        for key, value in changes.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value
        yield
    finally:
        for key, value in previous.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value


def config_vectors():
    items = [
        {"name": " 冷蔵庫 ", "key": f" {BIP84_ZPUB} ", "birthday": "2021-01-01"},
        {"name": "", "key": "x"},
        {"name": "missing-key", "key": ""},
    ]
    encoded = base64.b64encode(json.dumps(items, ensure_ascii=False).encode()).decode()
    with environment(WATCH_WALLETS_B64=encoded):
        valid = watch.load_config()
    malformed = []
    for value in ["not base64 json!!", base64.b64encode(b"{}").decode(), None]:
        with environment(WATCH_WALLETS_B64=value):
            malformed.append({"input": value, "output": watch.load_config()})
    return {"encoded": encoded, "output": valid, "malformed": malformed}


def store_vectors():
    with tempfile.TemporaryDirectory() as temp:
        path = str(Path(temp) / "wallets.json")
        seed = [{"name": " Seed ", "key": f" {BIP84_ZPUB} ", "birthday": None}]
        encoded = base64.b64encode(json.dumps(seed).encode()).decode()
        with environment(WATCH_STORE=path, WATCH_WALLETS_B64=encoded):
            seeded = watch.load_store()
            os.environ.pop("WATCH_WALLETS_B64", None)
            authoritative = watch.load_store()
            added = watch.add_entry(authoritative, "Cold", BIP84_ZPUB, "2021-01-01")
            duplicate = caught(lambda: watch.add_entry(authoritative, "cold", BIP84_ZPUB))
            invalid_date = caught(
                lambda: watch.add_entry(authoritative, "Hot", BIP84_ZPUB, "2021-02-29")
            )
            old_max = watch.MAX_WALLETS
            watch.MAX_WALLETS = len(authoritative)
            try:
                capped = caught(lambda: watch.add_entry(authoritative, "Hot", BIP84_ZPUB))
            finally:
                watch.MAX_WALLETS = old_max
            removed = watch.remove_entry(authoritative, "Cold")
            missing = watch.remove_entry(authoritative, "Cold")
            reloaded = watch.load_store()
        return {
            "seeded": seeded,
            "authoritative": authoritative,
            "added": added,
            "duplicate": duplicate,
            "invalid_date": invalid_date,
            "capped": capped,
            "removed": removed,
            "missing": missing,
            "reloaded": reloaded,
        }


def history_vectors():
    old_max = balance_history.MAX_POINTS
    with tempfile.TemporaryDirectory() as temp:
        path = str(Path(temp) / "history.json")
        with environment(BALANCE_HISTORY=path):
            balance_history._data = None
            balance_history.MAX_POINTS = 3
            try:
                for now, btc in [(0, 1), (1000, 9), (3600, 2), (7200, 3), (10800, 4)]:
                    balance_history.record(" xpub-AAA ", btc, now=now)
                balance_history.record("xpub-BBB", 9, now=0)
                balance_history.record("xpub-AAA", "ignored", now=14400)
                before_restart = balance_history.series("xpub-AAA")
                balance_history._data = None
                after_restart = balance_history.series("xpub-AAA")
                other = balance_history.series("xpub-BBB")
                persisted = json.loads(Path(path).read_text())
            finally:
                balance_history.MAX_POINTS = old_max
                balance_history._data = None
    keys = ["xpub-AAA", " xpub-AAA ", "xpub-BBB", ""]
    return {
        "key_ids": [{"input": key, "output": balance_history.key_id(key)} for key in keys],
        "series_before_restart": before_restart,
        "series_after_restart": after_restart,
        "other_series": other,
        "persisted": persisted,
        "min_interval": balance_history.MIN_INTERVAL,
        "cap_used": 3,
    }


def oracle():
    return {
        "schema": 1,
        "python_sources": {
            name: hashlib.sha256((REFERENCE / name).read_bytes()).hexdigest()
            for name in ["watch.py", "balance_history.py"]
        },
        "slip132": slip132_vectors(),
        "descriptors": descriptor_vectors(),
        "helpers": helper_vectors(),
        "provisioning": provision_vectors(),
        "balances": balance_vectors(),
        "config": config_vectors(),
        "store": store_vectors(),
        "history": history_vectors(),
    }


def encoded_oracle():
    return json.dumps(oracle(), ensure_ascii=False, indent=2, sort_keys=True) + "\n"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--write", action="store_true")
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    if args.write and args.check:
        parser.error("choose only one of --write and --check")
    target = Path(__file__).with_name("oracle.json")
    generated = encoded_oracle()
    if args.write:
        target.write_text(generated, encoding="utf-8")
        return
    if args.check:
        if target.read_text(encoding="utf-8") != generated:
            raise SystemExit("oracle.json differs from the Python oracle; run generate.py --write")
        print("oracle.json matches the unchanged Python modules")
        return
    print(generated, end="")


if __name__ == "__main__":
    main()
