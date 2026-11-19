#!/usr/bin/env python3
"""Rebuilds the Bitcoin Cash Node regtest chainstate fixture; each run mines a new chain, so the tests check the sidecar json."""
import argparse
import base64
import json
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
import uuid
from decimal import Decimal
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "build" / "dashboard" / "testdata"
IMAGE = "ghcr.io/vijitsingh97/node-manager-bitcoincash:1.34.0"
RPC_USER, RPC_PASSWORD = "fixture", "fixture"
G_COMPRESSED = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
G_UNCOMPRESSED = (
    "0479be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
    "483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8"
)
SATS = Decimal(100000000)


class Node:
    def __init__(self, image, prune):
        self.name = "bch-fixture-" + uuid.uuid4().hex[:8]
        subprocess.run(
            ["docker", "run", "-d", "--name", self.name, "-p", "127.0.0.1::18443", "--entrypoint", "bitcoind", image,
             "-regtest", "-datadir=/data", "-server=1", "-listen=0", "-dnsseed=0", "-acceptnonstdtxn=1",
             "-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0", f"-rpcuser={RPC_USER}", f"-rpcpassword={RPC_PASSWORD}",
             f"-prune={prune}"],
            check=True, capture_output=True,
        )
        port = subprocess.run(["docker", "port", self.name, "18443/tcp"], check=True, capture_output=True, text=True)
        self.url = "http://127.0.0.1:" + port.stdout.split()[0].rsplit(":", 1)[1]
        self.auth = "Basic " + base64.b64encode(f"{RPC_USER}:{RPC_PASSWORD}".encode()).decode()
        for _ in range(60):
            try:
                self.rpc("getblockchaininfo")
                return
            except (OSError, RuntimeError):
                time.sleep(1)
        self.close()
        raise RuntimeError("the node did not answer")

    def post(self, body, wallet=None):
        req = urllib.request.Request(
            self.url + ("/wallet/" + wallet if wallet else ""), json.dumps(body).encode(),
            {"Authorization": self.auth, "Content-Type": "application/json"},
        )
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                return json.loads(resp.read(), parse_float=Decimal)
        except urllib.error.HTTPError as err:
            return json.loads(err.read(), parse_float=Decimal)

    def rpc(self, method, *params, wallet=None):
        out = self.post({"method": method, "params": list(params), "id": 1}, wallet)
        if out.get("error"):
            raise RuntimeError(f"{method}: {out['error']}")
        return out["result"]

    def batch(self, method, calls, wallet=None):
        out = self.post([{"method": method, "params": list(p), "id": i} for i, p in enumerate(calls)], wallet)
        return [r["result"] for r in sorted(out, key=lambda r: r["id"])]

    def wallet(self, method, *params):
        return self.rpc(method, *params, wallet="w")

    def close(self):
        subprocess.run(["docker", "rm", "-f", "-v", self.name], check=False, capture_output=True)


def varint(b, i):
    n = b[i]
    if n < 0xFD:
        return n, i + 1
    size = {0xFD: 2, 0xFE: 4, 0xFF: 8}[n]
    return int.from_bytes(b[i + 1:i + 1 + size], "little"), i + 1 + size


def compact(n):
    if n < 0xFD:
        return bytes([n])
    return b"\xfd" + n.to_bytes(2, "little")


def patch_scripts(raw, scripts):
    """Swaps the scriptPubKey of the given outputs in an unsigned transaction, as createrawtransaction only writes address scripts."""
    b = bytes.fromhex(raw)
    i = 4
    nin, i = varint(b, i)
    for _ in range(nin):
        slen, j = varint(b, i + 36)
        i = j + slen + 4
    nout, i = varint(b, i)
    out = b[:i]
    for vout in range(nout):
        value = b[i:i + 8]
        slen, j = varint(b, i + 8)
        script = b[j:j + slen]
        out += value + compact(len(scripts.get(vout, script))) + scripts.get(vout, script)
        i = j + slen
    return (out + b[i:]).hex()


def send(node, inputs, outputs, scripts=None):
    raw = node.rpc("createrawtransaction", inputs, outputs)
    if scripts:
        raw = patch_scripts(raw, {k: bytes.fromhex(v) for k, v in scripts.items()})
    signed = node.wallet("signrawtransactionwithwallet", raw)
    if not signed["complete"]:
        raise RuntimeError("the transaction did not sign")
    return node.rpc("sendrawtransaction", signed["hex"])


def amount(sats):
    return format(Decimal(sats) / SATS, ".8f")


