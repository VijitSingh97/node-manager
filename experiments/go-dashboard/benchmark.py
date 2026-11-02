#!/usr/bin/env python3
import argparse
import csv
import json
import math
import statistics
import subprocess
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path


ENDPOINTS = ("/", "/metrics", "/api/fees", "/api/watch")
EXPECTED_RPC_PER_ROUTE = 8


def percentile(values, fraction):
    ordered = sorted(values)
    if not ordered:
        return None
    rank = (len(ordered) - 1) * fraction
    low = math.floor(rank)
    high = math.ceil(rank)
    if low == high:
        return ordered[low]
    return ordered[low] + (ordered[high] - ordered[low]) * (rank - low)


def docker_pid(name):
    return int(subprocess.check_output(["docker", "inspect", "-f", "{{.State.Pid}}", name], text=True).strip())


def cgroup_dir(pid):
    for line in Path(f"/proc/{pid}/cgroup").read_text().splitlines():
        fields = line.split(":", 2)
        if len(fields) == 3 and fields[0] == "0":
            path = (Path("/sys/fs/cgroup") / fields[2].lstrip("/")).resolve()
            if str(path).startswith("/sys/fs/cgroup/"):
                return path
    raise RuntimeError(f"no cgroup v2 path for PID {pid}")


def key_values(path):
    return {line.split()[0]: int(line.split()[1]) for line in path.read_text().splitlines() if len(line.split()) == 2}


def proc_status(pid):
    fields = {}
    for line in Path(f"/proc/{pid}/status").read_text().splitlines():
        if ":" in line:
            key, value = line.split(":", 1)
            fields[key] = value.strip()
    value_kib = lambda key: int(fields.get(key, "0 kB").split()[0])
    return {"rss_bytes": value_kib("VmRSS") * 1024, "hwm_bytes": value_kib("VmHWM") * 1024, "threads": int(fields["Threads"])}


def cgroup_sample(name):
    pid = docker_pid(name)
    root = cgroup_dir(pid)
    memory = int((root / "memory.current").read_text())
    memory_stat = key_values(root / "memory.stat")
    cpu = key_values(root / "cpu.stat")
    return {
        "time": time.monotonic(),
        "memory_bytes": memory,
        "memory_cgroup_peak_bytes": int((root / "memory.peak").read_text()),
        "working_set_bytes": max(0, memory - memory_stat.get("inactive_file", 0)),
        "cpu_usage_usec": cpu["usage_usec"],
        "nr_throttled": cpu.get("nr_throttled", 0),
        "throttled_usec": cpu.get("throttled_usec", 0),
        **proc_status(pid),
    }


def request(url, password):
    req = urllib.request.Request(url)
    token = __import__("base64").b64encode(f"admin:{password}".encode()).decode()
    req.add_header("Authorization", f"Basic {token}")
    start = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            response.read()
            status = response.status
    except urllib.error.HTTPError as error:
        error.read()
        status = error.code
    except Exception:
        status = 0
    return (time.perf_counter() - start) * 1000, status


def sample_phase(names, duration, action=None):
    start = {name: cgroup_sample(name) for name in names}
    samples = {name: [start[name]] for name in names}
    stopped = threading.Event()

    def sampler():
        while not stopped.wait(1):
            for name in names:
                samples[name].append(cgroup_sample(name))

    thread = threading.Thread(target=sampler, daemon=True)
    thread.start()
    action_result = action(duration) if action else (time.sleep(duration) or None)
    stopped.set()
    thread.join()
    end = {name: cgroup_sample(name) for name in names}
    for name in names:
        samples[name].append(end[name])
    summaries = {}
    for name in names:
        elapsed = end[name]["time"] - start[name]["time"]
        summaries[name] = {
            "memory_peak_bytes": max(row["memory_bytes"] for row in samples[name]),
            "memory_cgroup_peak_bytes": max(row["memory_cgroup_peak_bytes"] for row in samples[name]),
            "working_set_peak_bytes": max(row["working_set_bytes"] for row in samples[name]),
            "rss_peak_bytes": max(row["rss_bytes"] for row in samples[name]),
            "hwm_peak_bytes": max(row["hwm_bytes"] for row in samples[name]),
            "threads_peak": max(row["threads"] for row in samples[name]),
            "cpu_one_core_percent": cpu_percent(start[name]["cpu_usage_usec"], end[name]["cpu_usage_usec"], elapsed),
            "nr_throttled_delta": end[name]["nr_throttled"] - start[name]["nr_throttled"],
            "throttled_usec_delta": end[name]["throttled_usec"] - start[name]["throttled_usec"],
        }
    return summaries, action_result


def fixed_load(base_url, password, duration):
    rows = []
    executor = ThreadPoolExecutor(max_workers=2)
    pending = []
    deadline = time.monotonic() + duration
    sequence = 0
    while time.monotonic() < deadline:
        endpoint = ENDPOINTS[sequence % len(ENDPOINTS)]
        pending.append((endpoint, executor.submit(request, base_url + endpoint, password)))
        sequence += 1
        time.sleep(max(0, min(0.5, deadline - time.monotonic())))
    executor.shutdown(wait=True)
    for endpoint, future in pending:
        latency, status = future.result()
        rows.append({"endpoint": endpoint, "latency_ms": latency, "status": status})
    return rows


def cpu_percent(start_usec, end_usec, elapsed_seconds):
    return (end_usec - start_usec) / (elapsed_seconds * 1_000_000) * 100


