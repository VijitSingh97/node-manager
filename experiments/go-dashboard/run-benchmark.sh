#!/usr/bin/env bash
set -euo pipefail

scratch=/home/vijit/maintenance-20261027/go-dashboard-benchmark
bench_dir=$scratch/source/experiments/go-dashboard
go_image=${GO_BENCH_IMAGE:-bss-go-bench-go:local}
python_ref=ghcr.io/vijitsingh97/bitcoin-starter-stack-dashboard:1.31.1
python_image=
network=bss-go-bench-net
containers=(bss-go-bench-fixture bss-go-bench-go bss-go-bench-python)
before=$scratch/production-before.txt
after=$scratch/production-after.txt
snapshot_taken=0
run_id=
fixture_url=
go_url=
python_url=

die() {
  echo "benchmark: $*" >&2
  exit 1
}

verify_source() {
  local directory=$1
  local expected=$2
  local actual manifest_files staged_files bad_node
  [[ -d $directory && -f $directory/MANIFEST.sha256 ]] || die "missing reviewed source manifest"
  actual=$(sha256sum "$directory/MANIFEST.sha256" | awk '{print $1}')
  [[ $actual == "$expected" ]] || die "staged source manifest hash does not match the reviewed hash"
  if ! awk 'NF == 2 && $1 ~ /^[0-9a-f]{64}$/ && $2 ~ /^[A-Za-z0-9][A-Za-z0-9._-]*$/ && $2 != "MANIFEST.sha256" { next } { exit 1 }' "$directory/MANIFEST.sha256"; then
    die "source manifest has an invalid entry"
  fi
  bad_node=$(find "$directory" -mindepth 1 -maxdepth 1 ! -type f -print -quit)
  [[ -z $bad_node ]] || die "staged source contains a symlink or non-regular file: $bad_node"
  manifest_files=$(awk '{print $2}' "$directory/MANIFEST.sha256" | LC_ALL=C sort)
  staged_files=$(find "$directory" -mindepth 1 -maxdepth 1 -type f ! -name MANIFEST.sha256 -exec basename {} \; | LC_ALL=C sort)
  [[ $manifest_files == "$staged_files" ]] || die "staged source file set does not exactly match the reviewed manifest"
  (cd "$directory" && sha256sum -c MANIFEST.sha256 >/dev/null) || die "staged source does not match its reviewed manifest"
}

snapshot_production() {
  local output=$1
  {
    readlink -f /home/vijit/current
    git -C /home/vijit/bitcoin-starter-stack rev-parse HEAD
    sha256sum /home/vijit/bitcoin-starter-stack/.env | awk '{print $1}'
    for name in bitcoin tor dashboard; do
      docker inspect -f '{{.Name}}|{{.Id}}|{{.Image}}|{{.Config.Image}}|{{.State.StartedAt}}|{{range .Mounts}}{{.Source}}:{{.Destination}}:{{.RW}};{{end}}' "$name"
    done
  } >"$output"
}

