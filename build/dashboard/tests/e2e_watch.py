#!/usr/bin/env python3
import base64
import hashlib
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

DASHBOARD = os.environ.get("E2E_DASHBOARD_URL", "http://localhost:80")
DASHBOARD_PASSWORD = os.environ["E2E_DASHBOARD_PASSWORD"]
RPC_USER = os.environ["BITCOIN_RPC_USER"]
RPC_PASSWORD = os.environ["BITCOIN_RPC_PASSWORD"]
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))

GENESIS_ADDR = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
GENESIS_PUBKEY = (
    "04678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f6"
    "1deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5f"
)
XPUB = (
    "xpub6CatWdiZiodmUeTDp8LT5or8nmbKNcuyvz7WyksVFkKB4RHwCD3XyuvPEbvqAQY3"
    "rAPshWcMLoP2fMFMKHPJ4ZeZXYVUhLv1VMrjPC7PW6V"
)
ZPUB = (
    "zpub6rFR7y4Q2AijBEqTUquhVz398htDFrtymD9xYYfG1m4wAcvPhXNfE3EfH1r1ADqt"
    "fSdVCToUG868RvUUkgDKf31mGDtKsAYz2oz2AGutZYs"
)
CASES = [
    ("xpub", XPUB),
    ("zpub", ZPUB),
    ("Genesis", GENESIS_ADDR),
    ("satoshi-pubkey", f"pkh({GENESIS_PUBKEY})"),
]


def dashboard(method, path, body=None):
    headers = {
        "Authorization": "Basic "
        + base64.b64encode(f"admin:{DASHBOARD_PASSWORD}".encode()).decode(),
        "X-Requested-With": "fetch",
    }
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(DASHBOARD + path, data=data, headers=headers, method=method)
    try:
        response = OPENER.open(request, timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        return response.status, response.read().decode()


def bitcoin_cli(method, params=None, wallet=None):
    command = [
        "docker",
        "compose",
        "exec",
        "-T",
        "bitcoin",
        "bitcoin-cli",
        f"-rpcuser={RPC_USER}",
        f"-rpcpassword={RPC_PASSWORD}",
    ]
    if wallet:
        command.append(f"-rpcwallet={wallet}")
    command.append(method)
    command.extend(params or [])
    result = subprocess.run(command, check=True, capture_output=True, text=True, timeout=120)
    return json.loads(result.stdout)


def core_wallet_name(key):
    return "watch_" + hashlib.sha256(key.strip().encode()).hexdigest()


def wait_for_wallet(key):
    name = core_wallet_name(key)
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        try:
            info = bitcoin_cli("getwalletinfo", wallet=name)
            imported = bitcoin_cli("listdescriptors", wallet=name)
            if imported.get("descriptors"):
                return name, info
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, json.JSONDecodeError):
            pass
        time.sleep(1)
    raise RuntimeError(f"wallet {name} was not provisioned")


failures = []
status, body = dashboard("GET", "/api/watch")
wallets = json.loads(body).get("wallets", []) if status == 200 else []
if (len(wallets) != 1 or wallets[0].get("name") != "Binance (public)"
        or wallets[0].get("key") != "34xp4vRoCGJym3xR7yCVPFHoCNxv4Twseo"
        or wallets[0].get("example") is not True):
    failures.append("fresh install is missing the marked Binance public example")
elif dashboard("DELETE", "/api/watch/Binance%20%28public%29")[0] != 200:
    failures.append("public example could not be removed")
for label, key in CASES:
    status, body = dashboard("POST", "/api/watch", {"name": label, "key": key, "birthday": ""})
    if status != 200:
        failures.append(f"{label}: dashboard returned HTTP {status}: {body}")
        continue
    try:
        wallet, info = wait_for_wallet(key)
    except RuntimeError as error:
        failures.append(f"{label}: {error}")
        continue
    if not info.get("descriptors") or info.get("private_keys_enabled"):
        failures.append(f"{label}: wallet is not watch-only descriptor wallet: {info}")
    else:
        print(f"  OK {label}: {wallet} provisioned through the Go API")

for label, key in CASES[-2:]:
    wallet = core_wallet_name(key)
    try:
        address = bitcoin_cli("getaddressinfo", [GENESIS_ADDR], wallet=wallet)
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired, json.JSONDecodeError) as error:
        failures.append(f"{label}: getaddressinfo failed: {error}")
        continue
    if not address.get("ismine"):
        failures.append(f"{label}: genesis address is not watched")
    else:
        print(f"  OK {label}: watches {GENESIS_ADDR}")

try:
    legacy = "watch_reuse"
    bitcoin_cli("createwallet", [legacy, "true", "true", "", "false", "true", "true"])
    descriptor = bitcoin_cli("getdescriptorinfo", [f"addr({GENESIS_ADDR})"])["descriptor"]
    bitcoin_cli("importdescriptors", [json.dumps([{"desc": descriptor, "timestamp": 0}])], wallet=legacy)
    native = f"wpkh({XPUB}/<0;1>/*)"
    status, body = dashboard("POST", "/api/watch", {"name": "reuse", "key": native, "birthday": ""})
    if status != 200:
        raise RuntimeError(f"reused label returned HTTP {status}: {body}")
    wallet, _ = wait_for_wallet(native)
    descriptors = bitcoin_cli("listdescriptors", wallet=wallet)["descriptors"]
    if len(descriptors) != 2 or not all(item["desc"].startswith("wpkh(") for item in descriptors):
        raise RuntimeError("reused label did not import both native SegWit branches")
    if not bitcoin_cli("getaddressinfo", [GENESIS_ADDR], wallet=legacy).get("ismine"):
        raise RuntimeError("mismatched legacy wallet was changed")
    if dashboard("POST", "/api/watch", {"name": "duplicate", "key": native})[0] != 400:
        raise RuntimeError("duplicate key under another label was accepted")
    print("  OK reused label: imports the new key and preserves the old Core wallet")
except (RuntimeError, subprocess.CalledProcessError, subprocess.TimeoutExpired, json.JSONDecodeError) as error:
    failures.append(f"reused label: {error}")

if failures:
    print("FAIL: watch-only provisioning\n  " + "\n  ".join(failures))
    sys.exit(1)
print("PASS: Go watch-only provisioning for xpub / zpub / address / pubkey")