def fixture_counts(url):
    with urllib.request.urlopen(url + "/counts", timeout=5) as response:
        return json.load(response)


def delta_counts(before, after, user):
    methods = set(before["users"][user]) | set(after["users"][user])
    return {method: after["users"][user].get(method, 0) - before["users"][user].get(method, 0) for method in sorted(methods)}


def image_size(name):
    return int(subprocess.check_output(["docker", "image", "inspect", "-f", "{{.Size}}", name], text=True).strip())


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    parser.add_argument("--cycles", type=int, default=3)
    parser.add_argument("--warmup", type=int, default=30)
    parser.add_argument("--idle", type=int, default=70)
    parser.add_argument("--load", type=int, default=30)
    parser.add_argument("--go-image", required=True)
    parser.add_argument("--python-image", required=True)
    parser.add_argument("--go-url", required=True)
    parser.add_argument("--python-url", required=True)
    parser.add_argument("--fixture-url", required=True)
    args = parser.parse_args()
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    apps = {
        "go": {"container": "bss-go-bench-go", "url": args.go_url, "user": "go", "image": args.go_image},
        "python": {"container": "bss-go-bench-python", "url": args.python_url, "user": "py", "image": args.python_image},
    }
    with ThreadPoolExecutor(max_workers=2) as executor:
        warmups = [executor.submit(fixed_load, app["url"], "benchpass", args.warmup) for app in apps.values()]
        for future in warmups:
            future.result()

    report = {"method": "controlled short benchmark", "cycles": [], "images": {key: image_size(app["image"]) for key, app in apps.items()}}
    raw = []
    fixture_url = args.fixture_url
    for cycle in range(args.cycles):
        idle, _ = sample_phase([app["container"] for app in apps.values()], args.idle)
        cycle_result = {"cycle": cycle + 1, "idle": idle, "load": {}}
        order = ("go", "python") if cycle % 2 == 0 else ("python", "go")
        for key in order:
            app = apps[key]
            before = fixture_counts(fixture_url)
            summaries, rows = sample_phase([app["container"]], args.load, lambda duration: fixed_load(app["url"], "benchpass", duration))
            after = fixture_counts(fixture_url)
            statuses = [row["status"] for row in rows]
            latencies = [row["latency_ms"] for row in rows if row["status"] == 200]
            expected_rounds = sum(1 for row in rows if row["endpoint"] == "/") + sum(1 for row in rows if row["endpoint"] == "/metrics")
            rpc = delta_counts(before, after, app["user"])
            rpc_total = sum(rpc.values())
            minimums = {
                "getblockchaininfo": expected_rounds,
                "getnetworkinfo": expected_rounds,
                "getpeerinfo": expected_rounds,
                "uptime": expected_rounds,
                "getmempoolinfo": expected_rounds,
                "estimatesmartfee": expected_rounds * 3,
            }
            if any(status != 200 for status in statuses):
                raise RuntimeError(f"{key} returned a non-200 response")
            missing = {method: minimum - rpc.get(method, 0) for method, minimum in minimums.items() if rpc.get(method, 0) < minimum}
            if missing:
                raise RuntimeError(f"{key} did not make the expected read RPCs: {missing}")
            cycle_result["load"][key] = {
                **summaries[app["container"]],
                "requests": len(rows),
                "errors": sum(status != 200 for status in statuses),
                "latency_p50_ms": percentile(latencies, 0.50),
                "latency_p95_ms": percentile(latencies, 0.95),
                "rpc_counts": rpc,
                "rpc_minimums_from_requests": minimums,
                "rpc_expected_from_requests": expected_rounds * EXPECTED_RPC_PER_ROUTE,
                "rpc_observed": rpc_total,
                "rpc_background_delta": rpc_total - expected_rounds * EXPECTED_RPC_PER_ROUTE,
            }
            for row in rows:
                raw.append({"cycle": cycle + 1, "app": key, **row})
        report["cycles"].append(cycle_result)
    final_counts = fixture_counts(fixture_url)
    report["fixture"] = final_counts
    if final_counts["rejected"]:
        raise RuntimeError(f"fixture rejected {final_counts['rejected']} RPC requests")
    report["summary"] = {}
    for key, app in apps.items():
        idle_rows = [cycle["idle"][app["container"]] for cycle in report["cycles"]]
        load_rows = [cycle["load"][key] for cycle in report["cycles"]]
        report["summary"][key] = {
            "image_size_bytes": report["images"][key],
            "idle_working_set_median_bytes": int(statistics.median(row["working_set_peak_bytes"] for row in idle_rows)),
            "idle_cpu_median_one_core_percent": statistics.median(row["cpu_one_core_percent"] for row in idle_rows),
            "load_working_set_median_bytes": int(statistics.median(row["working_set_peak_bytes"] for row in load_rows)),
            "load_cpu_median_one_core_percent": statistics.median(row["cpu_one_core_percent"] for row in load_rows),
            "load_p50_median_ms": statistics.median(row["latency_p50_ms"] for row in load_rows),
            "load_p95_median_ms": statistics.median(row["latency_p95_ms"] for row in load_rows),
            "errors": sum(row["errors"] for row in load_rows),
            "throttled_periods": sum(row["nr_throttled_delta"] for row in load_rows),
        }
    (output / "report.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    with (output / "requests.csv").open("w", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=("cycle", "app", "endpoint", "latency_ms", "status"))
        writer.writeheader()
        writer.writerows(raw)
    print(json.dumps(report["summary"], indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
