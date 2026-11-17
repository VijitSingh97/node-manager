#!/usr/bin/env bash
# Regenerates testdata/parity/expected.json by running scripts/node-resource-profile against the fixture.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
root=$tmp/r
mkdir -p "$root" "$tmp/bin"
cp -R "$here/parity/." "$root/"
grep -rl '@ROOT@' "$root" | while read -r file; do sed -i.bak "s#@ROOT@#$root#g" "$file" && rm "$file.bak"; done
mkdir -p "$root/state/data/bitcoin" "$root/xmr" "$root/root"

if command -v gawk >/dev/null; then ln -s "$(command -v gawk)" "$tmp/bin/awk"; fi
cat >"$tmp/bin/sleep" <<'SHIM'
#!/bin/sh
cp -R "$FIXTURE/proc-next/." "$FIXTURE/proc/"
cp -R "$FIXTURE/cgroup-next/." "$FIXTURE/cgroup/"
SHIM
cat >"$tmp/bin/df" <<'SHIM'
#!/bin/sh
cat "$FIXTURE/df.txt"
SHIM
cat >"$tmp/bin/du" <<'SHIM'
#!/bin/sh
awk -v d="$2" '$1 == d { printf "%s\t%s\n", $2, d }' "$FIXTURE/du.txt"
SHIM
cat >"$tmp/bin/lsblk" <<'SHIM'
#!/bin/sh
cat "$FIXTURE/lsblk/$(basename "$4")"
SHIM
cat >"$tmp/docker" <<'DOCKER'
#!/bin/sh
set -eu
project=bitcoin-starter-stack-apt
case "$1" in
  compose) case "$*" in *"ps -q dashboard"*) jq -r '.[] | select(.Labels["com.docker.compose.service"] == "dashboard") | .Id' "$FIXTURE/containers.json" ;; esac ;;
  info) case "$3" in
    '{{.NCPU}}') jq -r .NCPU "$FIXTURE/info.json" ;;
    '{{.MemTotal}}') jq -r .MemTotal "$FIXTURE/info.json" ;;
    '{{.DockerRootDir}}') jq -r .DockerRootDir "$FIXTURE/info.json" ;;
  esac ;;
  stats) jq -r --arg p "$project" '.[] | select(.State == "running") | [.Id, ($p + "-" + .Labels["com.docker.compose.service"] + "-1"), "0.00%", "1MiB / 8GiB"] | @tsv' "$FIXTURE/containers.json" ;;
  ps) case "$*" in
    *'{{.ID}}'*) jq -r '.[] | "\(.Id[0:12]) \(.Labels["com.docker.compose.service"]) \(.State)"' "$FIXTURE/containers.json" ;;
    *) jq -r '.[] | "\(.Labels["com.docker.compose.service"]) \(.State)"' "$FIXTURE/containers.json" ;;
  esac ;;
  inspect) case "$2" in
    d09e*) restarts=6 code=1 ;;
    *) restarts=0 code=0 ;;
  esac
    printf '[{"RestartCount":%s,"State":{"ExitCode":%s,"OOMKilled":false,"StartedAt":"2026-01-01T00:00:00.5Z","FinishedAt":"2026-01-01T00:00:01.5Z"}}]\n' "$restarts" "$code" ;;
  exec) if [ "$2" = -i ]; then case "$*" in *events.log*) cat >/dev/null ;; *) cat >"$OUT" ;; esac; fi ;;
esac
DOCKER
chmod +x "$tmp/docker" "$tmp/bin/"*
cp "$repo/scripts/node-resource-profile" "$tmp/node-resource-profile"
# shellcheck disable=SC2016
sed -i.bak 's/^    \[ -b "\$dev" \] || continue$/    :/' "$tmp/node-resource-profile" && rm "$tmp/node-resource-profile.bak"
grep -q '^    :$' "$tmp/node-resource-profile"

env FIXTURE="$root" OUT="$root/expected.json" PATH="$tmp/bin:$PATH" DOCKER_BIN="$tmp/docker" \
  BITCOIN_STACK_ROOT="$root/root" BITCOIN_STACK_STATE_DIR="$root/state" \
  RESOURCE_PROC_ROOT="$root/proc" RESOURCE_SYS_ROOT="$root/sys" RESOURCE_CGROUP_ROOT="$root/cgroup" \
  RESOURCE_THERMAL_GLOB="$root/zones/*/temp" RESOURCE_NOW_EPOCH=100000 \
  sh "$tmp/node-resource-profile"
[ -s "$root/expected.json" ] || { echo "the shell writer produced no payload" >&2; exit 1; }
jq -S . "$root/expected.json" >"$here/parity/expected.json"
