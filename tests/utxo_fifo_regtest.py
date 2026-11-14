#!/usr/bin/env python3
"""FIFO seeding wedge matrix for dumptxoutset on throwaway regtest containers.

Run: python3 -I tests/utxo_fifo_regtest.py [results.json]
Needs Docker and the pinned bitcoind image from docker-compose.yml. It creates one bitcoind
container with a named volume and short-lived reader containers mounting that volume read-only,
and removes all of them (and the volume) on exit. Scenarios S0-S6 are described in
docs/measurements/utxo-fifo-wedge.md. S3 stalls the reader for 120 s.
"""
import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import time
import uuid

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
spec = importlib.util.spec_from_file_location("utxo_regtest", os.path.join(ROOT, "tests", "utxo_regtest.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)

DATADIR = "/home/bitcoin/.bitcoin"
RO = "/ro"
DAT = f"{DATADIR}/utxo-seed.dat"
INC = DAT + ".incomplete"
RO_INC = f"{RO}/utxo-seed.dat.incomplete"
LOG = f"{DATADIR}/regtest/debug.log"
READER_UID = None
MIN_DUMP_BYTES = 200000
HEADER_LEN = 51
HEX_OD = "od -An -tx1 -v | tr -d ' \\n'"


def run(args, timeout=120, check=False):
    return subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=check)


class Env:
    def __init__(self):
        tag = uuid.uuid4().hex[:8]
        self.vol = f"utxo-fifo-matrix-{tag}-data"
        self.node = f"utxo-fifo-matrix-{tag}-bitcoind"
        self.image = base.pinned_image()
        self.containers = [self.node]
        self.volume_made = False
        self.n_readers = 0
        self.tag = tag

    def start_node(self):
        run(["docker", "volume", "create", self.vol], check=True)
        self.volume_made = True
        run(["docker", "run", "-d", "--name", self.node, "--restart", "on-failure", "-v", f"{self.vol}:{DATADIR}",
             self.image, "bitcoind", "-regtest", "-acceptnonstdtxn=1", "-fallbackfee=0.0001"], check=True)
        self.wait_rpc()

    def wait_rpc(self, tries=90):
        for _ in range(tries):
            if self.cli_raw(["getblockcount"], timeout=5)[0] == 0:
                return
            time.sleep(1)
        raise RuntimeError("bitcoind RPC not ready")

    def cli_raw(self, args, client_timeout=None, timeout=60):
        cmd = ["docker", "exec", "-u", "bitcoin", self.node, "bitcoin-cli", "-regtest"]
        if client_timeout is not None:
            cmd.append(f"-rpcclienttimeout={client_timeout}")
        t = time.time()
        try:
            r = run(cmd + list(args), timeout=timeout)
            return r.returncode, r.stdout.strip(), r.stderr.strip(), time.time() - t
        except subprocess.TimeoutExpired:
            return 124, "", "host timeout", time.time() - t

    def cli(self, *args):
        a = [x if isinstance(x, str) else json.dumps(x) for x in args]
        rc, out, err, _ = self.cli_raw(a)
        if rc != 0:
            raise RuntimeError(f"bitcoin-cli {' '.join(a)}: {err}")
        try:
            return json.loads(out)
        except ValueError:
            return out

    def sh(self, script, user="bitcoin", timeout=60):
        r = run(["docker", "exec", "-u", user, self.node, "bash", "-c", script], timeout=timeout)
        return r.returncode, (r.stdout + r.stderr).strip()

    def start_dump(self):
        cmd = ["docker", "exec", "-u", "bitcoin", self.node, "bitcoin-cli", "-regtest", "-rpcclienttimeout=0",
               "dumptxoutset", DAT, "latest"]
        p = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        p.t0 = time.time()
        return p

    def inspect(self, fmt, name=None):
        return run(["docker", "inspect", name or self.node, "--format", fmt]).stdout.strip()

    def state(self):
        return {"restart_count": int(self.inspect("{{.RestartCount}}")),
                "started_at": self.inspect("{{.State.StartedAt}}"),
                "running": self.inspect("{{.State.Running}}") == "true"}

    def log_mark(self):
        return int(self.sh(f"wc -l < {LOG}")[1])

    def log_since(self, mark):
        return self.sh(f"tail -n +{mark + 1} {LOG}")[1].splitlines()

    def prep(self, fifo=True, mode="644"):
        self.sh(f"rm -f {DAT} {INC}")
        if fifo:
            rc, out = self.sh(f"mkfifo -m {mode} {INC}")
            assert rc == 0, out

    def reader(self, script, args=(), user=None):
        self.n_readers += 1
        name = f"utxo-fifo-matrix-{self.tag}-reader{self.n_readers}"
        self.containers.append(name)
        run(["docker", "run", "-d", "--name", name, "--user", user or READER_UID, "--entrypoint", "bash",
             "-v", f"{self.vol}:{RO}:ro", self.image, "-c", script, "reader", *args], check=True)
        return name

    def logs(self, name):
        r = run(["docker", "logs", name])
        return (r.stdout + r.stderr).strip()

    def wait_container(self, name, timeout=180):
        r = run(["docker", "wait", name], timeout=timeout)
        return int(r.stdout.strip() or -1)

    def wait_log(self, name, text, timeout=60):
        end = time.time() + timeout
        while time.time() < end:
            if text in self.logs(name):
                return True
            time.sleep(0.2)
        return False

    def active_commands(self):
        rc, out, err, el = self.cli_raw(["getrpcinfo"], client_timeout=2, timeout=15)
        if rc != 0:
            return None, err, el
        return [c["method"] for c in json.loads(out)["active_commands"]], "", el

    def cleanup(self):
        for c in self.containers:
            run(["docker", "rm", "-f", c])
        if self.volume_made:
            run(["docker", "volume", "rm", "-f", self.vol])

    def leftovers(self):
        out = run(["docker", "ps", "-a", "--filter", f"name=utxo-fifo-matrix-{self.tag}", "--format", "{{.Names}}"]).stdout
        vol = run(["docker", "volume", "ls", "-q", "--filter", f"name=utxo-fifo-matrix-{self.tag}"]).stdout
        return (out + vol).split()


def parse_header(hexstr, expect_hash, expect_coins):
    b = bytes.fromhex(hexstr)
    ok_magic = b[:5] == b"utxo\xff"
    base_hash = b[11:43][::-1].hex()
    coins = int.from_bytes(b[43:51], "little")
    return {"magic_ok": ok_magic, "base_hash": base_hash, "coins": coins,
            "matches_chain": ok_magic and base_hash == expect_hash and coins == expect_coins}


def read_reader_result(logs):
    kv = {}
    for line in logs.splitlines():
        for part in line.split():
            if "=" in part:
                k, v = part.split("=", 1)
                kv[k] = v
    return kv


DRAIN = f"""F={RO_INC}
cat "$F" > /tmp/out
echo size=$(stat -c %s /tmp/out) sha=$(sha256sum < /tmp/out | cut -d' ' -f1) hdr=$(head -c {HEADER_LEN} /tmp/out | {HEX_OD})
"""

HEAD_THEN = f"""F={RO_INC}
exec 3<"$F"
dd iflag=fullblock bs=4096 count=24 <&3 of=/tmp/out 2>/dev/null
echo got=$(stat -c %s /tmp/out)
"""

KEEPER_PERL = r"""
use Fcntl;
sub now { open(my $u, "<", "/proc/uptime"); my $l = <$u>; return (split / /, $l)[0] }
my $f = shift; my $out = shift;
my $t0 = now();
sysopen(R, $f, O_RDONLY|O_NONBLOCK) or die "reader open: $!\n";
sysopen(K, $f, O_WRONLY) or die "keeper open: $!\n";
open(OUT, ">", $out) or die;
my ($idle, $bytes, $first, $eof, $kept) = (0, 0, undef, 0, 1);
my $deadline = $t0 + 90;
while (now() < $deadline) {
    my $rin = ""; vec($rin, fileno(R), 1) = 1;
    my $n = select(my $rout = $rin, undef, undef, 0.5);
    if ($n == 0) { $idle++; next }
    my $buf; my $r = sysread(R, $buf, 65536);
    if (!defined $r) { next if $!{EAGAIN}; die "read: $!\n" }
    if ($r == 0) { $eof = 1; last }
    print OUT $buf; $bytes += $r;
    if (!defined $first) { $first = now() - $t0; close K; $kept = 0 }
}
close OUT;
printf "idle_polls=%d first_byte_s=%.2f bytes=%d eof=%d keeper_closed=%d\n", $idle, $first // -1, $bytes, $eof, 1 - $kept;
"""

PERM_PERL = r"""
use Fcntl;
my $f = shift;
sub t { my ($n, $fl) = @_; my $ok = sysopen(my $h, $f, $fl); print "$n=" . ($ok ? "ok" : "err:$!") . "\n"; $ok ? $h : undef }
t("wronly_nonblock_no_reader", O_WRONLY|O_NONBLOCK);
my $r = t("rdonly_nonblock", O_RDONLY|O_NONBLOCK);
my $k = t("wronly_with_reader", O_WRONLY);
"""


def build_chain(env):
    env.cli("createwallet", "w")
    addr = env.cli("getnewaddress", "", "bech32")
    env.cli("generatetoaddress", "101", addr)
    for i in range(8):
        outs = [(b"\x00\x20" + hashlib.sha256(f"{i}-{j}".encode()).digest(), 10000) for j in range(1000)]
        base.fund_and_send(env, outs)
        env.cli("generatetoaddress", "1", addr)


def fresh_ok(env, expect):
    env.prep()
    r = env.reader(DRAIN)
    p = env.start_dump()
    out, err = p.communicate(timeout=180)
    env.wait_container(r)
    kv = read_reader_result(env.logs(r))
    hdr = parse_header(kv.get("hdr", "00" * HEADER_LEN), expect["hash"], expect["coins"])
    ok = p.returncode == 0 and hdr["matches_chain"] and kv.get("sha") == expect["sha"]
    return ok, {"rc": p.returncode, "err": err.strip(), "size": kv.get("size")}


def probes(env, rounds_label):
    res = {}
    for name, args in (("getblockchaininfo", ["getblockchaininfo"]), ("getrpcinfo", ["getrpcinfo"])):
        rc, out, err, el = env.cli_raw(args, client_timeout=2, timeout=15)
        res[name] = {"rc": rc, "seconds": round(el, 2), "err": err}
    res["label"] = rounds_label
    return res


def verify_chain(env):
    out = {}
    rc, o, e, el = env.cli_raw(["getblockchaininfo"], timeout=30)
    out["getblockchaininfo_rc"] = rc
    rc, o, e, el = env.cli_raw(["verifychain", "1", "0"], timeout=120)
    out["verifychain_1_rc"], out["verifychain_1_out"], out["verifychain_seconds"] = rc, o, round(el, 2)
    return out


def scenario_s0(env, expect, R):
    env.prep()
    mark = env.log_mark()
    r = env.reader(DRAIN)
    p = env.start_dump()
    out, err = p.communicate(timeout=180)
    dump_s = time.time() - p.t0
    env.wait_container(r)
    kv = read_reader_result(env.logs(r))
    hdr = parse_header(kv["hdr"], expect["hash"], expect["coins"])
    expect["sha"], expect["size"] = kv["sha"], int(kv["size"])
    _, ls = env.sh(f"stat -c '%n %F' {DAT} {INC} 2>&1")
    R["S0"] = {"rpc_rc": p.returncode, "rpc_out": out.strip(), "rpc_err": err.strip(), "dump_seconds": round(dump_s, 2),
               "bytes": kv["size"], "sha256": kv["sha"], "header": hdr, "stat_after": ls, "log": env.log_since(mark)}
    return p.returncode == 0 and hdr["matches_chain"] and int(kv["size"]) >= MIN_DUMP_BYTES and "No such file" in ls and "fifo" in ls


def scenario_s1(env, expect, R):
    env.prep()
    mark = env.log_mark()
    s0 = env.state()
    p = env.start_dump()
    time.sleep(2)
    active, aerr, aelapsed = env.active_commands()
    rounds = [probes(env, "t+2s")]
    time.sleep(8)
    rounds.append(probes(env, "t+10s"))
    time.sleep(20)
    rounds.append(probes(env, "t+30s"))
    still_parked = p.poll() is None
    t_unpark = time.time()
    rc, out = env.sh(f"timeout 5 cat {INC} > /dev/null; echo cat_rc=$?")
    try:
        o, e = p.communicate(timeout=60)
    except subprocess.TimeoutExpired:
        o, e = "TIMEOUT", ""
    after = env.state()
    ok2, fresh = fresh_ok(env, expect)
    max_probe = max(x[k]["seconds"] for x in rounds for k in ("getblockchaininfo", "getrpcinfo"))
    R["S1"] = {"active_commands": active, "active_err": aerr, "rounds": rounds, "max_probe_seconds": max_probe,
               "dump_still_parked_at_30s": still_parked, "unpark": out, "dump_seconds_after_unpark": round(time.time() - t_unpark, 2),
               "rpc_rc": p.returncode, "rpc_out": o.strip(), "rpc_err": e.strip(), "state_before": s0, "state_after": after,
               "fresh_dump": fresh, "log": env.log_since(mark)}
    return (still_parked and active is not None and "dumptxoutset" in active and max_probe < 2.0 and "cat_rc=0" in out
            and p.returncode == 0 and after["restart_count"] == s0["restart_count"] and ok2)


def scenario_s2(env, expect, R):
    env.prep()
    mark = env.log_mark()
    s0 = env.state()
    r = env.reader(HEAD_THEN + "sleep 600\n")
    p = env.start_dump()
    got = env.wait_log(r, "got=98304", 60)
    run(["docker", "kill", "-s", "KILL", r])
    t_kill = time.time()
    try:
        o, e = p.communicate(timeout=90)
        wedged = False
    except subprocess.TimeoutExpired:
        o, e, wedged = "TIMEOUT", "", True
        env.sh(f"timeout 5 cat {INC} > /dev/null")
        p.communicate(timeout=60)
    rpc_after = round(time.time() - t_kill, 2)
    alive = env.cli_raw(["getblockchaininfo"], client_timeout=5, timeout=20)
    after = env.state()
    _, ls = env.sh(f"stat -c '%n %F %s' {DAT} {INC} 2>&1")
    ok2, fresh = fresh_ok(env, expect)
    R["S2"] = {"reader_read_before_kill": got, "wedged": wedged, "rpc_rc": p.returncode, "rpc_out": o.strip(),
               "rpc_err": e.strip(), "seconds_from_kill_to_rpc_return": rpc_after, "alive_rc": alive[0], "alive_seconds": round(alive[3], 2),
               "state_before": s0, "state_after": after, "stat_after": ls, "fresh_dump": fresh, "log": env.log_since(mark)}
    return (got and not wedged and alive[0] == 0 and after["restart_count"] == s0["restart_count"]
            and after["started_at"] == s0["started_at"] and ok2)


def scenario_s3(env, expect, R):
    env.prep()
    mark = env.log_mark()
    s0 = env.state()
    script = (HEAD_THEN + "echo stalled\nsleep 120\ncat <&3 >> /tmp/out\n" +
              f"echo size=$(stat -c %s /tmp/out) sha=$(sha256sum < /tmp/out | cut -d' ' -f1) hdr=$(head -c {HEADER_LEN} /tmp/out | {HEX_OD})\n")
    r = env.reader(script)
    p = env.start_dump()
    assert env.wait_log(r, "stalled", 60)
    t_stall = time.time()
    rounds, active_seen = [], False
    while time.time() - t_stall < 100:
        rounds.append(probes(env, f"t+{int(time.time() - t_stall)}s"))
        active, _, _ = env.active_commands()
        active_seen = active_seen or bool(active and "dumptxoutset" in active)
        time.sleep(8)
    parked_at_end = p.poll() is None
    try:
        o, e = p.communicate(timeout=120)
    except subprocess.TimeoutExpired:
        o, e = "TIMEOUT", ""
    env.wait_container(r, 120)
    kv = read_reader_result(env.logs(r))
    hdr = parse_header(kv.get("hdr", "00" * HEADER_LEN), expect["hash"], expect["coins"])
    after = env.state()
    ok2, fresh = fresh_ok(env, expect)
    max_probe = max(x[k]["seconds"] for x in rounds for k in ("getblockchaininfo", "getrpcinfo"))
    fails = sum(1 for x in rounds for k in ("getblockchaininfo", "getrpcinfo") if x[k]["rc"] != 0)
    R["S3"] = {"probe_rounds": len(rounds), "max_probe_seconds": max_probe, "probe_failures": fails, "dump_active_in_getrpcinfo": active_seen,
               "dump_still_running_at_end_of_stall": parked_at_end, "rpc_rc": p.returncode, "rpc_out": o.strip(), "rpc_err": e.strip(),
               "bytes": kv.get("size"), "sha_matches_s0": kv.get("sha") == expect["sha"], "header": hdr, "state_before": s0, "state_after": after,
               "fresh_dump": fresh, "log": env.log_since(mark)}
    return (fails == 0 and max_probe < 1.0 and parked_at_end and p.returncode == 0 and hdr["matches_chain"]
            and kv.get("sha") == expect["sha"] and after["restart_count"] == s0["restart_count"] and ok2)


def scenario_s4(env, expect, R):
    env.prep(fifo=False)
    mark = env.log_mark()
    watch = (f"F={RO_INC}\nuntil [ -e \"$F\" ]; do :; done\n"
             "if [ -f \"$F\" ]; then echo kind=regular; elif [ -p \"$F\" ]; then echo kind=fifo; else echo kind=gone; fi\n")
    r = env.reader(watch)
    time.sleep(1.5)
    p = env.start_dump()
    o, e = p.communicate(timeout=120)
    env.wait_container(r, 30)
    kind = read_reader_result(env.logs(r)).get("kind")
    _, ls = env.sh(f"stat -c '%n %F %s' {DAT} {INC} 2>&1; head -c {HEADER_LEN} {DAT} | {HEX_OD}")
    R["S4"] = {"rpc_rc": p.returncode, "rpc_out": o.strip(), "rpc_err": e.strip(), "watcher_saw_incomplete_as": kind,
               "stat_after": ls, "log": env.log_since(mark)}
    return p.returncode == 0 and "regular file" in ls and kind in ("regular", "gone")


def scenario_s5(env, expect, R):
    out = {}
    ok = True
    env.prep()
    mark = env.log_mark()
    s0 = env.state()
    p = env.start_dump()
    time.sleep(2)
    rc, o, e, el = env.cli_raw(["stop"], timeout=30)
    t_stop = time.time()
    out["stop_cli"] = {"rc": rc, "out": o, "err": e, "seconds": round(el, 2)}
    exited_s = None
    for _ in range(45):
        if not env.state()["running"]:
            exited_s = round(time.time() - t_stop, 1)
            break
        time.sleep(1)
    out["exited_on_its_own_within_45s"] = exited_s is not None
    out["seconds_to_exit_after_stop"] = exited_s
    if exited_s is None:
        t_unpark = time.time()
        out["unpark"] = env.sh(f"timeout 5 cat {INC} > /dev/null; echo cat_rc=$?")[1]
        for _ in range(60):
            if not env.state()["running"]:
                out["seconds_to_exit_after_unpark"] = round(time.time() - t_unpark, 1)
                break
            time.sleep(1)
    out["exit_code"] = int(env.inspect("{{.State.ExitCode}}"))
    try:
        o2, e2 = p.communicate(timeout=30)
    except subprocess.TimeoutExpired:
        o2, e2 = "TIMEOUT", ""
    out["dump_client"] = {"rc": p.returncode, "out": o2.strip(), "err": e2.strip()}
    run(["docker", "start", env.node])
    env.wait_rpc()
    out["log_a"] = env.log_since(mark)
    out["after_restart_a"] = verify_chain(env)
    out["state_after_a"] = env.state()
    ok = ok and out["exit_code"] == 0 and out["after_restart_a"]["verifychain_1_out"] == "true"

    env.prep()
    p = env.start_dump()
    time.sleep(2)
    run(["docker", "kill", "-s", "KILL", env.node])
    t_kill = time.time()
    time.sleep(5)
    if not env.state()["running"]:
        run(["docker", "start", env.node])
    env.wait_rpc()
    p.communicate(timeout=30)
    out["sigkill_restart_seconds"] = round(time.time() - t_kill, 1)
    out["after_restart_b"] = verify_chain(env)
    out["state_after_b"] = env.state()
    ok2, fresh = fresh_ok(env, expect)
    out["fresh_dump"] = fresh
    out["restart_note"] = "container uses --restart on-failure, so RestartCount counts crash restarts"
    R["S5"] = out
    return ok and out["after_restart_b"]["verifychain_1_out"] == "true" and ok2 and out["exited_on_its_own_within_45s"]


def scenario_s6(env, expect, R):
    out = {}
    for mode in ("644", "666"):
        env.prep(mode=mode)
        r = env.reader(f"perl -e '{PERM_PERL}' {RO_INC}")
        env.wait_container(r, 30)
        out[f"permissions_mode_{mode}_as_uid_{READER_UID}"] = env.logs(r).splitlines()
    env.prep(mode="666")
    mark = env.log_mark()
    s0 = env.state()
    r = env.reader(f"perl -e '{KEEPER_PERL}' {RO_INC} /tmp/out\n"
                   f"echo sha=$(sha256sum < /tmp/out | cut -d' ' -f1) size=$(stat -c %s /tmp/out) hdr=$(head -c {HEADER_LEN} /tmp/out | {HEX_OD})\n")
    time.sleep(4)
    out["reader_container_still_waiting_after_4s"] = env.inspect("{{.State.Running}}", r)
    p = env.start_dump()
    o, e = p.communicate(timeout=120)
    env.wait_container(r, 120)
    kv = read_reader_result(env.logs(r))
    hdr = parse_header(kv.get("hdr", "00" * HEADER_LEN), expect["hash"], expect["coins"])
    out.update({"rpc_rc": p.returncode, "rpc_out": o.strip(), "rpc_err": e.strip(), "reader_summary": env.logs(r).splitlines(),
                "header": hdr, "sha_matches_s0": kv.get("sha") == expect["sha"], "state_before": s0, "state_after": env.state()})
    ok2, fresh = fresh_ok(env, expect)
    out["fresh_dump"] = fresh
    out["log"] = env.log_since(mark)
    R["S6"] = out
    perm644 = out[f"permissions_mode_644_as_uid_{READER_UID}"]
    perm666 = out[f"permissions_mode_666_as_uid_{READER_UID}"]
    return (out["reader_container_still_waiting_after_4s"] == "true" and p.returncode == 0 and hdr["matches_chain"]
            and kv.get("sha") == expect["sha"] and kv.get("idle_polls", "0") != "0" and kv.get("eof") == "1" and ok2
            and "wronly_with_reader=ok" in perm666 and "rdonly_nonblock=ok" in perm644)


def main():
    global READER_UID
    outfile = sys.argv[1] if len(sys.argv) > 1 else None
    env = Env()
    READER_UID = run(["docker", "run", "--rm", "--entrypoint", "id", env.image, "-u", "bitcoin"], check=True).stdout.strip()
    R = {"reader_uid": READER_UID, "image": env.image, "date": time.strftime("%Y-%m-%d"), "verdicts": {}}
    try:
        env.start_node()
        R["core_version"] = env.cli("getnetworkinfo")["subversion"]
        build_chain(env)
        tip = env.cli("getbestblockhash")
        coins = env.cli("gettxoutsetinfo", "none")["txouts"]
        expect = {"hash": tip, "coins": coins}
        R["chain"] = {"height": env.cli("getblockcount"), "txouts": coins}
        for sid, fn in (("S0", scenario_s0), ("S1", scenario_s1), ("S2", scenario_s2), ("S3", scenario_s3),
                        ("S4", scenario_s4), ("S5", scenario_s5), ("S6", scenario_s6)):
            t = time.time()
            try:
                R["verdicts"][sid] = "PASS" if fn(env, expect, R) else "FAIL"
            except Exception as exc:
                R["verdicts"][sid] = "FAIL"
                R.setdefault(sid, {})["exception"] = repr(exc)
                if not env.state()["running"]:
                    run(["docker", "start", env.node])
                    env.wait_rpc()
            R[sid]["scenario_seconds"] = round(time.time() - t, 1)
            print(sid, R["verdicts"][sid], flush=True)
        R["baseline"] = {k: expect.get(k) for k in ("size", "sha")}
    finally:
        env.cleanup()
        R["containers_left"] = env.leftovers()
        text = json.dumps(R, indent=1, sort_keys=True)
        if outfile:
            with open(outfile, "w") as f:
                f.write(text + "\n")
        print(text)
    return 0 if all(v == "PASS" for v in R["verdicts"].values()) and not R["containers_left"] else 1


if __name__ == "__main__":
    sys.exit(main())