def build(node):
    node.rpc("createwallet", "w")
    addr = node.wallet("getnewaddress")
    p2sh = node.rpc("createmultisig", 1, [node.wallet("getaddressinfo", addr)["pubkey"]])["address"]
    fresh = iter(node.batch("getnewaddress", [()] * 400, wallet="w"))
    node.rpc("generatetoaddress", 110, addr)
    node.rpc("generatetoaddress", 3, p2sh)
    coinbases = [u for u in node.wallet("listunspent", 101) if u["amount"] > 0]
    coinbases.sort(key=lambda u: (u["txid"], u["vout"]))
    inputs = iter(coinbases)

    def spend(outs, scripts=None, ins=None):
        utxo = next(inputs)
        change = utxo["amount"] - sum(Decimal(next(iter(o.values()))) if not isinstance(next(iter(o.values())), dict)
                                       else Decimal(next(iter(o.values()))["amount"]) for o in outs) - Decimal("0.01")
        txid = send(node, [{"txid": utxo["txid"], "vout": utxo["vout"]}], outs + [{next(fresh): format(change, ".8f")}], scripts)
        return txid

    edges = [0, 1, 9, 10, 546, 1234567, 99999999, 100000000, 3999999999]
    outs = [{next(fresh): amount(10000000)}, {p2sh: amount(20000000)}]
    outs += [{next(fresh): amount(s)} for s in edges]
    scripts = {2 + len(edges): "6a0b6e6f7420756e7370656e64", 3 + len(edges): "51"}
    outs += [{next(fresh): amount(30000 + i)} for i in range(5)]
    first = 2 + len(edges) + 2
    scripts.update({
        first: "21" + G_COMPRESSED + "ac",
        first + 1: "41" + G_UNCOMPRESSED + "ac",
        first + 2: "aa20" + "ab" * 32 + "87",
        first + 3: "00" * 10001,
        first + 4: "6a" + "11" * 40,
    })
    tx1 = spend(outs, scripts)

    # Output 0 of this transaction is the token genesis input and is spent below.
    genesis = spend([{next(fresh): amount(50000000)}])
    node.rpc("generatetoaddress", 1, addr)
    wide = [{next(fresh): amount(1000 + 7 * i)} for i in range(301)]
    wide_tx = spend(wide)
    node.rpc("generatetoaddress", 1, addr)

    category = genesis
    tokens = [
        {"category": category, "amount": 1},
        {"category": category, "amount": 252},
        {"category": category, "amount": 253},
        {"category": category, "amount": 65535},
        {"category": category, "amount": 65536},
        {"category": category, "amount": 4294967295},
        {"category": category, "amount": 4294967296},
        {"category": category, "nft": {"capability": "none"}},
        {"category": category, "nft": {"capability": "none", "commitment": "ab"}},
        {"category": category, "nft": {"capability": "minting", "commitment": "cd"}},
        {"category": category, "amount": 77, "nft": {"capability": "mutable", "commitment": "ef" * 40}},
        {"category": category, "amount": 5, "nft": {"capability": "minting"}},
    ]
    token_outs = [{next(fresh): {"amount": amount(100000), "tokenData": t}} for t in tokens]
    token_outs.append({p2sh: {"amount": amount(100000), "tokenData": {"category": category, "amount": 9}}})
    tx = node.rpc("createrawtransaction", [{"txid": genesis, "vout": 0}], token_outs + [{next(fresh): amount(48600000)}])
    signed = node.wallet("signrawtransactionwithwallet", tx)
    token_tx = node.rpc("sendrawtransaction", signed["hex"])

    spent_parent = spend([{next(fresh): amount(70000)}, {next(fresh): amount(80000)}])
    send(node, [{"txid": spent_parent, "vout": 0}], [{next(fresh): amount(60000)}])
    node.rpc("generatetoaddress", 2, addr)
    return tx1, wide_tx, token_tx, genesis


def candidates(node):
    height = node.rpc("getblockcount")
    hashes = node.batch("getblockhash", [(h,) for h in range(height + 1)])
    txs = []
    for block in node.batch("getblock", [(h, 2) for h in hashes]):
        txs += block["tx"]
    return height, [(t["txid"], len(t["vout"])) for t in txs]


def export(node):
    height, txs = candidates(node)
    points = [(txid, vout) for txid, n in txs for vout in range(n)]
    coins = node.batch("gettxout", [(txid, vout, False) for txid, vout in points])
    info = node.rpc("gettxoutsetinfo")
    outputs = []
    for (txid, vout), coin in zip(points, coins):
        if coin is None:
            continue
        entry = {
            "txid": txid, "vout": vout, "value": int(coin["value"] * SATS), "script": coin["scriptPubKey"]["hex"],
            "height": height - coin["confirmations"] + 1, "coinbase": coin["coinbase"],
        }
        if "tokenData" in coin:
            entry["tokenData"] = coin["tokenData"]
        outputs.append(entry)
    return {
        "height": height, "best_block": info["bestblock"], "txouts": info["txouts"],
        "total_amount_sats": int(info["total_amount"] * SATS), "outputs": outputs,
    }


def default(o):
    if isinstance(o, Decimal):
        return int(o) if o == o.to_integral_value() else str(o)
    raise TypeError(type(o))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", default=IMAGE)
    parser.add_argument("--prune", type=int, default=1, choices=[0, 1])
    parser.add_argument("--out", default="bitcoincash-chainstate-regtest")
    args = parser.parse_args()
    dest = FIXTURES / args.out
    node = Node(args.image, args.prune)
    try:
        build(node)
        expected = export(node)
        expected["pruned"] = args.prune != 0
        node.rpc("stop")
        subprocess.run(["docker", "wait", node.name], check=True, capture_output=True)
        with tempfile.TemporaryDirectory() as tmp:
            subprocess.run(["docker", "cp", f"{node.name}:/data/regtest/chainstate/.", tmp], check=True)
            if dest.exists():
                shutil.rmtree(dest)
            dest.mkdir(parents=True)
            for f in sorted(Path(tmp).iterdir()):
                if f.name in ("LOCK", "LOG", "LOG.old"):
                    continue
                target = dest / (f.stem + ".sst" if f.suffix == ".ldb" else f.name)
                shutil.copy(f, target)
                target.chmod(0o644)
    finally:
        node.close()
    (FIXTURES / (args.out + ".json")).write_text(json.dumps(expected, indent=1, default=default) + "\n")
    print(f"wrote {dest} with {expected['txouts']} coins at height {expected['height']}")


if __name__ == "__main__":
    sys.exit(main())