private_url() {
  local name=$1
  local port=$2
  local ip
  ip=$(docker inspect "$name" | python3 -c '
import ipaddress
import json
import sys

container = json.load(sys.stdin)[0]
expected = sys.argv[1]
networks = container["NetworkSettings"]["Networks"]
if set(networks) != {expected}:
    raise SystemExit(f"expected only {expected}, found {sorted(networks)}")
address = networks[expected].get("IPAddress", "")
try:
    ipaddress.IPv4Address(address)
except ipaddress.AddressValueError:
    raise SystemExit(f"invalid private IPv4 address: {address!r}")
gateway = networks[expected].get("Gateway")
if gateway:
    raise SystemExit(f"unexpected default gateway: {gateway}")
print(address)
' "$network") || die "$name failed private-network validation"
  printf 'http://%s:%s' "$ip" "$port"
}

cleanup() {
  local name
  for name in "${containers[@]}"; do
    if [[ $name == bss-go-bench-* ]] && docker container inspect "$name" >/dev/null 2>&1; then
      if [[ $(docker inspect -f '{{index .Config.Labels "bss-go-bench.run"}}' "$name" 2>/dev/null) == "$run_id" ]]; then
        docker rm -f "$name" >/dev/null
      else
        echo "benchmark: refusing to remove unowned container $name" >&2
      fi
    fi
  done
  if docker network inspect "$network" >/dev/null 2>&1; then
    if [[ $(docker network inspect -f '{{index .Labels "bss-go-bench.run"}}' "$network" 2>/dev/null) == "$run_id" ]]; then
      docker network rm "$network" >/dev/null
    else
      echo "benchmark: refusing to remove unowned network $network" >&2
    fi
  fi
}

finish() {
  local status=$?
  trap - EXIT
  cleanup || status=1
  if [[ $snapshot_taken == 1 ]]; then
    if ! snapshot_production "$after"; then
      echo "benchmark: could not take the final production snapshot" >&2
      status=1
    elif ! cmp -s "$before" "$after"; then
      diff -u "$before" "$after" >&2 || true
      echo "benchmark: production state changed during benchmark" >&2
      status=1
    fi
  fi
  exit "$status"
}

if [[ ${1:-} == --selftest-cleanup ]]; then
  run_id=selftest-owner
  cleanup
  exit 0
fi

if [[ ${1:-} == --selftest-source ]]; then
  [[ $# == 3 ]] || die "usage: --selftest-source DIRECTORY MANIFEST_SHA256"
  verify_source "$2" "$3"
  exit 0
fi

expected_go_image_id=${GO_BENCH_IMAGE_ID:?set GO_BENCH_IMAGE_ID to the reviewed image ID}
expected_manifest=${GO_BENCH_SOURCE_MANIFEST_SHA256:?set GO_BENCH_SOURCE_MANIFEST_SHA256 to the reviewed manifest hash}
[[ $expected_go_image_id =~ ^sha256:[0-9a-f]{64}$ ]] || die "GO_BENCH_IMAGE_ID is not a sha256 image ID"
[[ $expected_manifest =~ ^[0-9a-f]{64}$ ]] || die "GO_BENCH_SOURCE_MANIFEST_SHA256 is not a sha256 hash"

[[ $(uname -s) == Linux ]] || die "run this benchmark on mini-1"
[[ $scratch == /home/vijit/maintenance-20261027/go-dashboard-benchmark ]] || die "unexpected scratch path"
[[ -d $bench_dir ]] || die "missing staged source at $bench_dir"
[[ $(realpath "$bench_dir") == "$scratch"/source/experiments/go-dashboard ]] || die "benchmark source escapes the scratch root"
[[ $(id -u) -ne 0 ]] || die "run as the normal mini-1 user"
cat /home/vijit/README.md >/dev/null

verify_source "$bench_dir" "$expected_manifest"
actual_manifest=$(sha256sum "$bench_dir/MANIFEST.sha256" | awk '{print $1}')
run_id=${actual_manifest:0:16}-$$

for name in "${containers[@]}"; do
  ! docker container inspect "$name" >/dev/null 2>&1 || die "container $name already exists"
done
! docker network inspect "$network" >/dev/null 2>&1 || die "network $network already exists"
for name in bitcoin tor dashboard; do
  [[ $(docker inspect -f '{{.State.Running}}' "$name") == true ]] || die "production $name is not running"
done
[[ $(readlink -f /home/vijit/current) == /home/vijit/bitcoin-starter-stack ]] || die "unexpected current symlink"
[[ ! -e $scratch/data/.cookie ]] || die "scratch data unexpectedly contains a Bitcoin cookie"
[[ $(docker inspect -f '{{.Config.Image}}' dashboard) == "$python_ref" ]] || die "production dashboard is not the expected 1.31.1 image"
python_image=$(docker inspect -f '{{.Image}}' dashboard)
[[ $python_image == sha256:* ]] || die "production dashboard did not resolve to an immutable image ID"
actual_go_image_id=$(docker image inspect -f '{{.Id}}' "$go_image" 2>/dev/null) || die "missing staged Go image $go_image"
[[ $actual_go_image_id == "$expected_go_image_id" ]] || die "staged Go image ID does not match the reviewed image"

mkdir -p "$scratch"/{data,go-state,python-state,results}
chmod 700 "$scratch" "$scratch/data" "$scratch/go-state" "$scratch/python-state" "$scratch/results"
snapshot_production "$before"
snapshot_taken=1
trap finish EXIT

docker network create --internal --label "bss-go-bench.run=$run_id" "$network" >/dev/null
[[ $(docker network inspect -f '{{.Internal}}' "$network") == true ]] || die "$network is not internal"

common=(--network "$network" --label "bss-go-bench.run=$run_id" --security-opt no-new-privileges --cap-drop ALL --pids-limit 64 --read-only --tmpfs "/tmp:rw,noexec,nosuid,size=16m" --restart no)
docker run -d --name bss-go-bench-fixture "${common[@]}" --user 1000:1000 --memory 128m --cpus 0.5 \
  -v "$bench_dir:/bench:ro" --entrypoint python "$python_image" /bench/fixture.py >/dev/null
docker run -d --name bss-go-bench-go "${common[@]}" --user 1000:1000 --memory 256m --cpus 0.5 \
  -v "$scratch/data:/data:ro" -v "$scratch/go-state:/state" \
  -e RPC_URL=http://bss-go-bench-fixture:8001 -e RPC_USER=go -e RPC_PASSWORD=fakepw \
  -e DASHBOARD_PASSWORD=benchpass -e DATA_DIR=/data -e STATE_DIR=/state -e STACK_VERSION=1.31.1 \
  -e PORT=8000 "$go_image" >/dev/null
docker run -d --name bss-go-bench-python "${common[@]}" --user 1000:1000 --memory 256m --cpus 0.5 \
  -v "$scratch/data:/data:ro" -v "$scratch/python-state:/state" -v "$bench_dir:/bench:ro" \
  -e BENCH_RPC_URL=http://bss-go-bench-fixture:8001 -e DASHBOARD_PASSWORD=benchpass -e STACK_VERSION=1.31.1 \
  --entrypoint python "$python_image" /bench/python_reference.py >/dev/null

fixture_url=$(private_url bss-go-bench-fixture 8001)
go_url=$(private_url bss-go-bench-go 8000)
python_url=$(private_url bss-go-bench-python 8000)

ready=0
for _ in $(seq 1 60); do
  if curl -fsS "$fixture_url/counts" >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
[[ $ready == 1 ]] || die "fixture at $fixture_url did not become ready"

for url in "$go_url" "$python_url"; do
  ready=0
  for _ in $(seq 1 60); do
    if curl -fsS -u admin:benchpass "$url/" >/dev/null; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ $ready == 1 ]] || die "dashboard at $url did not become ready"
done

python3 "$bench_dir/benchmark.py" \
  --output "$scratch/results" \
  --go-image "$go_image" \
  --python-image "$python_image" \
  --go-url "$go_url" \
  --python-url "$python_url" \
  --fixture-url "$fixture_url"

echo "Results: $scratch/results/report.json"
