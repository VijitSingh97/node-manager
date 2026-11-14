#!/usr/bin/env python3
"""Generate dumptxoutset fixtures and expected UTXO lists from the pinned regtest bitcoind.

Usage: tests/utxo_regtest.py OUTDIR

Writes OUTDIR/<scenario>.dat (the dumptxoutset file) and OUTDIR/<scenario>.json (the expected
UTXO list, derived by walking the chain over RPC rather than from the dump) for:
all_types, same_block, reorg1_before, reorg1_after, reorgn_before, reorgn_after.
"""
import hashlib
import json
import os
import re
import subprocess
import sys
import time
import uuid
from decimal import Decimal

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GENESIS_X = bytes.fromhex("678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb6")
GENESIS_Y = bytes.fromhex("49f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5f")
P = 2**256 - 2**32 - 977


def pinned_image():
    with open(os.path.join(ROOT, "docker-compose.yml")) as f:
        return re.search(r"image: (bitcoin/bitcoin:\S+)", f.read()).group(1)


class Node:
    def __init__(self):
        self.name = f"utxo-regtest-{uuid.uuid4().hex[:8]}"
        self.blocks = {}

    def start(self):
        subprocess.run(
            ["docker", "run", "-d", "--rm", "--name", self.name, pinned_image(), "bitcoind", "-regtest",
             "-acceptnonstdtxn=1", "-fallbackfee=0.0001", "-printtoconsole=0"],
            check=True, stdout=subprocess.DEVNULL)
        for _ in range(60):
            if self.cli("getblockcount", check=False) is not None:
                return
            time.sleep(1)
        raise RuntimeError("bitcoind did not start")

    def stop(self):
        subprocess.run(["docker", "rm", "-f", self.name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)

    def cli(self, *args, check=True):
        args = [a if isinstance(a, str) else json.dumps(a) for a in args]
        out = subprocess.run(["docker", "exec", "-u", "bitcoin", self.name, "bitcoin-cli", "-regtest"] + args,
                             capture_output=True, text=True, check=False)
        if out.returncode != 0:
            if check:
                raise RuntimeError("bitcoin-cli {}: {}".format(" ".join(args), out.stderr.strip()))
            return None
        text = out.stdout.strip()
        try:
            return json.loads(text, parse_float=Decimal)
        except ValueError:
            return text

    def dump(self, outdir, name):
        path = f"/tmp/{name}.dat"
        self.cli("dumptxoutset", path, "latest")
        subprocess.run(["docker", "cp", f"{self.name}:{path}", os.path.join(outdir, name + ".dat")], check=True)
        coins = self.expected_utxos()
        tip = self.cli("getbestblockhash")
        with open(os.path.join(outdir, name + ".json"), "w") as f:
            json.dump({"network": "regtest", "base_hash": tip, "base_height": self.cli("getblockcount"),
                       "coins": coins}, f, indent=1, sort_keys=True)
            f.write("\n")
        return coins

    def expected_utxos(self):
        unspent = {}
        for height in range(1, self.cli("getblockcount") + 1):
            block_hash = self.cli("getblockhash", str(height))
            if block_hash not in self.blocks:
                self.blocks[block_hash] = self.cli("getblock", block_hash, "2")
            block = self.blocks[block_hash]
            for tx in block["tx"]:
                coinbase = "coinbase" in tx["vin"][0]
                for vin in tx["vin"]:
                    if "txid" in vin:
                        del unspent[(vin["txid"], vin["vout"])]
                for out in tx["vout"]:
                    script = out["scriptPubKey"]["hex"]
                    if script.startswith("6a") or len(script) > 20000:
                        continue
                    unspent[(tx["txid"], out["n"])] = {
                        "txid": tx["txid"], "vout": out["n"], "amount": int(out["value"] * 100000000),
                        "height": height, "coinbase": coinbase, "script": script}
        return sorted(unspent.values(), key=lambda c: (c["txid"], c["vout"]))


def push(data):
    return bytes([len(data)]) + data


def h160(seed):
    return hashlib.sha256(seed).digest()[:20]


def y_for(x, odd):
    y = pow((pow(x, 3, P) + 7) % P, (P + 1) // 4, P)
    return y if (y & 1) == odd else P - y


def scripts():
    xa = int.from_bytes(hashlib.sha256(b"p2pk-even").digest(), "big") % P
    while pow((pow(xa, 3, P) + 7) % P, (P - 1) // 2, P) != 1:
        xa += 1
    uncompressed_even = b"\x04" + xa.to_bytes(32, "big") + y_for(xa, False).to_bytes(32, "big")
    assert GENESIS_Y[-1] & 1 and int.from_bytes(GENESIS_Y, "big") == y_for(int.from_bytes(GENESIS_X, "big"), True)
    comp_even = b"\x02" + hashlib.sha256(b"c-even").digest()
    comp_odd = b"\x03" + hashlib.sha256(b"c-odd").digest()
    return [
        ("p2pkh", b"\x76\xa9" + push(h160(b"a")) + b"\x88\xac", 150000),
        ("p2sh", b"\xa9" + push(h160(b"b")) + b"\x87", 1000000),
        ("p2wpkh", b"\x00" + push(h160(b"c")), 123456789),
        ("p2wsh", b"\x00" + push(hashlib.sha256(b"d").digest()), 10000),
        ("p2tr", b"\x51" + push(hashlib.sha256(b"e").digest()), 2000000000),
        ("p2pk_compressed_even", push(comp_even) + b"\xac", 77700),
        ("p2pk_compressed_odd", push(comp_odd) + b"\xac", 31337000),
        ("p2pk_uncompressed_odd", push(b"\x04" + GENESIS_X + GENESIS_Y) + b"\xac", 4200000),
        ("p2pk_uncompressed_even", push(uncompressed_even) + b"\xac", 99999999),
        ("raw_op_true", b"\x51", 20000),
        ("raw_arith", b"\x51\x52\x93\x54\x87", 30000),
        ("raw_bare_multisig", b"\x51" + push(comp_even) + b"\x51\xae", 40000),
        ("raw_empty", b"", 50000),
        ("raw_long", b"\x00" * 300, 60000),
    ]


def compact(n):
    return bytes([n]) if n < 253 else b"\xfd" + n.to_bytes(2, "little")


def raw_outputs_tx(outs):
    body = b"".join(amount.to_bytes(8, "little") + compact(len(s)) + s for s, amount in outs)
    return (b"\x02\x00\x00\x00\x00" + compact(len(outs)) + body + b"\x00\x00\x00\x00").hex()


def fund_and_send(node, outs):
    funded = node.cli("fundrawtransaction", raw_outputs_tx(outs), {}, False)["hex"]
    signed = node.cli("signrawtransactionwithwallet", funded)
    assert signed["complete"]
    return node.cli("sendrawtransaction", signed["hex"], "0")


def mine_txs(node, addr, txids):
    return node.cli("generateblock", addr, txids)["hash"]


def run(outdir):
    os.makedirs(outdir, exist_ok=True)
    node = Node()
    try:
        node.start()
        node.cli("createwallet", "w")
        addr = node.cli("getnewaddress", "", "bech32")
        other = node.cli("getnewaddress", "", "bech32")
        node.cli("generatetoaddress", "101", addr)

        txid = fund_and_send(node, [(s, a) for _, s, a in scripts()])
        node.cli("generatetoaddress", "1", addr)
        node.dump(outdir, "all_types")
        assert any(c["txid"] == txid for c in node.expected_utxos())

        a_txid = node.cli("sendtoaddress", other, "1.5")
        a_tx = node.cli("getrawtransaction", a_txid, "true")
        n = next(o["n"] for o in a_tx["vout"] if o["scriptPubKey"]["address"] == other)
        raw_b = node.cli("createrawtransaction", [{"txid": a_txid, "vout": n}], {addr: "1.4999"})
        signed_b = node.cli("signrawtransactionwithwallet", raw_b)
        assert signed_b["complete"]
        b_txid = node.cli("sendrawtransaction", signed_b["hex"])
        mine_txs(node, addr, [a_txid, b_txid])
        coins = node.dump(outdir, "same_block")
        assert not any((c["txid"], c["vout"]) == (a_txid, n) for c in coins)
        assert any(c["txid"] == b_txid for c in coins)

        tip_tx = node.cli("sendtoaddress", other, "0.25")
        mine_txs(node, addr, [tip_tx])
        node.dump(outdir, "reorg1_before")
        node.cli("invalidateblock", node.cli("getbestblockhash"))
        mine_txs(node, other, [])
        node.dump(outdir, "reorg1_after")

        node.cli("generatetoaddress", "2", addr)
        for _ in range(3):
            mine_txs(node, addr, [node.cli("sendtoaddress", other, "0.1")])
        node.dump(outdir, "reorgn_before")
        node.cli("invalidateblock", node.cli("getblockhash", str(node.cli("getblockcount") - 2)))
        for _ in range(4):
            mine_txs(node, other, [])
        node.dump(outdir, "reorgn_after")
    finally:
        node.stop()


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    run(sys.argv[1])
